// sqlite2mysql 把本地 prod 的 SQLite 快照导入一个**全新的线上 MySQL 库**（回归云端用）。
//
// 用法（-db 必填；目标库已由管理员建好，工具不建库）：
//
//	# 本地 dev 先验证：把快照导进 dev 库，再 `make dev` 起来看
//	go run ./cmd/sqlite2mysql -source /path/xhan_helper.snapshot -to dev -db xhan_helper_test -recreate -confirm=yes
//	# 线上：导进新库（旧库不动）
//	go run ./cmd/sqlite2mysql -source /path/xhan_helper.snapshot -to prod -db xhan_helper_cloud -confirm=yes
//	go run ./cmd/sqlite2mysql -source ... -dry-run        # 只读源、报统计，不碰目标
//
// 为什么导新库：旧的线上库（xhan_helper）原样保留作回退点。导入完成后把 config.prod.yaml 的
// db.database 指向新库、部署重启即可；切库/删旧库由操作者决定，工具既不碰旧库也不建/删库。
//
// 前提：目标库已由管理员建好并授权（工具不会 CREATE DATABASE）：
//
//	CREATE DATABASE xhan_helper_cloud CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci;
//	GRANT ALL PRIVILEGES ON xhan_helper_cloud.* TO 'xhan_helper_user'@'localhost';
//
// 安全闸：-db 必填（绝不默认落到配置里的旧库）；目标库若已有模型表 → 默认拒绝，重灌加 -recreate；
// 目标名与配置默认库同名时告警（prod 下多半是误指旧库，dev 本地验证可忽略）。
// 源用**纯 Go 驱动**只读打开（服务器无 CGO 也能跑），全程不动快照。
//
// 已知需要特殊处理：
//   - llm_cost_details.date / llm_model_prices.effective_date：源里带 "T...Z"，MySQL strict 拒绝，拷贝时截成 10 位。
//   - concept_embeddings：MySQL 库排序规则大小写不敏感，源里大小写不同的同义 concept 会撞唯一索引，按 (type,value,user_id)
//     小写归一保留 created_at 较新者。
//   - 批量 packet：messages 单行可达数百 KB，按字节自适应分批（默认 1 MiB），不套用固定 50 行。
//   - 跳过 llm_call_ios（7 天调试日志，无搬迁价值）。
//   - 时间格式探针（Gate-2）：源经纯 Go 驱动读出的 time.Time 与原始文本必须指向同一绝对时刻，否则中止，改在 Mac 上跑。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// maxBatchBytes 单次 INSERT 的估算字节上限（保守对齐 MySQL 默认 max_allowed_packet=64MB，留足余量）。
const maxBatchBytes = 1 << 20

// maxPlaceholders 单条 INSERT 的占位符预算（MySQL 上限 65535，留足余量）。
// 行数上限 = maxPlaceholders / 列数，与字节上限共同约束每一批。
const maxPlaceholders = 30000

func maxRowsPerBatch(cols int) int {
	if cols < 1 {
		cols = 1
	}
	if n := maxPlaceholders / cols; n >= 1 {
		return n
	}
	return 1
}

// skipTables 不搬迁的表。
var skipTables = map[string]bool{
	"llm_call_ios": true, // 7 天保留的纯调试日志
}

// orphanTables 旧云端 dump 里存在、但已不在 AllModels 的残留表（仅 -recreate 时一并清掉）。
var orphanTables = []string{
	"facts", "fact_changelogs", "missions", "mailbox_items", "pending_findings", "commitments",
}

