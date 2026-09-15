package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
)

// LlmCostRepo token 消耗数据访问层
type LlmCostRepo struct {
	db *gorm.DB
}

// NewLlmCostRepo 创建 LlmCostRepo
func NewLlmCostRepo(db *gorm.DB) *LlmCostRepo {
	return &LlmCostRepo{db: db}
}

// Insert 插入一条调用明细。
// detail.Date 应为 "2006-01-02" 格式字符串；如果为空则用当前日期。
func (r *LlmCostRepo) Insert(ctx context.Context, detail *model.LlmCostDetail) error {
	if detail.Date == "" {
		detail.Date = time.Now().Format("2006-01-02")
	}
	return r.db.WithContext(ctx).Create(detail).Error
}

// ============================================================
// 聚合查询
// ============================================================

// QueryParams 聚合查询参数
type QueryParams struct {
	Start    string   // 开始日期 YYYY-MM-DD（含）
	End      string   // 结束日期 YYYY-MM-DD（含）
	UserIDs  []string // 可选，多选用户；为空 = 所有用户
	Category string   // 可选，按大类筛选
	Models   []string // 可选，多选模型；为空 = 所有模型
	GroupBy  string   // 分组字段：date / category / model / user_id / sub_category
}

// TokenTotal 一段数据的 token 汇总（含缓存拆分）
type TokenTotal struct {
	PromptCacheHit  int64   `json:"prompt_cache_hit"`
	PromptCacheMiss int64   `json:"prompt_cache_miss"`
	Completion      int64   `json:"completion"`
	TotalTokens     int64   `json:"total_tokens"`
	CallCount       int64   `json:"call_count"`
	CacheHitRate    float64 `json:"cache_hit_rate"`
}

// GroupRow 单个分组行
type GroupRow struct {
	Key string `json:"key"`
	TokenTotal
}

// GroupRows
type GroupRows []GroupRow

// Len 返回切片长度
func (g GroupRows) Len() int {
	return len(g)
}

// Less 默认从小到大排序，按 Key 比较
func (g GroupRows) Less(i, j int) bool {
	return g[i].Key < g[j].Key
}

// Swap 交换两个元素
func (g GroupRows) Swap(i, j int) {
	g[i], g[j] = g[j], g[i]
}

// QueryResult 聚合查询结果
type QueryResult struct {
	Total  TokenTotal `json:"total"`
	Groups []GroupRow `json:"groups"`
}

var validDims = map[string]bool{
	"date": true, "category": true, "model": true, "user_id": true, "sub_category": true,
}

// dimToColumn 将查询参数维度名转为数据库列名（防注入）。
func dimToColumn(dim string) string {
	switch dim {
	case "date":
		return "date"
	case "category":
		return "category"
	case "model":
		return "model"
	case "user_id":
		return "user_id"
	case "sub_category":
		return "sub_category"
	default:
		return "date"
	}
}

const tokenSelectCols = "COALESCE(SUM(prompt_cache_hit_tokens),0) as prompt_cache_hit," +
	"COALESCE(SUM(prompt_cache_miss_tokens),0) as prompt_cache_miss," +
	"COALESCE(SUM(completion_tokens),0) as completion," +
	"COALESCE(SUM(total_tokens),0) as total_tokens," +
	"COUNT(*) as call_count"

// Query 执行聚合查询。
func (r *LlmCostRepo) Query(ctx context.Context, params QueryParams) (*QueryResult, error) {
	if !validDims[params.GroupBy] {
		return nil, fmt.Errorf("invalid group_by: %s", params.GroupBy)
	}

	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{})
	if params.Start != "" {
		q = q.Where("date >= ?", params.Start)
	}
	if params.End != "" {
		q = q.Where("date <= ?", params.End)
	}
	if len(params.UserIDs) > 0 {
		q = q.Where("user_id IN ?", params.UserIDs)
	}
	if params.Category != "" {
		q = q.Where("category = ?", params.Category)
	}
	if len(params.Models) > 0 {
		q = q.Where("model IN ?", params.Models)
	}

	// --- 总额 ---
	var total TokenTotal
	if err := q.Select(tokenSelectCols).Scan(&total).Error; err != nil {
		return nil, fmt.Errorf("query totals: %w", err)
	}
	total.CacheHitRate = cacheHitRate(total.PromptCacheHit, total.PromptCacheMiss)

	// --- 分组 ---
	groupCol := dimToColumn(params.GroupBy)
	var groups []GroupRow
	if err := q.Select(groupCol + " as `key`," + tokenSelectCols).
		Group(groupCol).
		Order("total_tokens DESC").
		Scan(&groups).Error; err != nil {
		return nil, fmt.Errorf("query groups: %w", err)
	}
	for i := range groups {
		groups[i].Key = normalizeDateKey(groups[i].Key)
		groups[i].CacheHitRate = cacheHitRate(groups[i].PromptCacheHit, groups[i].PromptCacheMiss)
	}
	if groups == nil {
		groups = []GroupRow{}
	}
	var groupRows GroupRows = groups
	sort.Sort(groupRows)

	return &QueryResult{Total: total, Groups: groupRows}, nil
}

