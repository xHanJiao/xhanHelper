package topicmerge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"

	"xhan-helper/internal/llm"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// Options — 工具参数。
type Options struct {
	Threshold float64
	UserID    string        // 空 = 全部用户；否则按前缀匹配单个用户
	OutDir    string        // 报告 / 映射文件目录
	Force     bool          // Generate 时覆盖已存在的映射文件
	DryRun    bool          // Apply 时只打印不落库
	Embedder  *llm.Embedder // 可空；为缺向量的 topic 兜底嵌入
}

// ApplySummary — Apply 的汇总。
type ApplySummary struct {
	Files           int
	NotesRenamed    int64
	MemoriesRenamed int64
	ConceptsDeleted int64
	OrphansDeleted  int64
	DryRun          bool
}

// Generate 只读：为每个用户产出报告 + 可编辑的映射文件。
func Generate(db *gorm.DB, opts Options) error {
	ctx := context.Background()
	concepts := service.NewConceptEmbeddingRepo(db)
	notes := service.NewNoteRepo(db)
	mems := service.NewUserMemoryRepo(db)
	embs := service.NewEmbeddingRepo(db)

	users, err := topicUsers(ctx, concepts, opts.UserID)
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return fmt.Errorf("没有找到任何 topic 概念")
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录 %s: %w", opts.OutDir, err)
	}

	for _, uid := range users {
		topics, noVector, err := loadTopics(ctx, concepts, embs, notes, mems, opts.Embedder, uid)
		if err != nil {
			return fmt.Errorf("user %s 载入 topic: %w", short(uid), err)
		}
		if len(topics) == 0 {
			log.Printf("[topic-merge] user %s 无可聚类 topic，跳过", short(uid))
			continue
		}

		clusters := LeaderCluster(topics, opts.Threshold)
		sensitivity := make([]ThresholdStat, 0, 3)
		for _, th := range []float64{0.60, 0.70, 0.80} {
			sensitivity = append(sensitivity, StatOf(LeaderCluster(topics, th), th))
		}

		reportPath := filepath.Join(opts.OutDir, fmt.Sprintf("report-%s.md", short(uid)))
		planPath := filepath.Join(opts.OutDir, fmt.Sprintf("plan-%s.tsv", short(uid)))
		if err := os.WriteFile(reportPath, []byte(RenderReport(uid, opts.Threshold, clusters, sensitivity, noVector)), 0o644); err != nil {
			return fmt.Errorf("写报告 %s: %w", reportPath, err)
		}
		if !opts.Force {
			if _, err := os.Stat(planPath); err == nil {
				log.Printf("[topic-merge] %s 已存在，跳过写映射（--force 覆盖）", planPath)
				continue
			}
		}
		if err := os.WriteFile(planPath, []byte(RenderPlan(uid, BuildPlanRows(clusters))), 0o644); err != nil {
			return fmt.Errorf("写映射 %s: %w", planPath, err)
		}
		log.Printf("[topic-merge] user %s：%d 个 topic → %d 簇（多成员 %d，改名 %d），报告 %s",
			short(uid), len(topics), len(clusters), MultiMemberCount(Actionable(clusters)), MergedTopicCount(Actionable(clusters)), reportPath)
	}
	return nil
}

