// activity-replay —— 09-25 基准日回放（实施技术方案 阶段 3 / 施工进度 P17）。
//
// 用途：从主库存量数据重建某天的**服务端事件**（chat/llm/tool/系统变更），灌进一份独立 events.db，
// 跑派生（Recompute），打印每段 session 的起止与依据，供人工对照设计篇 1.1 表里「服务端可验」的几段。
//
// 硬约束：主库**只读**；派生结果写独立临时 events.db（--out），绝不碰 prod 的 events.db。
// 派生层（session/daystat）的切日时区可用 activity.SetDayLocation 覆盖（现已提供）；
// 本工具沿用进程本地时区，故一律按进程本地时区过滤与展示——换时区请设 TZ 环境变量，
// 不要另传 --tz（会造成派生与报告错位）。
// 客户端侧（浏览/在场/电源）09-25 没有数据、不回填，本工具不造这些事件。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"xhan-helper/internal/activity"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	date := flag.String("date", "2026-09-25", "回放日期（进程本地日，YYYY-MM-DD）")
	out := flag.String("out", "", "输出 events.db 路径（默认 /tmp/activity-replay-<date>.db）")
	dbPath := flag.String("db", "", "主库路径（SQLite，只读打开；默认本机 prod 库）")
	force := flag.Bool("force", false, "允许 --out 指向 prod 事件库同名文件（危险，默认拒绝）")
	noRecompute := flag.Bool("no-recompute", false, "只灌事件、不跑派生（调试用）")
	flag.Parse()

	mainDBPath := *dbPath
	if mainDBPath == "" {
		p, err := defaultMainDB()
		if err != nil {
			log.Fatalf("解析默认主库路径: %v", err)
		}
		mainDBPath = p
	}

	// 派生层按进程本地时区切日；这里用同一时区。
	location := time.Local
	dayStart := mustDate(*date, location)
	dayEnd := dayStart.Add(24 * time.Hour)

	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(os.TempDir(), "activity-replay-"+*date+".db")
	}
	// 守卫：绝不覆盖 prod 的 events.db（原始客户端事件不可重建，抹掉即不可逆丢失）。
	if err := guardOutput(outPath, mainDBPath, *force); err != nil {
		log.Fatalf("%v", err)
	}
	removeStaleOutput(outPath)

	log.Printf("[replay] 主库(只读)=%s 日期=%s (%s) 输出=%s", mainDBPath, *date, location, outPath)
	mainDB, err := openReadOnly(mainDBPath)
	if err != nil {
		log.Fatalf("打开主库只读: %v", err)
	}

	events, err := collectEvents(mainDB, dayStart, dayEnd)
	if err != nil {
		log.Fatalf("重建事件: %v", err)
	}
	changeEvents, err := collectChangeEvents(mainDB, dayStart, dayEnd)
	if err != nil {
		log.Fatalf("重建系统变更事件: %v", err)
	}
	events = append(events, changeEvents...)
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	log.Printf("[replay] 重建服务端事件 %d 条（含系统变更 %d）", len(events), len(changeEvents))

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		log.Fatalf("创建输出目录: %v", err)
	}
	store, err := activity.Open(outPath, false)
	if err != nil {
		log.Fatalf("打开输出 events.db: %v", err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			log.Printf("[replay] 关闭 events.db: %v", cerr)
		}
	}()

	for i, ev := range events {
		store.Emit(ev)
		// 背压：emit 队列有界（默认 1024），一次性灌几千条会丢最旧的。
		// 每 500 条主动 Flush 一次把队列排空，保证全量落库（回放是离线批处理，慢一点无妨）。
		if i%500 == 499 {
			store.Flush()
		}
	}
	store.Flush()

	if !*noRecompute {
		if err := store.Recompute(); err != nil {
			log.Fatalf("派生重算: %v", err)
		}
	}

	rep, err := buildReport(store, dayStart, location)
	if err != nil {
		log.Fatalf("生成对照报告: %v", err)
	}
	rep.print(os.Stdout, *date)
}

// guardOutput 拒绝把输出指向 prod 事件库（同目录同名 events.db），除非显式 --force。
func guardOutput(outPath, mainDBPath string, force bool) error {
	if force {
		return nil
	}
	if filepath.Base(outPath) == "events.db" ||
		filepath.Dir(mustAbs(outPath)) == filepath.Dir(mustAbs(mainDBPath)) {
		return fmt.Errorf("输出路径 %q 疑似指向真实事件库（与主库同目录或名为 events.db）；如确认要覆盖请加 --force", outPath)
	}
	return nil
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// removeStaleOutput 删除旧输出（含 WAL/SHM 残留）保证从零重建。
// 删除失败仅在文件存在时报错；不存在属正常，不静默吞。
func removeStaleOutput(outPath string) {
	for _, p := range []string{outPath, outPath + "-wal", outPath + "-shm"} {
		err := os.Remove(p)
		if err == nil || os.IsNotExist(err) {
			continue
		}
		log.Fatalf("清理旧输出 %s: %v", p, err)
	}
}

func mustDate(s string, loc *time.Location) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		log.Fatalf("解析日期 %q: %v", s, err)
	}
	return t
}