// TotalsByConversations 按 conv_id 聚合一批会话的 token 汇总（GROUP BY conv_id）。
// 返回 map[convID]TokenTotal；不区分 sub_category（子任务列表直接展示该子会话全部消耗）。
func (r *LlmCostRepo) TotalsByConversations(ctx context.Context, convIDs []string) (map[string]TokenTotal, error) {
	out := make(map[string]TokenTotal)
	if len(convIDs) == 0 {
		return out, nil
	}
	var rows []GroupRow
	if err := r.db.WithContext(ctx).Model(&model.LlmCostDetail{}).
		Select("conv_id as `key`,"+tokenSelectCols).
		Where("conv_id IN ?", convIDs).
		Group("conv_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].CacheHitRate = cacheHitRate(rows[i].PromptCacheHit, rows[i].PromptCacheMiss)
		out[rows[i].Key] = rows[i].TokenTotal
	}
	return out, nil
}

// ConversationCost 计算会话总消耗，以及 windowStart 之后的窗口消耗（元）。
// windowStart 为零值表示不区分窗口（窗口=总）。同时返回最近一条明细的模型名，
// 供 UI 按档位查上下文窗口上限。
func (r *LlmCostRepo) ConversationCost(ctx context.Context, convID string, windowStart time.Time, prices []model.LlmModelPrice) (total, window float64, latestModel string, err error) {
	if convID == "" {
		return 0, 0, "", nil
	}
	return r.costByConvIDs(ctx, []string{convID}, windowStart, prices)
}

// SumCostByConvIDs 汇总一批会话的消耗金额（元），并额外返回 windowStart 之后的窗口消耗。
// windowStart 为零值表示不区分窗口（窗口=总）。convIDs 为空返回 0。
// 用于把子会话（subAgent）成本并入父会话合计。
func (r *LlmCostRepo) SumCostByConvIDs(ctx context.Context, convIDs []string, windowStart time.Time, prices []model.LlmModelPrice) (total, window float64, err error) {
	if len(convIDs) == 0 {
		return 0, 0, nil
	}
	total, window, _, err = r.costByConvIDs(ctx, convIDs, windowStart, prices)
	return total, window, err
}

// costByConvIDs 汇总一批会话的消耗。windowStart 非零时额外返回其后的窗口消耗。
// 与 ExpenseQuery 同口径：先按 (date, model, 北京时分, 是否在窗口内) 分桶，
// 再逐桶匹配价格，避免逐行解析峰谷 JSON。
func (r *LlmCostRepo) costByConvIDs(ctx context.Context, convIDs []string, windowStart time.Time, prices []model.LlmModelPrice) (total, window float64, latestModel string, err error) {
	byModel := make(map[string][]model.LlmModelPrice)
	for _, p := range prices {
		byModel[p.Model] = append(byModel[p.Model], p)
	}
	for m := range byModel {
		sort.Slice(byModel[m], func(i, j int) bool {
			return byModel[m][i].EffectiveDate < byModel[m][j].EffectiveDate
		})
	}

	var rows []ExpenseRow
	if err := r.db.WithContext(ctx).Model(&model.LlmCostDetail{}).
		Select("date, model, category, created_at,"+
			"prompt_cache_hit_tokens as hit,"+
			"prompt_cache_miss_tokens as miss,"+
			"completion_tokens as output").
		Where("conv_id IN ?", convIDs).
		Scan(&rows).Error; err != nil {
		return 0, 0, "", fmt.Errorf("query conversation cost rows: %w", err)
	}

	type aggKey struct {
		date, model, hm string
		inWindow        bool
	}
	type agg struct{ hit, miss, output int64 }
	buckets := make(map[aggKey]*agg)
	var latestTime time.Time
	for _, row := range rows {
		if row.CreatedAt.After(latestTime) {
			latestTime = row.CreatedAt
			latestModel = row.Model
		}
		inWindow := windowStart.IsZero() || !row.CreatedAt.Before(windowStart)
		key := aggKey{normalizeDateKey(row.Date), row.Model, row.CreatedAt.In(config.BJ).Format("15:04"), inWindow}
		b := buckets[key]
		if b == nil {
			b = &agg{}
			buckets[key] = b
		}
		b.hit += row.Hit
		b.miss += row.Miss
		b.output += row.Output
	}

	for key, b := range buckets {
		ps, _, ok := priceAt(byModel[key.model], key.date, key.hm)
		if !ok {
			continue // 未定价模型不计入
		}
		cost := float64(b.hit)/1e6*ps.hit + float64(b.miss)/1e6*ps.miss + float64(b.output)/1e6*ps.output
		total += cost
		if key.inWindow {
			window += cost
		}
	}
	return total, window, latestModel, nil
}

