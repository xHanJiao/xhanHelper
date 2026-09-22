// entity-gc — 清理历史遗留的孤儿派生数据。
//
// bug 017 之前的删除路径只做 SoftDelete，不清理派生数据，于是留下孤儿：
// 笔记的全文向量行 + 版本快照、记忆的全文向量行。本工具一次性清扫，幂等。
//
// 用法:
//
//	APP_ENV=prod ./entity-gc            # dry-run，只统计
//	APP_ENV=prod ./entity-gc --write    # 落库
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/entitygc"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	write := flag.Bool("write", false, "真正删除（默认只统计）")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `entity-gc — 清理孤儿派生数据（note / memory）

用法:
  entity-gc [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  entity-gc            # dry-run，只统计
  entity-gc --write    # 落库
`)
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db := app.InitDB(cfg)
	sum, err := entitygc.Run(db, entitygc.Options{Write: *write})
	if err != nil {
		log.Fatalf("entity-gc: %v", err)
	}
	mode := "dry-run"
	if *write {
		mode = "已落库"
	}
	log.Printf("[entity-gc] %s 完成：软删笔记 %d，笔记孤儿向量 %d，记忆孤儿向量 %d，笔记孤儿版本快照 %d",
		mode, sum.SoftDeletedNotes, sum.NoteEmbeddings, sum.MemoryEmbeddings, sum.NoteVersions)
}
