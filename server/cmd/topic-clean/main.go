// topic-clean — 记忆/笔记 topic 存量清洗工具。
//
// 两步，默认只读：
//
//	go run ./cmd/topic-clean                        # 产出报告 report-*.md + 映射 plan-*.tsv
//	（人工审阅 report、编辑 plan，把要改的行 action 改成 rename）
//	go run ./cmd/topic-clean --apply                # dry-run，打印将改动
//	go run ./cmd/topic-clean --apply --dry-run=false  # 真正落库
//
// 清洗规则见 internal/topicclean。落库前置：先 mysqldump 备份 + 停服（避免内存向量库不一致）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/llm"
	"xhan-helper/internal/topicclean"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	user := flag.String("user", "", "只处理指定 user_id（支持前缀，默认全部）")
	out := flag.String("out", "data/topic-clean", "报告 / 映射文件输出目录")
	apply := flag.Bool("apply", false, "读取映射文件执行改名")
	dryRun := flag.Bool("dry-run", true, "apply 时只打印不落库")
	force := flag.Bool("force", false, "生成时覆盖已存在的映射文件")
	noLLM := flag.Bool("no-llm", false, "不调 LLM 起名（candidate 留空，全部交人工）")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `topic-clean — 记忆/笔记 topic 存量清洗（空 / 超 5 字 / 含标点空格）

用法:
  topic-clean [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  topic-clean                                    # 生成报告 + plan-*.tsv
  topic-clean --user <user-id>                    # 只处理某用户
  topic-clean --apply                            # dry-run，打印将改动
  topic-clean --apply --dry-run=false            # 落库
`)
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db := app.InitDB(cfg)

	// 破坏性护栏：真正落库（apply 且非 dry-run）时回显 DB 路径，prod 库需 CONFIRM_PROD=yes。
	if *apply && !*dryRun {
		config.GuardProdWrite(cfg.DB.Database)
	}

	opts := topicclean.Options{
		UserID: *user,
		OutDir: *out,
		Force:  *force,
		DryRun: *dryRun,
	}
	if !*noLLM {
		client, err := llm.NewClient(cfg.LLM)
		if err != nil {
			log.Fatalf("create llm client: %v", err)
		}
		opts.LLM = client
	}

	ctx := context.Background()
	if !*apply {
		if err := topicclean.Generate(ctx, db, opts); err != nil {
			log.Fatalf("generate: %v", err)
		}
		return
	}

	sum, err := topicclean.Apply(ctx, db, opts)
	if err != nil {
		log.Fatalf("apply: %v", err)
	}
	mode := "dry-run"
	if !*dryRun {
		mode = "已落库"
	}
	log.Printf("[topic-clean] %s 完成：映射文件 %d，notes 改名 %d，memories 改名 %d",
		mode, sum.Files, sum.NotesRenamed, sum.MemoriesRenamed)
}
