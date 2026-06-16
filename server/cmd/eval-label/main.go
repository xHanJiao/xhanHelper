package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"xhan-helper/internal/config"
	"xhan-helper/internal/eval"
	"xhan-helper/internal/llm"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	project := flag.String("project", "", "project name (required)")
	session := flag.String("session", "batch-1", "annotation session ID")
	queryLimit := flag.Int("query-limit", 10, "max queries to select this batch")
	candidateLimit := flag.Int("candidate-limit", 20, "top K corpus candidates for pre-labeling")
	retrySession := flag.Bool("retry-session", false, "re-run even if session exists")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `eval-label — CLI 批量标注工具（随机选 query + cosine 预筛 + LLM 五档评分）

用法:
  eval-label --project <name> [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  eval-label --project baseline --session batch-1 --query-limit 10
  eval-label --project baseline --session batch-2 --query-limit 50
  eval-label --project baseline --session batch-1 --retry-session
`)
	}

	flag.Parse()

	if *project == "" {
		fmt.Fprintln(os.Stderr, "Error: --project is required")
		flag.Usage()
		os.Exit(1)
	}

	dbPath := filepath.Join("data", "eval", *project, "eval.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Fatalf("eval.db not found at %s (run eval-export first)", dbPath)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	llmClient, err := llm.NewClient(cfg.LLM)
	if err != nil {
		log.Fatalf("create llm client: %v", err)
	}

	strategy := &eval.DefaultLabelStrategy{}

	embedder := llm.NewEmbedder(cfg.Embedding.APIKey, cfg.Embedding.BaseURL, cfg.Embedding.Model)

	if err := eval.RunLabel(dbPath, *session, *project, *queryLimit, *candidateLimit, *retrySession, strategy, llmClient, embedder); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