var (
	source   = flag.String("source", "", "SQLite 快照路径（必填）")
	toEnv    = flag.String("to", "prod", "目标配置的 APP_ENV（MySQL）")
	dbName   = flag.String("db", "", "目标库名（非 dry-run 必填）：prod 用新库名，dev 本地验证用 xhan_helper_test")
	recreate = flag.Bool("recreate", false, "目标库已有模型表时允许 drop 重建（重灌/本地验证用；务必确认 -db 指向的不是线上旧库）")
	confirm  = flag.String("confirm", "", "确认导入，必须为 yes")
	dryRun   = flag.Bool("dry-run", false, "只读源、报统计，不触碰目标库")
)

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)
	t0 := time.Now()

	if *source == "" {
		log.Fatal("[usage] 必须用 -source 指定 SQLite 快照路径")
	}

	// 工具不调 LLM，但 config.Load() 校验 api_key 非空 → 未提供时占位绕过（不覆盖真实值）。
	setenvIfEmpty("LLM_API_KEY", "unused-by-sqlite2mysql")
	setenvIfEmpty("EMBEDDING_API_KEY", "unused-by-sqlite2mysql")

	dstCfg := loadConfig(*toEnv)
	if dstCfg.DB.Driver != "mysql" {
		log.Fatalf("[config] 目标 APP_ENV=%s 的 db.driver=%s，本工具只往 MySQL 搬", *toEnv, dstCfg.DB.Driver)
	}
	defaultDB := dstCfg.DB.Database // 配置里的默认库（prod 下 = 线上旧库/回退点）
	if *dbName != "" {
		dstCfg.DB.Database = *dbName
	} else if !*dryRun {
		// 目标必须显式命名：绝不默认落到配置里的旧库（prod 下即回退点）。
		log.Fatal("[usage] 必须用 -db <目标库名> 显式指定目标（避免默认落到配置里的旧库/回退点）")
	}
	// 目标名与配置默认同名：prod 下很可能是旧库/回退点，告警但不拦（dev 本地验证时默认库就是要导入的目标）。
	if !*dryRun && *dbName == defaultDB {
		log.Printf("[safety] ⚠️  目标库 %q 与配置默认库同名；若它是线上旧库/回退点，请改用新库名（dev 本地验证可忽略）", defaultDB)
	}
	if !*dryRun && *confirm != "yes" {
		log.Fatalf("[safety] 将把整库数据导入 MySQL 库 %q（非空时默认拒绝）。确认请加 -confirm=yes（当前 %q）", dstCfg.DB.Database, *confirm)
	}
	log.Printf("[migrate] 源   sqlite://%s", *source)
	log.Printf("[migrate] 目标 %s://%s:%d/%s", dstCfg.DB.Driver, dstCfg.DB.Host, dstCfg.DB.Port, dstCfg.DB.Database)

	src := openSource(*source)
	if err := probeTimeParsing(src); err != nil {
		log.Fatalf("[probe] %v", err)
	}

	if *dryRun {
		reportSource(src)
		log.Printf("[dry-run] 完成（未触碰目标库）")
		return
	}

	dst := service.NewDB(dstCfg.DB)
	pinSingleConn(dst)
	logMaxAllowedPacket(dst)
	prepareTarget(dst, *recreate)

	written, dropped, err := copyAll(src, dst)
	if err != nil {
		log.Fatalf("[copy] %v\n提示：目标库 %q 写到一半失败——加 -recreate 重跑（仅清本工具模型的表）即可；旧库未受影响", err, dstCfg.DB.Database)
	}
	ok := verifyCounts(dst, written)
	summarize(dst, dropped, time.Since(t0))
	if !ok {
		log.Fatalf("[migrate] 对账存在不一致，见上面 ❌ 行")
	}
	log.Printf("[migrate] 完成，耗时 %s。下一步：把 config.prod.yaml 的 db.database 改为 %s → 部署重启 → 确认 /api/health.activity_enabled=true",
		time.Since(t0).Round(time.Second), dstCfg.DB.Database)
}

func setenvIfEmpty(k, v string) {
	if os.Getenv(k) == "" {
		if err := os.Setenv(k, v); err != nil {
			log.Fatalf("[env] set %s: %v", k, err)
		}
	}
}

func loadConfig(env string) *config.Config {
	if err := os.Setenv("APP_ENV", env); err != nil {
		log.Fatalf("[env] set APP_ENV: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[config] APP_ENV=%s: %v", env, err)
	}
	return cfg
}

func openSource(path string) *gorm.DB {
	// 必须绝对路径：modernc.org/sqlite v1.23.1 在 Linux 上把相对 file: URI
	// （file:./upload/x）误报 "out of memory"，绝对路径正常（Mac 上相对也能开，易漏测）。
	abs, err := filepath.Abs(path)
	if err != nil {
		log.Fatalf("[source] 解析绝对路径 %s: %v", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		log.Fatalf("[source] %v", err)
	}
	// 只读打开（P1）：modernc 对非 file: 前缀一律 READWRITE|CREATE，会建 -wal/-shm；
	// 用 file: URI + mode=ro 让只读成为驱动层硬约束。源应是 .backup 出的单文件快照（无 WAL）。
	db, err := gorm.Open(sqlite.Open(readOnlyDSN(abs)), &gorm.Config{})
	if err != nil {
		log.Fatalf("[source] 打开 %s: %v", path, err)
	}
	return db
}

// readOnlyDSN 把本地路径转成 SQLite file: URI 只读连接串（对空格等做百分号编码）。
func readOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	return u.String()
}