// normalizeDateKey SQLite 驱动可能把 date 列读回 RFC3339（"2026-08-11T00:00:00Z"），
// MySQL 返回纯日期串（"2026-08-11"）。统一截取日期部分。
func normalizeDateKey(s string) string {
	if len(s) > 10 && s[4] == '-' {
		return s[:10]
	}
	return s
}

// cacheHitRate 缓存命中率：hit / (hit + miss)，分母为 0 返回 0。
func cacheHitRate(hit, miss int64) float64 {
	if hit+miss == 0 {
		return 0
	}
	return float64(hit) / float64(hit+miss)
}

// ============================================================
// 明细 / 用户列表 / 汇总
// ============================================================

// ListParams 明细分页查询参数
type ListParams struct {
	Start    string
	End      string
	UserIDs  []string
	Category string
	Models   []string
	Date     string // 精确日期筛选
}

// ListDetails 分页查询调用明细（created_at DESC）
func (r *LlmCostRepo) ListDetails(ctx context.Context, params ListParams, page, pageSize int) ([]model.LlmCostDetail, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}

	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{})
	if params.Start != "" {
		q = q.Where("date >= ?", params.Start)
	}
	if params.End != "" {
		q = q.Where("date <= ?", params.End)
	}
	if len(params.UserIDs) > 0 {
		q = q.Where("user_id IN ?", params.UserIDs)
	}
	if params.Category != "" {
		q = q.Where("category = ?", params.Category)
	}
	if len(params.Models) > 0 {
		q = q.Where("model IN ?", params.Models)
	}
	if params.Date != "" {
		q = q.Where("date = ?", params.Date)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count details: %w", err)
	}

	var rows []model.LlmCostDetail
	err := q.Order("created_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error
	if err != nil {
		return nil, 0, fmt.Errorf("list details: %w", err)
	}
	for i := range rows {
		rows[i].Date = normalizeDateKey(rows[i].Date)
	}
	return rows, total, nil
}

// ============================================================
// 花销计算（价格匹配在内存完成）
// ============================================================

// ExpenseRow 明细行（价格匹配的输入）。
// 不做 SQL 聚合：峰谷时段必须按北京时间换算，SQL 字符串函数提取时分在不同方言/时区下不可靠，
// 因此在 Go 内用 CreatedAt.In(config.BJ) 换算后内存聚合。
// ponytail: 个人量级（30 天数万行）内存扫描毫秒级；若数据量级到百万行/月，
// 升级为写时落 time_of_day 列（db_recorder 计算，存 "HH:MM" 北京时间字符串）+ SQL GROUP BY。
type ExpenseRow struct {
	Date      string
	Model     string
	Category  string
	CreatedAt time.Time
	Hit       int64
	Miss      int64
	Output    int64
}

// ExpenseDay 单日花销（含命中/未命中/生成与峰谷拆分）
type ExpenseDay struct {
	Date       string  `json:"date"`
	Cost       float64 `json:"cost"`
	CostHit    float64 `json:"cost_hit"`
	CostMiss   float64 `json:"cost_miss"`
	CostOutput float64 `json:"cost_output"`
	CostPeak   float64 `json:"cost_peak"`
	CostOff    float64 `json:"cost_off"`
}