// openReadOnly 以只读方式打开主库，禁止任何写（防手滑污染真库）。
func openReadOnly(path string) (*gorm.DB, error) {
	// 只读必须走 file: URI（纯 Go 驱动对非 file: 前缀一律 READWRITE|CREATE）。
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=busy_timeout(5000)"}).String()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// ——— 主库行 → activity.Event ———

type msgRow struct {
	ID             string
	ConversationID string
	Role           string
	Content        string
	ToolCallsJSON  *string
	ToolCallID     *string
	CreatedAt      time.Time
}

type llmRow struct {
	ID               string
	ConvID           string
	Model            string
	PromptTokens     int
	CompletionTokens int
	LatencyMs        int
	ErrorMsg         *string
	CreatedAt        time.Time
}

// collectEvents 按「turn 为核心」重建服务端事件：
// 每个 conversation 里，一条 user 消息开一个 turn（同时落 chat.message.sent，作根点事件）；
// 其后的 assistant/tool 消息挂该 turn 下（turn 收边用该轮最后一条消息时刻）。
// llm.call.* 由 llm_call_ios 行重建，挂到同 conv 当天最近的 turn。
func collectEvents(db *gorm.DB, dayStart, dayEnd time.Time) ([]activity.Event, error) {
	var msgs []msgRow
	if err := db.Raw(`
		SELECT id, conversation_id, role, COALESCE(content,'') AS content, tool_calls_json, tool_call_id, created_at
		FROM messages
		WHERE created_at >= ? AND created_at < ?
		ORDER BY created_at, id`, dayStart, dayEnd).Scan(&msgs).Error; err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}

	turns := buildTurns(msgs)
	turnByConv := indexTurnsByConv(turns)

	var out []activity.Event
	for _, t := range turns {
		// user 消息 = 根点事件（生产：orchestrator.emitUserMessageEvent），turn 挂它下面。
		out = append(out, activity.Event{
			Type: activity.TypeChatMessageSent, EventID: t.userMsgEventID,
			OccurredAt: t.started.OccurredAt, Actor: activity.ActorUser, Trigger: activity.TriggerManual,
			ConversationID: t.conv, BizKey: t.userMsgID,
			Attrs: map[string]any{"message_id": t.userMsgID},
		})
		out = append(out, t.started, t.ended)
		for _, m := range t.messages {
			out = append(out, toolCallEvents(m, t)...)
		}
	}

	// llm.call.*
	var llms []llmRow
	if err := db.Raw(`
		SELECT id, conv_id, model, prompt_tokens, completion_tokens, latency_ms, error_msg, created_at
		FROM llm_call_ios
		WHERE created_at >= ? AND created_at < ?
		ORDER BY created_at, id`, dayStart, dayEnd).Scan(&llms).Error; err != nil {
		return nil, fmt.Errorf("query llm_call_ios: %w", err)
	}
	for _, l := range llms {
		turnID, parentID := nearestTurn(turnByConv[l.ConvID], l.CreatedAt)
		startID, endID := detID("llm-s", l.ID), detID("llm-e", l.ID)
		attrs := map[string]any{"model": l.Model, "prompt_tokens": l.PromptTokens, "completion_tokens": l.CompletionTokens}
		out = append(out,
			activity.Event{
				Type: activity.TypeLLMCallStarted, EventID: startID,
				OccurredAt: l.CreatedAt, Actor: activity.ActorAssistant, Trigger: activity.TriggerManual,
				ConversationID: l.ConvID, TurnID: turnID, ParentEventID: parentID,
				CallID: l.ID, BizKey: l.ID, PairID: endID,
			},
			activity.Event{
				Type: activity.TypeLLMCallEnded, EventID: endID,
				OccurredAt: l.CreatedAt.Add(time.Duration(l.LatencyMs) * time.Millisecond),
				Actor:      activity.ActorAssistant, Trigger: activity.TriggerManual,
				ConversationID: l.ConvID, TurnID: turnID, ParentEventID: parentID,
				CallID: l.ID, BizKey: l.ID, PairID: startID,
				Outcome: outcomeFor(l.ErrorMsg), ErrorMsg: deref(l.ErrorMsg), Attrs: attrs,
			},
		)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].OccurredAt.Before(out[j].OccurredAt) })
	return out, nil
}

