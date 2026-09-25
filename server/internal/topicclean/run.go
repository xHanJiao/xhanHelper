// Package topicclean 提供 topic 存量清洗工具：把空 / 超 5 字 / 含标点空格的 topic 收敛到合规短名。
//
// 与 topicmerge 同范式，分两步：
//   - Generate 只读：扫出违规 topic、调 LLM 给候选短名，产出报告 + 可编辑的映射文件；
//   - Apply 读取人工确认后的映射执行改名（默认 dry-run，落库时停服/起服由脚本负责）。
//
// 只处理 notes / memories 上的 topic。不动向量：topic 不在持久化向量 metadata 里，
// 服务重启时自动重载（见 docs/design/memory-note-rework/topic-5char.md）。
package topicclean

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"xhan-helper/internal/util"

	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/service"
)

// MaxTopicRunes 合规 topic 的字数上限（与 agent.memoryTopicMaxLen 对齐）。
const MaxTopicRunes = 5

// Options — 工具参数。
type Options struct {
	UserID string        // 空 = 全部用户；否则按前缀匹配单个用户
	OutDir string        // 报告 / 映射文件目录
	Force  bool          // Generate 时覆盖已存在的映射文件
	DryRun bool          // Apply 时只打印不落库
	LLM    llm.LLMClient // 可空；为违规 topic 提议短名
}

// ApplySummary — Apply 的汇总。
type ApplySummary struct {
	Files           int
	NotesRenamed    int64
	MemoriesRenamed int64
	DryRun          bool
}

// Generate 只读：为每个用户产出违规 topic 报告 + 可编辑的映射文件。
func Generate(ctx context.Context, db *gorm.DB, opts Options) error {
	memRepo := service.NewUserMemoryRepo(db)
	noteRepo := service.NewNoteRepo(db)

	users, err := topicUsers(ctx, memRepo, noteRepo, opts.UserID)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return fmt.Errorf("没有找到任何带 topic 的记忆/笔记")
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录 %s: %w", opts.OutDir, err)
	}

	for _, uid := range users {
		topics, err := loadUserTopics(ctx, memRepo, noteRepo, uid)
		if err != nil {
			return fmt.Errorf("user %s 载入 topic: %w", short(uid), err)
		}
		violations := findViolations(topics)
		// 空 topic 逐条处置（内容各异，不能按值聚合）：按内容逐条让 LLM 起名。
		empties, err := memRepo.EmptyTopics(ctx, uid, 100)
		if err != nil {
			return fmt.Errorf("user %s 载入空 topic: %w", short(uid), err)
		}
		if len(empties) > 0 {
			names := make([]string, len(empties))
			if opts.LLM != nil {
				if err := proposeEmptyNames(ctx, opts.LLM, uid, empties, topics, names); err != nil {
					log.Printf("[topic-clean] user %s 空 topic 起名失败: %v（留空交人工）", short(uid), err)
				}
			}
			for i, e := range empties {
				violations = append(violations, Violation{
					OldTopic: "", Runes: 0, Action: "rename", MemoryID: e.ID, Content: e.Content, NewTopic: names[i],
				})
			}
		}
		if len(violations) == 0 {
			log.Printf("[topic-clean] user %s 无违规 topic，跳过", short(uid))
			continue
		}
		// 非空违规 topic 批量给候选名（同用户一次调用）。
		if opts.LLM != nil {
			if err := proposeNames(ctx, opts.LLM, uid, violations, topics); err != nil {
				log.Printf("[topic-clean] user %s LLM 起名失败: %v（留空交人工）", short(uid), err)
			}
		}

		reportPath := filepath.Join(opts.OutDir, fmt.Sprintf("report-%s.md", short(uid)))
		planPath := filepath.Join(opts.OutDir, fmt.Sprintf("plan-%s.tsv", short(uid)))
		// report 与 plan 同步跳过（否则 report 被新 LLM 结果覆盖、plan 留旧稿，两者对不上）。
		if !opts.Force {
			if _, err := os.Stat(planPath); err == nil {
				log.Printf("[topic-clean] %s 已存在，跳过 report+plan（--force 覆盖）", planPath)
				continue
			}
		}
		if err := os.WriteFile(reportPath, []byte(renderReport(uid, topics, violations)), 0o644); err != nil {
			return fmt.Errorf("写报告 %s: %w", reportPath, err)
		}
		if err := os.WriteFile(planPath, []byte(renderPlan(uid, violations)), 0o644); err != nil {
			return fmt.Errorf("写映射 %s: %w", planPath, err)
		}
		log.Printf("[topic-clean] user %s：%d 个违规 topic，报告 %s", short(uid), len(violations), reportPath)
	}
	return nil
}