// Apply 读取 OutDir 下的 plan-*.tsv，执行合并，随后清理孤儿 topic 概念。
// DryRun 时只打印将改动，不落库（事务回滚）。
func Apply(db *gorm.DB, opts Options) (*ApplySummary, error) {
	ctx := context.Background()
	files, err := filepath.Glob(filepath.Join(opts.OutDir, "plan-*.tsv"))
	if err != nil {
		return nil, fmt.Errorf("扫描 %s: %w", opts.OutDir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("在 %s 下没有 plan-*.tsv；请先不带 --apply 跑一次生成", opts.OutDir)
	}
	sort.Strings(files)

	concepts := service.NewConceptEmbeddingRepo(db)
	users, err := topicUsers(ctx, concepts, opts.UserID)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(users))
	for _, u := range users {
		allowed[u] = true
	}

	sum := &ApplySummary{DryRun: opts.DryRun}
	planned := map[string]bool{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("读 %s: %w", f, err)
		}
		uid, merges, err := ParsePlan(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if !allowed[uid] {
			log.Printf("[topic-merge] %s 不属于当前 --user 范围，跳过", filepath.Base(f))
			continue
		}
		if len(merges) == 0 {
			log.Printf("[topic-merge] %s 无合并行，跳过", filepath.Base(f))
			continue
		}
		planned[uid] = true
		r, err := applyUser(ctx, db, uid, merges, opts.DryRun)
		if err != nil {
			return nil, fmt.Errorf("user %s 合并: %w", short(uid), err)
		}
		sum.Files++
		sum.NotesRenamed += r.notes
		sum.MemoriesRenamed += r.memories
		sum.ConceptsDeleted += r.concepts
		sum.OrphansDeleted += r.orphans
	}

	// 没有映射文件的用户，也顺带清一次孤儿概念。
	for _, uid := range users {
		if planned[uid] {
			continue
		}
		o, err := gcUser(ctx, db, uid, opts.DryRun)
		if err != nil {
			return nil, fmt.Errorf("user %s 孤儿 GC: %w", short(uid), err)
		}
		sum.OrphansDeleted += o
	}
	return sum, nil
}

// errDryRun 在事务里回滚 dry-run 的写入，同时保留已累计的行数。
var errDryRun = errors.New("dry-run")

type userResult struct{ notes, memories, concepts, orphans int64 }

