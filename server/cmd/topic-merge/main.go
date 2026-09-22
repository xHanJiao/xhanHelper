// topic-merge — topic 存量收敛工具。
//
// 两步，默认只读：
//
//	go run ./cmd/topic-merge                        # 产出报告 report-*.md + 映射 plan-*.tsv
//	（人工审阅 report、编辑 plan）
//	go run ./cmd/topic-merge --apply                # dry-run，打印将改动
//	go run ./cmd/topic-merge --apply --dry-run=false  # 真正落库
//
// 合并规则见 internal/topicmerge。执行前置：先 mysqldump 备份 + 停服（避免内存向量库不一致）。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/topicmerge"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	threshold := flag.Float64("threshold", 0.80, "leader 聚类相似度阈值")
	user := flag.String("user", "", "只处理指定 user_id（支持前缀，默认全部）")
	out := flag.String("out", "data/topic-merge", "报告 / 映射文件输出目录")
	apply := flag.Bool("apply", false, "读取映射文件执行合并")
	dryRun := flag.Bool("dry-run", true, "apply 时只打印不落库")
	force := flag.Bool("force", false, "生成时覆盖已存在的映射文件")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `topic-merge — topic 存量收敛（同义 topic 合并）

用法:
  topic-merge [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  topic-merge                                    # 生成报告 + plan-*.tsv
  topic-merge --user <user-id>                    # 只处理某用户
  topic-merge --apply                            # dry-run，打印将改动
  topic-merge --apply --dry-run=false            # 落库
`)
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db := app.InitDB(cfg)
	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)

	opts := topicmerge.Options{
		Threshold: *threshold,
		UserID:    *user,
		OutDir:    *out,
		Force:     *force,
		DryRun:    *dryRun,
		Embedder:  embedder,
	}

	if !*apply {
		if err := topicmerge.Generate(db, opts); err != nil {
			log.Fatalf("generate: %v", err)
		}
		return
	}

	sum, err := topicmerge.Apply(db, opts)
	if err != nil {
		log.Fatalf("apply: %v", err)
	}
	mode := "dry-run"
	if !*dryRun {
		mode = "已落库"
	}
	log.Printf("[topic-merge] %s 完成：映射文件 %d，notes 改名 %d，memories 改名 %d，删概念 %d，删孤儿 %d",
		mode, sum.Files, sum.NotesRenamed, sum.MemoriesRenamed, sum.ConceptsDeleted, sum.OrphansDeleted)
}