// Apply 读取 OutDir 下的 plan-*.tsv，执行改名。DryRun 时只打印（事务回滚）。
// opts.UserID 非空时只处理该用户对应的 plan-<short(uid)>.tsv，避免误改其他用户。
func Apply(ctx context.Context, db *gorm.DB, opts Options) (*ApplySummary, error) {
	files, err := filepath.Glob(filepath.Join(opts.OutDir, "plan-*.tsv"))
	if err != nil {
		return nil, fmt.Errorf("扫描 %s: %w", opts.OutDir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("在 %s 下没有 plan-*.tsv；请先不带 --apply 跑一次生成", opts.OutDir)
	}
	sort.Strings(files)

	// --user 过滤：把前缀解析成唯一 uid（与生成同一口径），只保留匹配的 plan 文件。
	if opts.UserID != "" {
		memRepo := service.NewUserMemoryRepo(db)
		noteRepo := service.NewNoteRepo(db)
		users, err := topicUsers(ctx, memRepo, noteRepo, opts.UserID)
		if err != nil {
			return nil, err
		}
		want := fmt.Sprintf("plan-%s.tsv", short(users[0]))
		filtered := files[:0:0]
		for _, f := range files {
			if filepath.Base(f) == want {
				filtered = append(filtered, f)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("--user=%s 对应的 %s 不存在；已忽略其余 %d 个 plan 文件", opts.UserID, want, len(files))
		}
		files = filtered
	}

	sum := &ApplySummary{DryRun: opts.DryRun}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("读 %s: %w", f, err)
		}
		uid, rows, err := parsePlan(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if len(rows) == 0 {
			log.Printf("[topic-clean] %s 无改名列，跳过", filepath.Base(f))
			continue
		}
		r, err := applyUser(ctx, db, uid, rows, opts.DryRun)
		if err != nil {
			return nil, fmt.Errorf("user %s 改名: %w", short(uid), err)
		}
		sum.Files++
		sum.NotesRenamed += r.notes
		sum.MemoriesRenamed += r.memories
	}
	return sum, nil
}

// errDryRun 在事务里回滚 dry-run 的写入，同时保留已累计的行数。
var errDryRun = errors.New("dry-run")

type userResult struct{ notes, memories int64 }

func applyUser(ctx context.Context, db *gorm.DB, userID string, rows []PlanRow, dryRun bool) (userResult, error) {
	var r userResult
	err := db.Transaction(func(tx *gorm.DB) error {
		noteRepo := service.NewNoteRepo(tx)
		memRepo := service.NewUserMemoryRepo(tx)

		for _, row := range rows {
			if row.Action != "rename" || row.NewTopic == "" {
				continue
			}
			if err := validateNewTopic(row.NewTopic); err != nil {
				return fmt.Errorf("新 topic %q 不合规: %w", row.NewTopic, err)
			}
			// 空 topic 行：按 memory_id 定位单条记忆（值匹配匹配不到 NULL）。
			if row.MemoryID != "" {
				m, err := memRepo.SetTopicByID(ctx, userID, row.MemoryID, row.NewTopic)
				if err != nil {
					return fmt.Errorf("填空 topic（id %s）→ %q: %w", row.MemoryID, row.NewTopic, err)
				}
				r.memories += m
				continue
			}
			n, err := noteRepo.RenameTopic(ctx, userID, []string{row.OldTopic}, row.NewTopic)
			if err != nil {
				return fmt.Errorf("改名 notes %q → %q: %w", row.OldTopic, row.NewTopic, err)
			}
			r.notes += n
			m, err := memRepo.RenameTopic(ctx, userID, []string{row.OldTopic}, row.NewTopic)
			if err != nil {
				return fmt.Errorf("改名 memories %q → %q: %w", row.OldTopic, row.NewTopic, err)
			}
			r.memories += m
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return userResult{}, err
	}
	return r, nil
}

func validateNewTopic(t string) error {
	t = strings.TrimSpace(t)
	if t == "" {
		return fmt.Errorf("不能为空")
	}
	if len([]rune(t)) > MaxTopicRunes {
		return fmt.Errorf("超过 %d 字", MaxTopicRunes)
	}
	if !util.IsValidTopicChars(t) {
		return fmt.Errorf("只能含中文/英文/数字（不含标点/空格/符号）")
	}
	return nil
}

// loadUserTopics 载入某用户 notes ∪ memories 的 topic 及引用数，按引用数降序。
func loadUserTopics(ctx context.Context, memRepo *service.UserMemoryRepo, noteRepo *service.NoteRepo, userID string) ([]topicRef, error) {
	agg := map[string]int64{}
	mems, err := memRepo.DistinctTopics(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("memory topics: %w", err)
	}
	for _, m := range mems {
		agg[m.Topic] += m.Count
	}
	notes, err := noteRepo.DistinctTopicsWithCount(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("note topics: %w", err)
	}
	for _, n := range notes {
		agg[n.Topic] += n.Count
	}
	out := make([]topicRef, 0, len(agg))
	for t, c := range agg {
		out = append(out, topicRef{Topic: t, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Topic < out[j].Topic
	})
	return out, nil
}

type topicRef struct {
	Topic string
	Count int64
}

// findViolations 挑出不合规的 topic：空 / 超 5 字 / 含标点或空格等非法字符。
// 与新写规则（util.IsValidTopicChars + MaxTopicRunes）口径一致。
func findViolations(topics []topicRef) []Violation {
	var out []Violation
	for _, t := range topics {
		r := len([]rune(t.Topic))
		if strings.TrimSpace(t.Topic) == "" || r > MaxTopicRunes || !util.IsValidTopicChars(t.Topic) {
			out = append(out, Violation{OldTopic: t.Topic, Runes: r, Action: "rename"})
		}
	}
	return out
}

// topicUsers 返回有 topic 的用户；only 非空时按前缀匹配唯一用户。
func topicUsers(ctx context.Context, memRepo *service.UserMemoryRepo, noteRepo *service.NoteRepo, only string) ([]string, error) {
	set := map[string]bool{}
	memIDs, err := memRepo.DistinctUserIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("memory distinct users: %w", err)
	}
	for _, u := range memIDs {
		set[u] = true
	}
	noteIDs, err := noteRepo.DistinctUserIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("note distinct users: %w", err)
	}
	for _, u := range noteIDs {
		set[u] = true
	}
	users := make([]string, 0, len(set))
	for u := range set {
		users = append(users, u)
	}
	sort.Strings(users)

	if only == "" {
		return users, nil
	}
	var match []string
	for _, u := range users {
		if strings.HasPrefix(u, only) {
			match = append(match, u)
		}
	}
	if len(match) == 0 {
		return nil, fmt.Errorf("没有匹配 %q 的用户", only)
	}
	if len(match) > 1 {
		return nil, fmt.Errorf("%q 匹配到多个用户: %v", only, match)
	}
	return match, nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