func applyUser(ctx context.Context, db *gorm.DB, userID string, merges []Merge, dryRun bool) (userResult, error) {
	var r userResult
	err := db.Transaction(func(tx *gorm.DB) error {
		noteRepo := service.NewNoteRepo(tx)
		memRepo := service.NewUserMemoryRepo(tx)
		conceptRepo := service.NewConceptEmbeddingRepo(tx)
		embRepo := service.NewEmbeddingRepo(tx)

		byTarget := map[string][]string{}
		members := make([]string, 0, len(merges))
		for _, m := range merges {
			byTarget[m.To] = append(byTarget[m.To], m.From)
			members = append(members, m.From)
		}
		sort.Strings(members)
		targets := make([]string, 0, len(byTarget))
		for to := range byTarget {
			targets = append(targets, to)
		}
		sort.Strings(targets)

		for _, to := range targets {
			froms := byTarget[to]
			n, err := noteRepo.RenameTopic(ctx, userID, froms, to)
			if err != nil {
				return fmt.Errorf("改名 notes → %q: %w", to, err)
			}
			r.notes += n
			m, err := memRepo.RenameTopic(ctx, userID, froms, to)
			if err != nil {
				return fmt.Errorf("改名 memories → %q: %w", to, err)
			}
			r.memories += m
		}

		ids, err := conceptRepo.DeleteByValues(ctx, "topic", userID, members)
		if err != nil {
			return fmt.Errorf("删概念行: %w", err)
		}
		r.concepts = int64(len(ids))
		if _, err := embRepo.DeleteByEntities(ctx, model.EntityTypeConcept, ids); err != nil {
			return fmt.Errorf("删概念向量: %w", err)
		}

		o, err := gcTx(ctx, tx, userID, dryRun)
		if err != nil {
			return err
		}
		r.orphans = o

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

func gcUser(ctx context.Context, db *gorm.DB, userID string, dryRun bool) (int64, error) {
	var n int64
	err := db.Transaction(func(tx *gorm.DB) error {
		o, err := gcTx(ctx, tx, userID, dryRun)
		if err != nil {
			return err
		}
		n = o
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return 0, err
	}
	return n, nil
}

// gcTx 删除引用清零的 topic 概念行（含合并后腾空的旧概念与历史 0 引用行），
// 并同步删除对应 concept 向量。返回删除的概念行数。
func gcTx(ctx context.Context, tx *gorm.DB, userID string, dryRun bool) (int64, error) {
	conceptRepo := service.NewConceptEmbeddingRepo(tx)
	noteRepo := service.NewNoteRepo(tx)
	memRepo := service.NewUserMemoryRepo(tx)
	embRepo := service.NewEmbeddingRepo(tx)

	cs, err := conceptRepo.ListByType(ctx, "topic", userID)
	if err != nil {
		return 0, err
	}
	if len(cs) == 0 {
		return 0, nil
	}
	referenced := map[string]bool{}
	nc, err := noteRepo.DistinctTopicsWithCount(ctx, userID)
	if err != nil {
		return 0, err
	}
	for _, x := range nc {
		referenced[x.Topic] = true
	}
	mc, err := memRepo.DistinctTopics(ctx, userID)
	if err != nil {
		return 0, err
	}
	for _, x := range mc {
		referenced[x.Topic] = true
	}

	var orphans []string
	for _, c := range cs {
		if !referenced[c.Value] {
			orphans = append(orphans, c.Value)
		}
	}
	if len(orphans) == 0 {
		return 0, nil
	}
	sort.Strings(orphans)
	ids, err := conceptRepo.DeleteByValues(ctx, "topic", userID, orphans)
	if err != nil {
		return 0, err
	}
	if _, err := embRepo.DeleteByEntities(ctx, model.EntityTypeConcept, ids); err != nil {
		return 0, err
	}
	label := "删除"
	if dryRun {
		label = "(dry-run) 将删除"
	}
	log.Printf("[topic-merge] %s user %s %d 个孤儿 topic 概念", label, short(userID), len(ids))
	return int64(len(ids)), nil
}

// loadTopics 载入某用户的 topic 概念、向量与引用数。无向量的 topic 收集到 noVector。
func loadTopics(ctx context.Context, concepts *service.ConceptEmbeddingRepo, embs *service.EmbeddingRepo, notes *service.NoteRepo, mems *service.UserMemoryRepo, embedder *llm.Embedder, userID string) (topics []Topic, noVector []string, err error) {
	cs, err := concepts.ListByType(ctx, "topic", userID)
	if err != nil {
		return nil, nil, err
	}

	refs := map[string]int{}
	nc, err := notes.DistinctTopicsWithCount(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, x := range nc {
		refs[x.Topic] += int(x.Count)
	}
	mc, err := mems.DistinctTopics(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	for _, x := range mc {
		refs[x.Topic] += int(x.Count)
	}

	ids := make([]string, 0, len(cs))
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	embsList, err := embs.ListByEntities(ctx, model.EntityTypeConcept, ids)
	if err != nil {
		return nil, nil, err
	}
	vecByID := make(map[string][]float64, len(embsList))
	for _, e := range embsList {
		if e.Embedding == nil {
			continue
		}
		v, err := llm.StringToEmbedding(*e.Embedding)
		if err != nil {
			log.Printf("[topic-merge] 解析向量失败 concept=%s: %v", short(e.EntityID), err)
			continue
		}
		if len(v) > 0 {
			vecByID[e.EntityID] = v
		}
	}

	for _, c := range cs {
		vec := vecByID[c.ID]
		if len(vec) == 0 && embedder != nil {
			v, err := embedder.Embed(ctx, c.Value)
			if err != nil {
				log.Printf("[topic-merge] 兜底嵌入 %q 失败: %v", c.Value, err)
			} else {
				vec = v
			}
		}
		if len(vec) == 0 {
			noVector = append(noVector, c.Value)
			continue
		}
		topics = append(topics, Topic{Value: c.Value, Vector: vec, RefCount: refs[c.Value]})
	}
	sort.Strings(noVector)
	return topics, noVector, nil
}

// topicUsers 返回有 topic 概念的用户；only 非空时按前缀匹配唯一用户。
func topicUsers(ctx context.Context, concepts *service.ConceptEmbeddingRepo, only string) ([]string, error) {
	all, err := concepts.ListByTypeAll(ctx, "topic")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, c := range all {
		set[c.UserID] = true
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