// ExpenseGroup 按分类/模型聚合的花销
type ExpenseGroup struct {
	Key  string  `json:"key"`
	Cost float64 `json:"cost"`
}

// ExpenseResult 花销聚合结果
type ExpenseResult struct {
	Total          float64        `json:"total"`
	TotalHit       float64        `json:"total_hit"`
	TotalMiss      float64        `json:"total_miss"`
	TotalOutput    float64        `json:"total_output"`
	TotalPeak      float64        `json:"total_peak"`
	TotalOff       float64        `json:"total_off"`
	Days           []ExpenseDay   `json:"days"`
	Categories     []ExpenseGroup `json:"categories"`
	Models         []ExpenseGroup `json:"models"`
	UnpricedModels []string       `json:"unpriced_models"`
}

type priceSet struct {
	hit, miss, output float64
}

// priceAt 返回某模型在指定日期+北京时刻的有效价格。
// 生效规则：effective_date <= date 的最新一条；峰段内用峰价，其余用谷价。
// 第二返回值表示该时刻是否峰价；false 表示该模型在 date 无有效价格记录。
func priceAt(schedule []model.LlmModelPrice, date, hm string) (priceSet, bool, bool) {
	// schedule 已按 effective_date 升序；倒序找第一条 <= date 的即可
	var cur *model.LlmModelPrice
	for i := len(schedule) - 1; i >= 0; i-- {
		if schedule[i].EffectiveDate <= date {
			cur = &schedule[i]
			break
		}
	}
	if cur == nil {
		return priceSet{}, false, false
	}
	segs, err := parsePeakSegmentsJSON(cur.PeakSegments)
	if err != nil {
		log.Printf("[cost] invalid peak segments for %s@%s: %v", cur.Model, cur.EffectiveDate, err)
		segs = nil
	}
	peak := isPeak(hm, segs)
	if peak {
		return priceSet{cur.PriceHit, cur.PriceMiss, cur.PriceOutput}, true, true
	}
	return priceSet{cur.PriceHitOff, cur.PriceMissOff, cur.PriceOutputOff}, false, true
}

// ExpenseQuery 计算指定范围的花销。
// prices 为 llm_model_prices 全量记录（由 controller 拉取后传入），内存按模型+日期+北京时刻匹配。
func (r *LlmCostRepo) ExpenseQuery(ctx context.Context, params QueryParams, prices []model.LlmModelPrice) (*ExpenseResult, error) {
	// 按模型分组、按生效日期升序的价格表
	byModel := make(map[string][]model.LlmModelPrice)
	for _, p := range prices {
		byModel[p.Model] = append(byModel[p.Model], p)
	}
	for m := range byModel {
		sort.Slice(byModel[m], func(i, j int) bool {
			return byModel[m][i].EffectiveDate < byModel[m][j].EffectiveDate
		})
	}

	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{})
	if params.Start != "" {
		q = q.Where("date >= ?", params.Start)
	}
	if params.End != "" {
		q = q.Where("date <= ?", params.End)
	}
	if len(params.UserIDs) > 0 {
		q = q.Where("user_id IN ?", params.UserIDs)
	}
	if params.Category != "" {
		q = q.Where("category = ?", params.Category)
	}
	if len(params.Models) > 0 {
		q = q.Where("model IN ?", params.Models)
	}

	var rows []ExpenseRow
	if err := q.Select("date, model, category, created_at," +
		"prompt_cache_hit_tokens as hit," +
		"prompt_cache_miss_tokens as miss," +
		"completion_tokens as output").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query expense rows: %w", err)
	}

	// 内存聚合：key = date + model + category + 北京时分
	type aggKey struct{ date, model, category, hm string }
	type agg struct{ hit, miss, output int64 }
	buckets := make(map[aggKey]*agg)
	for _, row := range rows {
		hm := row.CreatedAt.In(config.BJ).Format("15:04")
		key := aggKey{normalizeDateKey(row.Date), row.Model, row.Category, hm}
		b := buckets[key]
		if b == nil {
			b = &agg{}
			buckets[key] = b
		}
		b.hit += row.Hit
		b.miss += row.Miss
		b.output += row.Output
	}

	res := &ExpenseResult{Days: []ExpenseDay{}, Categories: []ExpenseGroup{}, Models: []ExpenseGroup{}, UnpricedModels: []string{}}
	dayIdx := make(map[string]int)
	catCost := make(map[string]float64)
	modelCost := make(map[string]float64)
	unpriced := make(map[string]bool)

	for key, b := range buckets {
		ps, peak, ok := priceAt(byModel[key.model], key.date, key.hm)
		if !ok {
			unpriced[key.model] = true
			continue
		}
		hitCost := float64(b.hit) / 1e6 * ps.hit
		missCost := float64(b.miss) / 1e6 * ps.miss
		outCost := float64(b.output) / 1e6 * ps.output
		cost := hitCost + missCost + outCost

		res.Total += cost
		res.TotalHit += hitCost
		res.TotalMiss += missCost
		res.TotalOutput += outCost

		idx, ok := dayIdx[key.date]
		if !ok {
			idx = len(res.Days)
			dayIdx[key.date] = idx
			res.Days = append(res.Days, ExpenseDay{Date: key.date})
		}
		res.Days[idx].Cost += cost
		res.Days[idx].CostHit += hitCost
		res.Days[idx].CostMiss += missCost
		res.Days[idx].CostOutput += outCost
		if peak {
			res.TotalPeak += cost
			res.Days[idx].CostPeak += cost
		} else {
			res.TotalOff += cost
			res.Days[idx].CostOff += cost
		}

		catCost[key.category] += cost
		modelCost[key.model] += cost
	}

	for m := range unpriced {
		res.UnpricedModels = append(res.UnpricedModels, m)
	}
	sort.Strings(res.UnpricedModels)

	for key, cost := range catCost {
		res.Categories = append(res.Categories, ExpenseGroup{Key: key, Cost: cost})
	}
	for key, cost := range modelCost {
		res.Models = append(res.Models, ExpenseGroup{Key: key, Cost: cost})
	}
	sort.Slice(res.Categories, func(i, j int) bool { return res.Categories[i].Cost > res.Categories[j].Cost })
	sort.Slice(res.Models, func(i, j int) bool { return res.Models[i].Cost > res.Models[j].Cost })

	sort.Slice(res.Days, func(i, j int) bool { return res.Days[i].Date < res.Days[j].Date })

	return res, nil
}