// collectChangeEvents 从主库重建系统变更事件：documents / user_memories 的 created_at / updated_at。
// 这些是 §1.1「系统变更事件」那几段（12:03–14:42 备份/topic 清洗、18:31 记忆代码）的证据来源。
// trigger 统一 batch：回放无法还原当时的 turn 上下文，且这些变更多为后台 worker 产物。
func collectChangeEvents(db *gorm.DB, dayStart, dayEnd time.Time) ([]activity.Event, error) {
	var out []activity.Event

	var docs []struct {
		ID        string
		Title     string
		CreatedAt time.Time
		UpdatedAt time.Time
	}
	if err := db.Raw(`SELECT id, COALESCE(title,'') AS title, created_at, updated_at FROM documents
		WHERE created_at >= ? AND created_at < ?`, dayStart, dayEnd).Scan(&docs).Error; err != nil {
		return nil, fmt.Errorf("query documents: %w", err)
	}
	for _, d := range docs {
		out = append(out, activity.Event{
			Type: activity.TypeDocCreated, EventID: detID("doc-c", d.ID),
			OccurredAt: d.CreatedAt, Actor: activity.ActorAssistant, Trigger: activity.TriggerBatch,
			BizKey: "doc:" + d.ID, Attrs: map[string]any{"doc_id": d.ID, "title": d.Title},
		})
	}

	var mems []struct {
		ID        string
		Topic     string
		CreatedAt time.Time
		UpdatedAt time.Time
	}
	if err := db.Raw(`SELECT id, COALESCE(topic,'') AS topic, created_at, updated_at FROM user_memories
		WHERE (created_at >= ? AND created_at < ?) OR (updated_at >= ? AND updated_at < ?)`,
		dayStart, dayEnd, dayStart, dayEnd).Scan(&mems).Error; err != nil {
		return nil, fmt.Errorf("query user_memories: %w", err)
	}
	for _, m := range mems {
		if !m.CreatedAt.Before(dayStart) && m.CreatedAt.Before(dayEnd) {
			out = append(out, activity.Event{
				Type: activity.TypeMemoryEntityChanged, EventID: detID("mem-c", m.ID),
				OccurredAt: m.CreatedAt, Actor: activity.ActorAssistant, Trigger: activity.TriggerBatch,
				Attrs: map[string]any{"memory_id": m.ID, "op": "create", "topic": m.Topic},
			})
		}
		if !m.UpdatedAt.Before(dayStart) && m.UpdatedAt.Before(dayEnd) && m.UpdatedAt.After(m.CreatedAt) {
			out = append(out, activity.Event{
				Type: activity.TypeMemoryEntityChanged, EventID: detID("mem-u", m.ID),
				OccurredAt: m.UpdatedAt, Actor: activity.ActorAssistant, Trigger: activity.TriggerBatch,
				Attrs: map[string]any{"memory_id": m.ID, "op": "update", "topic": m.Topic},
			})
		}
	}
	return out, nil
}

// turn 是一轮对话的重建骨架。userMsgEventID 是该轮 user 消息（根点事件）的 event_id。
type turn struct {
	id             string
	conv           string
	userMsgID      string
	userMsgEventID string
	started        activity.Event
	ended          activity.Event
	messages       []msgRow
}

// buildTurns：一个 conversation 内遇到 user 消息就开新 turn；直到下一条 user 或 conv 结束才收边。
func buildTurns(msgs []msgRow) []*turn {
	var turns []*turn
	byConv := map[string][]msgRow{}
	var convOrder []string
	for _, m := range msgs {
		if _, ok := byConv[m.ConversationID]; !ok {
			convOrder = append(convOrder, m.ConversationID)
		}
		byConv[m.ConversationID] = append(byConv[m.ConversationID], m)
	}
	for _, conv := range convOrder {
		var cur *turn
		for _, m := range byConv[conv] {
			if m.Role == "user" || cur == nil {
				if cur != nil {
					finalizeTurn(cur)
					turns = append(turns, cur)
				}
				cur = newTurn(conv, m)
			}
			cur.messages = append(cur.messages, m)
		}
		if cur != nil {
			finalizeTurn(cur)
			turns = append(turns, cur)
		}
	}
	sort.Slice(turns, func(i, j int) bool { return turns[i].started.OccurredAt.Before(turns[j].started.OccurredAt) })
	return turns
}

