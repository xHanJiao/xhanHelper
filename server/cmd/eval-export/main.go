package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"xhan-helper/internal/config"
	"xhan-helper/internal/eval"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	project := flag.String("project", "retrieval-baseline", "project name")
	out := flag.String("out", "", "output path for eval.db (default: data/eval/<project>/eval.db)")
	queryExporter := flag.String("query-exporter", "raw", "query exporter (available: raw)")
	corpusExporter := flag.String("corpus-exporter", "full-content", "corpus exporter (available: full-content)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `eval-export — 从 MySQL 导出检索评测数据集到 SQLite

用法:
  eval-export --project <name> [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  eval-export --project baseline
  eval-export --project baseline --query-exporter raw --corpus-exporter full-content
  eval-export --project my-test --out /tmp/my-eval.db
`)
	}

	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if cfg.DB.Driver != "mysql" {
		log.Fatalf("source db must be mysql, got %q", cfg.DB.Driver)
	}

	outPath := *out
	if outPath == "" {
		projRoot := filepath.Join("data", "eval", *project)
		outPath = filepath.Join(projRoot, "eval.db")
	}

	var q eval.QueryExporter
	switch *queryExporter {
	case "raw":
		q = &eval.RawQueryExporter{}
	default:
		log.Fatalf("unknown query-exporter: %q (available: raw)", *queryExporter)
	}

	var c eval.CorpusExporter
	switch *corpusExporter {
	case "full-content":
		c = &eval.FullContentCorpusExporter{}
	default:
		log.Fatalf("unknown corpus-exporter: %q (available: full-content)", *corpusExporter)
	}

	if err := eval.LoadDataset(cfg, q, c, *project, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