// ============================================================
// 单日分时（24 小时）聚合
// ============================================================

// HourlyBucket 单个小时桶
type HourlyBucket struct {
	Hour       string  `json:"hour"` // "00".."23"
	Hit        int64   `json:"hit"`
	Miss       int64   `json:"miss"`
	Output     int64   `json:"output"`
	Cost       float64 `json:"cost"`
	CostHit    float64 `json:"cost_hit"`
	CostMiss   float64 `json:"cost_miss"`
	CostOutput float64 `json:"cost_output"`
}

// HourlyTotals 单日总量（峰谷拆分）
type HourlyTotals struct {
	Hit        int64   `json:"hit"`
	Miss       int64   `json:"miss"`
	Output     int64   `json:"output"`
	Cost       float64 `json:"cost"`
	CostHit    float64 `json:"cost_hit"`
	CostMiss   float64 `json:"cost_miss"`
	CostOutput float64 `json:"cost_output"`
	CostPeak   float64 `json:"cost_peak"`
	CostOff    float64 `json:"cost_off"`
}

// HourlyResult 单日分时聚合结果
type HourlyResult struct {
	Date           string         `json:"date"`
	Hours          []HourlyBucket `json:"hours"`
	Totals         HourlyTotals   `json:"totals"`
	UnpricedModels []string       `json:"unpriced_models"`
}