// probeTimeParsing（Gate-2）比对「驱动读出的 time.Time」与「原始文本自己解析出的绝对时刻」。
// 不一致（云端纯 Go 驱动与本地 mattn 写入格式不兼容）→ 中止，改在 Mac 上用 mattn 跑本工具。
func probeTimeParsing(src *gorm.DB) error {
	if !src.Migrator().HasTable("messages") {
		return fmt.Errorf("源库里没有 messages 表，疑似快照不完整")
	}
	var raw string
	if err := src.Raw(
		`SELECT created_at FROM messages WHERE created_at IS NOT NULL AND created_at <> '' ORDER BY created_at DESC LIMIT 1`,
	).Scan(&raw).Error; err != nil {
		return fmt.Errorf("读样本 created_at 原始文本: %w", err)
	}
	if raw == "" {
		return fmt.Errorf("源库 messages 没有可用的 created_at 样本")
	}
	var msg model.Message
	if err := src.Order("created_at DESC").First(&msg).Error; err != nil {
		return fmt.Errorf("经驱动读样本消息: %w", err)
	}
	parsed, err := parseSQLiteTime(raw)
	if err != nil {
		return fmt.Errorf("解析原始时间 %q: %w", raw, err)
	}
	if !msg.CreatedAt.Equal(parsed) {
		return fmt.Errorf(
			"时间解析不一致：驱动 → %s，原始文本 %q 应为 %s。请改在 macOS（mattn 驱动）上跑本工具",
			msg.CreatedAt.Format(time.RFC3339Nano), raw, parsed.Format(time.RFC3339Nano))
	}
	log.Printf("[probe] 时间解析 OK：%q → %s", raw, msg.CreatedAt.Format(time.RFC3339Nano))
	return nil
}

func parseSQLiteTime(s string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		time.RFC3339Nano,
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	}
	var lastErr error
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		} else {
			lastErr = err
		}
	}
	return time.Time{}, lastErr
}

// logMaxAllowedPacket 打印目标库的 max_allowed_packet（分批的硬上界信号）。
func logMaxAllowedPacket(db *gorm.DB) {
	var row struct {
		Name  string
		Value string
	}
	if err := db.Raw("SHOW VARIABLES LIKE 'max_allowed_packet'").Scan(&row).Error; err != nil {
		log.Printf("[target] 读取 max_allowed_packet 失败（不阻断）: %v", err)
		return
	}
	log.Printf("[target] MySQL max_allowed_packet=%s；本工具单批字节上限 maxBatchBytes=%d", row.Value, maxBatchBytes)
}

