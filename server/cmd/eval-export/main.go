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

	project := flag.String("project", "retrieval-baseline", "project name (used for eval_projects.id and default output path)")
	out := flag.String("out", "", "output path for eval.db (default: dataset/projects/<project>/eval.db)")
	strategy := flag.String("strategy", "all-past-data", "export strategy name")
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
		projRoot := filepath.Join("..", "dataset", "projects", *project)
		outPath = filepath.Join(projRoot, "eval.db")
	}

	var s eval.ExportStrategy
	switch *strategy {
	case "all-past-data":
		s = &eval.AllPastDataStrategy{}
	default:
		log.Fatalf("unknown strategy: %q (available: all-past-data)", *strategy)
	}

	if err := eval.LoadDataset(cfg, s, *project, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