// newTurn 以该轮第一条消息（应为 user）为锚；started 互指 ended（pair_id = 对端 event_id）。
func newTurn(conv string, first msgRow) *turn {
	tid := "turn:" + first.ID
	startID, endID := detID("turn-s", first.ID), detID("turn-e", first.ID)
	return &turn{
		id:             tid,
		conv:           conv,
		userMsgID:      first.ID,
		userMsgEventID: detID("msg", first.ID),
		started: activity.Event{
			Type: activity.TypeChatTurnStarted, EventID: startID,
			OccurredAt: first.CreatedAt, Actor: activity.ActorAssistant, Trigger: activity.TriggerManual,
			ConversationID: conv, TurnID: tid, BizKey: tid, ParentEventID: detID("msg", first.ID), PairID: endID,
		},
		ended: activity.Event{
			Type: activity.TypeChatTurnEnded, EventID: endID,
			Actor: activity.ActorAssistant, Trigger: activity.TriggerManual,
			ConversationID: conv, TurnID: tid, BizKey: tid, ParentEventID: detID("msg", first.ID), PairID: startID,
			Outcome: activity.OutcomeCompleted,
		},
	}
}

// finalizeTurn 用最后一条消息时刻收边（缺则 fallback 由 P13 闭合兜住）。
func finalizeTurn(t *turn) {
	if len(t.messages) == 0 {
		return
	}
	last := t.messages[len(t.messages)-1]
	t.ended.OccurredAt = last.CreatedAt
}

// toolCallEvents 从 assistant 消息的 tool_calls_json 重建 tool.call.started/ended。
// ended 时刻优先取紧随其后、call_id 匹配的 role=tool 结果消息的 created_at；
// 找不到匹配结果消息时用 started 时刻（时长 0，诚实反映「只有调用、没抓到结果时刻」）。
func toolCallEvents(m msgRow, t *turn) []activity.Event {
	if m.ToolCallsJSON == nil || *m.ToolCallsJSON == "" {
		return nil
	}
	var calls []struct {
		ID       string `json:"id"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(*m.ToolCallsJSON), &calls); err != nil {
		log.Printf("[replay] 跳过无法解析的 tool_calls_json (msg=%s): %v", m.ID, err)
		return nil
	}
	results := toolResultTimes(t, m.CreatedAt)
	var out []activity.Event
	for _, c := range calls {
		if c.ID == "" {
			continue
		}
		startID, endID := detID("tool-s", c.ID), detID("tool-e", c.ID)
		endAt, ok := results[c.ID]
		if !ok {
			endAt = m.CreatedAt
		}
		out = append(out,
			activity.Event{
				Type: activity.TypeToolCallStarted, EventID: startID,
				OccurredAt: m.CreatedAt, Actor: activity.ActorAssistant, Trigger: activity.TriggerManual,
				ConversationID: t.conv, TurnID: t.id, ParentEventID: t.started.EventID,
				CallID: c.ID, BizKey: c.ID, PairID: endID, Attrs: map[string]any{"tool": c.Function.Name},
			},
			activity.Event{
				Type: activity.TypeToolCallEnded, EventID: endID,
				OccurredAt: endAt, Actor: activity.ActorAssistant, Trigger: activity.TriggerManual,
				ConversationID: t.conv, TurnID: t.id, ParentEventID: t.started.EventID,
				CallID: c.ID, BizKey: c.ID, PairID: startID, Outcome: activity.OutcomeCompleted,
			},
		)
	}
	return out
}

// toolResultTimes 收该 turn 内、时间 ≥ after 的 role=tool 结果消息，按 tool_call_id 建「call id → 结果时刻」表。
func toolResultTimes(t *turn, after time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for _, m := range t.messages {
		if m.Role != "tool" || m.CreatedAt.Before(after) || m.ToolCallID == nil || *m.ToolCallID == "" {
			continue
		}
		out[*m.ToolCallID] = m.CreatedAt
	}
	return out
}

func indexTurnsByConv(turns []*turn) map[string][]*turn {
	idx := map[string][]*turn{}
	for _, t := range turns {
		idx[t.conv] = append(idx[t.conv], t)
	}
	return idx
}

// nearestTurn 找 conv 下 started ≤ t 的最近一个 turn；无则空。
func nearestTurn(turns []*turn, t time.Time) (turnID, parentID string) {
	var best *turn
	for _, tt := range turns {
		if !tt.started.OccurredAt.After(t) {
			best = tt
		}
	}
	if best == nil {
		return "", ""
	}
	return best.id, best.started.EventID
}

func outcomeFor(errMsg *string) string {
	if errMsg != nil && *errMsg != "" {
		return activity.OutcomeFailed
	}
	return activity.OutcomeCompleted
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// detID 由主库主键派生确定性 event_id：同一份主库数据重放两次得到同一批 id，
// 事件表 INSERT OR IGNORE 幂等，重复回放不产生重复。
func detID(kind, key string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("replay:"+kind+":"+key)).String()
}

// defaultMainDB 返回本机 prod 主库路径；取不到 home 时报错而非返回空串（空串会误导成「表不存在」）。
func defaultMainDB() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录: %w", err)
	}
	return filepath.Join(home, "Library", "Application Support", "xhanHelper", "xhan_helper.db"), nil
}