// pinSingleConn 把连接池压到 1，保证 SET FOREIGN_KEY_CHECKS=0 这类会话级设置贯穿全程。
func pinSingleConn(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("[db] 取底层连接: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
}

// prepareTarget 保证目标 schema 就绪，但不建库/删库、不碰旧数据。
// 目标若已有模型表 → 默认 fatal（防手滑指向有数据的旧库）；-recreate 时才 drop 本工具模型的表 + 已知孤儿表。
func prepareTarget(dst *gorm.DB, recreate bool) {
	models := app.AllModels()
	var existing []string
	for _, t := range modelTableList(dst) {
		if dst.Migrator().HasTable(t.Name) {
			existing = append(existing, t.Name)
		}
	}
	if len(existing) > 0 {
		if !recreate {
			log.Fatalf("[target] 目标库已有 %d 张模型表（如 %s）。若这是旧库请改用新库（-db <新库名>）；确要重灌本工具模型的表再加 -recreate",
				len(existing), strings.Join(firstN(existing, 5), ", "))
		}
		if err := dst.Exec("SET FOREIGN_KEY_CHECKS=0").Error; err != nil {
			log.Fatalf("[target] 关外键检查: %v", err)
		}
		for _, m := range models {
			if err := dst.Migrator().DropTable(m); err != nil {
				log.Fatalf("[target] drop %T: %v", m, err)
			}
		}
		for _, t := range orphanTables {
			if dst.Migrator().HasTable(t) {
				if err := dst.Migrator().DropTable(t); err != nil {
					log.Fatalf("[target] drop 孤儿表 %s: %v", t, err)
				}
				log.Printf("[target] 清掉孤儿表 %s", t)
			}
		}
		log.Printf("[target] -recreate：已清空 %d 张模型表", len(existing))
	}
	if err := dst.AutoMigrate(models...); err != nil {
		// 与主服务不同：目标 schema 没建好就继续拷贝毫无意义，必须 fatal。
		log.Fatalf("[target] AutoMigrate 失败（schema 未就绪，拒绝继续）: %v", err)
	}
	log.Printf("[target] schema 就绪")
}

func firstN(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	return list[:n]
}

// tableModel — 表名 → 模型类型（按 app.AllModels() 顺序逐表搬）。
type tableModel struct {
	Name string
	Type reflect.Type
}

func modelTableList(db *gorm.DB) []tableModel {
	out := make([]tableModel, 0, len(app.AllModels()))
	for _, m := range app.AllModels() {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(m); err != nil {
			log.Fatalf("[copy] 解析模型 %T: %v", m, err)
		}
		out = append(out, tableModel{Name: stmt.Schema.Table, Type: stmt.Schema.ModelType})
	}
	return out
}

// utf8Stats 记录拷贝期的可降级修正次数（供日志，不静默）。
type utf8Stats struct {
	invalidUTF8 int64 // 源文本含非法 UTF-8 的字符串字段数（已替换为 U+FFFD）
}

// copyAll 逐表搬迁，返回「各表计划写入行数」（供对账）与 concept 去重丢弃数。
func copyAll(src, dst *gorm.DB) (map[string]int64, int64, error) {
	stats := &utf8Stats{}
	srcTables, err := src.Migrator().GetTables()
	if err != nil {
		return nil, 0, fmt.Errorf("列出源表: %w", err)
	}
	dstTables, err := dst.Migrator().GetTables()
	if err != nil {
		return nil, 0, fmt.Errorf("列出目标表: %w", err)
	}
	has := func(list []string, t string) bool {
		for _, x := range list {
			if x == t {
				return true
			}
		}
		return false
	}

	written := make(map[string]int64)
	var conceptDropped int64
	var total int64

	for _, m := range modelTableList(src) {
		if skipTables[m.Name] {
			n := countRows(src, m.Name)
			log.Printf("[copy] 跳过 %-24s（按配置排除）%d 行", m.Name, n)
			continue
		}
		if !has(srcTables, m.Name) {
			log.Printf("[copy] 跳过 %-24s 源库没有这张表", m.Name)
			continue
		}
		if !has(dstTables, m.Name) {
			log.Printf("[copy] 跳过 %-24s 目标 schema 没有这张表（不在 AllModels？）", m.Name)
			continue
		}

		if m.Name == "concept_embeddings" {
			n, dr, err := copyConceptEmbeddings(src, dst, stats)
			if err != nil {
				return nil, 0, err
			}
			written[m.Name] = n
			conceptDropped += dr
			total += n
			log.Printf("[copy] %-24s %7d 行（大小写重复丢弃 %d）", m.Name, n, dr)
			continue
		}

		n, err := copyTable(src, dst, m, stats)
		if err != nil {
			return nil, 0, fmt.Errorf("搬 %s: %w", m.Name, err)
		}
		written[m.Name] = n
		total += n
		log.Printf("[copy] %-24s %7d 行", m.Name, n)
	}

	// 源库有、模型清单里没有的表 = 已下线残留（云端 dump 常见）
	for _, t := range srcTables {
		known := false
		for _, m := range modelTableList(src) {
			if m.Name == t {
				known = true
				break
			}
		}
		if known || skipTables[t] {
			continue
		}
		log.Printf("[copy] 跳过 %-24s 模型清单外（已下线残留？）%d 行", t, countRows(src, t))
	}
	log.Printf("[copy] 合计写入 %d 行", total)
	if stats.invalidUTF8 > 0 {
		log.Printf("[copy] ⚠️  源文本含非法 UTF-8 的字段 %d 个（多在 20000/2000 字节处被按字节截断），已替换为 U+FFFD 后入库", stats.invalidUTF8)
	}
	return written, conceptDropped, nil
}

// copyTable 按主键分页读源，按字节预算自适应分批写目标；拷贝时做字段归一（date、非法 UTF-8、零值时间→NULL）。
// 用 map 而非结构体插入：结构体路径下 Go 零值 time.Time 会被写成 '0000-00-00'，MySQL strict 拒收；
// map 路径可对零值时间显式写 NULL。
func copyTable(src, dst *gorm.DB, m tableModel, stats *utf8Stats) (int64, error) {
	stmt := &gorm.Statement{DB: dst}
	if err := stmt.Parse(reflect.New(m.Type).Interface()); err != nil {
		return 0, fmt.Errorf("解析模型 %s: %w", m.Name, err)
	}
	slicePtr := reflect.New(reflect.SliceOf(m.Type))
	maxRows := maxRowsPerBatch(len(stmt.Schema.Fields))
	pending := make([]map[string]any, 0, 64)
	pendingBytes := 0
	var total int64

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if err := dst.Session(&gorm.Session{SkipHooks: true}).
			Table(m.Name).
			Create(&pending).Error; err != nil {
			return fmt.Errorf("写入第 %d 行起的一批（%d 行）: %w", total, len(pending), err)
		}
		total += int64(len(pending))
		pending = make([]map[string]any, 0, 64)
		pendingBytes = 0
		return nil
	}

	err := src.Table(m.Name).FindInBatches(slicePtr.Interface(), 50, func(tx *gorm.DB, batch int) error {
		rows := slicePtr.Elem()
		for i := 0; i < rows.Len(); i++ {
			item := rows.Index(i)
			normalizeRow(item, stats)
			pending = append(pending, structToMap(stmt, item))
			pendingBytes += estimateRowBytes(item.Addr().Interface())
			if pendingBytes >= maxBatchBytes || len(pending) >= maxRows {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		return nil
	}).Error
	if err != nil {
		return total, err
	}
	if err := flush(); err != nil {
		return total, err
	}
	return total, nil
}

// structToMap 按模型 schema 把一行结构体转成「列名→值」；Go 零值 time.Time 一律转 NULL
// （MySQL strict 下 0000-00-00 非法；NULL 才是「未设置」的正确语义）。
func structToMap(stmt *gorm.Statement, item reflect.Value) map[string]any {
	m := make(map[string]any, len(stmt.Schema.Fields))
	for _, f := range stmt.Schema.Fields {
		if f.DBName == "" {
			continue
		}
		val, isZero := f.ValueOf(context.Background(), item)
		if isZero && f.FieldType == reflect.TypeOf(time.Time{}) {
			m[f.DBName] = nil
			continue
		}
		m[f.DBName] = val
	}
	return m
}

// normalizeRow 拷贝期字段归一：
//   - 所有字符串字段：非法 UTF-8（源里被按字节截断）→ 非法字节替换为 U+FFFD，否则 MySQL utf8mb4 拒收；
//   - date 字段：带 T...Z → 截成 YYYY-MM-DD。
func normalizeRow(item reflect.Value, stats *utf8Stats) {
	for i := 0; i < item.NumField(); i++ {
		f := item.Field(i)
		if !f.CanSet() {
			continue
		}
		switch {
		case f.Kind() == reflect.String:
			if s := f.String(); !utf8.ValidString(s) {
				f.SetString(strings.ToValidUTF8(s, "\uFFFD"))
				stats.invalidUTF8++
			}
		case f.Kind() == reflect.Ptr && !f.IsNil() && f.Type().Elem().Kind() == reflect.String:
			// *string 大字段（request_json 等）同样可能含非法 UTF-8，别漏。
			if s := f.Elem().String(); !utf8.ValidString(s) {
				f.Elem().SetString(strings.ToValidUTF8(s, "\uFFFD"))
				stats.invalidUTF8++
			}
		}
	}
	switch v := item.Addr().Interface().(type) {
	case *model.LlmCostDetail:
		v.Date = normalizeDate(v.Date)
	case *model.LlmModelPrice:
		v.EffectiveDate = normalizeDate(v.EffectiveDate)
	}
}

// normalizeDate 仅在字符串带时间成分（含 'T'/'Z'）时截成 YYYY-MM-DD；已是纯日期（或空）原样返回。
// 非法串原样保留，靠插入时报错暴露，不静默改写。
func normalizeDate(s string) string {
	if len(s) >= 10 && strings.ContainsAny(s, "TZ") {
		return s[:10]
	}
	return s
}

// copyConceptEmbeddings 全量读入内存按 (type,value,user_id) 小写归一去重，保留 created_at 较新者。
// concept_embeddings 行小、量不大，全量读入可行；跨页无法去重，故不走分页。
func copyConceptEmbeddings(src, dst *gorm.DB, stats *utf8Stats) (int64, int64, error) {
	var rows []model.ConceptEmbedding
	if err := src.Find(&rows).Error; err != nil {
		return 0, 0, fmt.Errorf("读 concept_embeddings: %w", err)
	}
	out, dropped := dedupConceptEmbeddings(rows)
	stmt := &gorm.Statement{DB: dst}
	if err := stmt.Parse(&model.ConceptEmbedding{}); err != nil {
		return 0, 0, fmt.Errorf("解析 ConceptEmbedding: %w", err)
	}
	for i := range out {
		normalizeRow(reflect.ValueOf(&out[i]).Elem(), stats) // 只做 UTF-8 清洗；无 date 字段
	}

	var written int64
	maxRows := maxRowsPerBatch(len(stmt.Schema.Fields))
	var batch []map[string]any
	bytes := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := dst.Session(&gorm.Session{SkipHooks: true}).
			Table("concept_embeddings").
			Create(&batch).Error; err != nil {
			return fmt.Errorf("写入 concept_embeddings（%d 行）: %w", len(batch), err)
		}
		written += int64(len(batch))
		batch = batch[:0]
		bytes = 0
		return nil
	}
	for i := range out {
		batch = append(batch, structToMap(stmt, reflect.ValueOf(&out[i]).Elem()))
		bytes += estimateRowBytes(out[i])
		if bytes >= maxBatchBytes || len(batch) >= maxRows {
			if err := flush(); err != nil {
				return written, int64(dropped), err
			}
		}
	}
	if err := flush(); err != nil {
		return written, int64(dropped), err
	}
	return written, int64(dropped), nil
}

// dedupConceptEmbeddings 按 (type,value,user_id) 小写归一（对齐 MySQL CI 排序规则），保留 created_at 较新者。
// 返回结果（按 ID 升序，保证确定性）与丢弃行数。纯函数，便于单测。
func dedupConceptEmbeddings(rows []model.ConceptEmbedding) ([]model.ConceptEmbedding, int) {
	keep := make(map[string]model.ConceptEmbedding, len(rows))
	for _, r := range rows {
		k := strings.ToLower(r.Type) + "\x00" + strings.ToLower(r.Value) + "\x00" + r.UserID
		if prev, ok := keep[k]; !ok || r.CreatedAt.After(prev.CreatedAt) {
			keep[k] = r
		}
	}
	out := make([]model.ConceptEmbedding, 0, len(keep))
	for _, r := range keep {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, len(rows) - len(keep)
}

// estimateRowBytes 估算一行写入的字节数，用于按字节分批。
// 必须覆盖 *string / *[]byte（模型里的大字段几乎全是指针，如 Message.RequestJSON、
// Embedding.Embedding）：漏算会让字节预算形同虚设，整表挤进一条 INSERT。
func estimateRowBytes(v any) int {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return 0
	}
	total := 64 // 行/分隔开销估个底
	for i := 0; i < rv.NumField(); i++ {
		total += estimateFieldBytes(rv.Field(i))
	}
	return total
}

// estimateFieldBytes 计单个字段的长度；指针（*string / *[]byte）解引用后计，nil 计 0。
func estimateFieldBytes(f reflect.Value) int {
	if f.Kind() == reflect.Ptr {
		if f.IsNil() {
			return 0
		}
		f = f.Elem()
	}
	switch f.Kind() {
	case reflect.String:
		return f.Len()
	case reflect.Slice:
		if f.Type().Elem().Kind() == reflect.Uint8 {
			return f.Len()
		}
	}
	return 0
}

// verifyCounts 逐表核对「计划写入 vs 目标实际行数」。
func verifyCounts(dst *gorm.DB, written map[string]int64) bool {
	log.Printf("[reconcile] %-24s %10s %10s", "表", "计划", "目标")
	bad := 0
	for _, m := range modelTableList(dst) {
		want, ok := written[m.Name]
		if !ok {
			continue // 跳过/源缺失的表不参与
		}
		var got int64
		if err := dst.Table(m.Name).Count(&got).Error; err != nil {
			log.Fatalf("[reconcile] 数目标 %s: %v", m.Name, err)
		}
		mark := "✅"
		if want != got {
			mark = "❌"
			bad++
		}
		log.Printf("[reconcile] %s %-22s %10d %10d", mark, m.Name, want, got)
	}
	return bad == 0
}

func summarize(dst *gorm.DB, conceptDropped int64, took time.Duration) {
	var users int64
	if err := dst.Model(&model.User{}).Count(&users).Error; err != nil {
		log.Fatalf("[summary] 数 users: %v", err)
	}
	fmt.Println()
	log.Printf("[summary] 耗时 %s，users 表 %d 行（本地库应恰为 1）", took.Round(time.Second), users)
	if conceptDropped > 0 {
		log.Printf("[summary] concept_embeddings 大小写重复丢弃 %d 行（云端库排序规则不敏感所致）", conceptDropped)
	}
	key := []string{
		"conversations", "messages", "conversation_summaries", "user_memories",
		"notes", "bills", "documents", "reminders", "theme_states",
		"embeddings", "embedding_chunks", "concept_embeddings",
	}
	for _, t := range key {
		var n int64
		if err := dst.Table(t).Count(&n).Error; err != nil {
			log.Fatalf("[summary] 数 %s: %v", t, err)
		}
		log.Printf("[summary] %-22s %7d", t, n)
	}
	var span struct{ Min, Max time.Time }
	if err := dst.Table("messages").Select("MIN(created_at) as min, MAX(created_at) as max").Scan(&span).Error; err != nil {
		log.Fatalf("[summary] 取消息时间范围: %v", err)
	}
	if !span.Min.IsZero() {
		log.Printf("[summary] 消息时间范围 %s → %s", span.Min.Format(time.RFC3339), span.Max.Format(time.RFC3339))
	}
}

func countRows(db *gorm.DB, table string) int64 {
	var n int64
	if err := db.Table(table).Count(&n).Error; err != nil {
		log.Printf("[count] 数 %s: %v", table, err)
		return 0
	}
	return n
}

// reportSource 是 dry-run 的体检：逐表行数 + 两个待归一列的异常计数 + concept 重复数。
func reportSource(src *gorm.DB) {
	log.Printf("[dry-run] 源 sbom: %s", *source)
	has := func(t string) bool { return src.Migrator().HasTable(t) }
	for _, m := range modelTableList(src) {
		if !has(m.Name) {
			log.Printf("[dry-run] %-24s （源缺表）", m.Name)
			continue
		}
		log.Printf("[dry-run] %-24s %8d 行%s", m.Name, countRows(src, m.Name), skipLabel(m.Name))
	}
	if has("llm_cost_details") {
		log.Printf("[dry-run] llm_cost_details.date 带 T/Z 需归一：%d 行", countMalformedDates(src, "llm_cost_details", "date"))
	}
	if has("llm_model_prices") {
		log.Printf("[dry-run] llm_model_prices.effective_date 带 T/Z 需归一：%d 行", countMalformedDates(src, "llm_model_prices", "effective_date"))
	}
	if has("concept_embeddings") {
		var rows []model.ConceptEmbedding
		if err := src.Find(&rows).Error; err != nil {
			log.Printf("[dry-run] 读 concept_embeddings 失败: %v", err)
			return
		}
		kept, dropped := dedupConceptEmbeddings(rows)
		log.Printf("[dry-run] concept_embeddings %d 行 → 小写归一后 %d 行（将丢弃 %d）", len(rows), len(kept), dropped)
	}
}

func skipLabel(table string) string {
	if skipTables[table] {
		return "  [跳过]"
	}
	return ""
}

func countMalformedDates(src *gorm.DB, table, col string) int64 {
	var n int64
	q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s LIKE '%%T%%' OR %s LIKE '%%Z%%'`, table, col, col)
	if err := src.Raw(q).Scan(&n).Error; err != nil {
		log.Printf("[dry-run] 数 %s.%s 异常日期: %v", table, col, err)
		return 0
	}
	return n
}