// HourlyQuery 计算指定日期的分时（24 小时）token 与花销。
// 逐行按真实分钟判峰谷（分钟精度不受小时桶影响），再聚到小时桶。
func (r *LlmCostRepo) HourlyQuery(ctx context.Context, date string, params QueryParams, prices []model.LlmModelPrice) (*HourlyResult, error) {
	byModel := make(map[string][]model.LlmModelPrice)
	for _, p := range prices {
		byModel[p.Model] = append(byModel[p.Model], p)
	}
	for m := range byModel {
		sort.Slice(byModel[m], func(i, j int) bool {
			return byModel[m][i].EffectiveDate < byModel[m][j].EffectiveDate
		})
	}

	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{}).Where("date = ?", date)
	if len(params.UserIDs) > 0 {
		q = q.Where("user_id IN ?", params.UserIDs)
	}
	if params.Category != "" {
		q = q.Where("category = ?", params.Category)
	}
	if len(params.Models) > 0 {
		q = q.Where("model IN ?", params.Models)
	}

	var rows []ExpenseRow
	if err := q.Select("date, model, category, created_at," +
		"prompt_cache_hit_tokens as hit," +
		"prompt_cache_miss_tokens as miss," +
		"completion_tokens as output").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("query hourly rows: %w", err)
	}

	res := &HourlyResult{
		Date:           date,
		Hours:          make([]HourlyBucket, 24),
		UnpricedModels: []string{},
	}
	for i := range res.Hours {
		res.Hours[i].Hour = fmt.Sprintf("%02d", i)
	}
	unpriced := make(map[string]bool)

	for _, row := range rows {
		bj := row.CreatedAt.In(config.BJ)
		hm := bj.Format("15:04")
		h := bj.Hour()

		ps, peak, ok := priceAt(byModel[row.Model], normalizeDateKey(row.Date), hm)
		var hitCost, missCost, outCost, cost float64
		if !ok {
			unpriced[row.Model] = true
		} else {
			hitCost = float64(row.Hit) / 1e6 * ps.hit
			missCost = float64(row.Miss) / 1e6 * ps.miss
			outCost = float64(row.Output) / 1e6 * ps.output
			cost = hitCost + missCost + outCost
		}

		b := &res.Hours[h]
		b.Hit += row.Hit
		b.Miss += row.Miss
		b.Output += row.Output
		b.Cost += cost
		b.CostHit += hitCost
		b.CostMiss += missCost
		b.CostOutput += outCost

		res.Totals.Hit += row.Hit
		res.Totals.Miss += row.Miss
		res.Totals.Output += row.Output
		res.Totals.Cost += cost
		res.Totals.CostHit += hitCost
		res.Totals.CostMiss += missCost
		res.Totals.CostOutput += outCost
		if peak {
			res.Totals.CostPeak += cost
		} else {
			res.Totals.CostOff += cost
		}
	}

	for m := range unpriced {
		res.UnpricedModels = append(res.UnpricedModels, m)
	}
	sort.Strings(res.UnpricedModels)

	return res, nil
}

// UserCostSummary 用户在统计时段内的消耗汇总（下拉列表排序用）
type UserCostSummary struct {
	UserID      string `json:"user_id"`
	TotalTokens int64  `json:"total_tokens"`
	CallCount   int64  `json:"call_count"`
}

// ListModels 列出有成本记录的模型名（去重，供定价表单下拉选择）。
func (r *LlmCostRepo) ListModels(ctx context.Context) ([]string, error) {
	var rows []string
	if err := r.db.WithContext(ctx).Model(&model.LlmCostDetail{}).
		Distinct().Order("model").Pluck("model", &rows).Error; err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	if rows == nil {
		rows = []string{}
	}
	return rows, nil
}

// ListUsers 列出有成本记录的用户（total_tokens DESC），userID 非空过滤。
func (r *LlmCostRepo) ListUsers(ctx context.Context, start, end string) ([]UserCostSummary, error) {
	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{}).
		Select("user_id, SUM(total_tokens) as total_tokens, COUNT(*) as call_count").
		Where("user_id != ''")
	if start != "" {
		q = q.Where("date >= ?", start)
	}
	if end != "" {
		q = q.Where("date <= ?", end)
	}
	var rows []UserCostSummary
	err := q.Group("user_id").Order("total_tokens DESC").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return rows, nil
}

// Summary 返回时间段内的汇总（不含分组）
func (r *LlmCostRepo) Summary(ctx context.Context, start, end string, userIDs, models []string, category string) (*TokenTotal, error) {
	q := r.db.WithContext(ctx).Model(&model.LlmCostDetail{})
	if start != "" {
		q = q.Where("date >= ?", start)
	}
	if end != "" {
		q = q.Where("date <= ?", end)
	}
	if len(userIDs) > 0 {
		q = q.Where("user_id IN ?", userIDs)
	}
	if category != "" {
		q = q.Where("category = ?", category)
	}
	if len(models) > 0 {
		q = q.Where("model IN ?", models)
	}
	var total TokenTotal
	if err := q.Select(tokenSelectCols).Scan(&total).Error; err != nil {
		return nil, fmt.Errorf("query summary: %w", err)
	}
	total.CacheHitRate = cacheHitRate(total.PromptCacheHit, total.PromptCacheMiss)
	return &total, nil
}
