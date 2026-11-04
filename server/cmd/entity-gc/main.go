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
	purgeCancelled := flag.Bool("purge-cancelled", false, "硬删所有取消态笔记及其派生数据（chunk/向量/版本）")
	purgeLegacyThemes := flag.Bool("purge-legacy-themes", false, "清理旧主题系统残留（theme_states / 向量 / chunk / concept）")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `entity-gc — 清理孤儿派生数据 / 硬删取消态笔记 / 清理旧主题残留

用法:
  entity-gc [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  entity-gc                        # dry-run，只统计孤儿
  entity-gc --write                # 落库清理孤儿
  entity-gc --purge-cancelled      # dry-run，统计待硬删的取消态笔记
  entity-gc --purge-cancelled --write   # 硬删取消态笔记 + 派生数据
  entity-gc --purge-legacy-themes       # dry-run，统计旧主题残留
  entity-gc --purge-legacy-themes --write   # 清理旧主题残留
`)
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	db := app.InitDB(cfg)

	// 破坏性护栏：--write 时回显解析后的 DB 路径，prod 库需显式 CONFIRM_PROD=yes。
	if *write {
		config.GuardProdWrite(cfg.DB.Database)
	}

	if *purgeLegacyThemes {
		sum, err := entitygc.PurgeLegacyThemes(db, entitygc.Options{Write: *write})
		if err != nil {
			log.Fatalf("entity-gc purge-legacy-themes: %v", err)
		}
		mode := "dry-run（未落库）"
		if *write {
			mode = "已落库"
		}
		log.Printf("[entity-gc] %s 完成：旧主题 theme_states %d，全文向量 %d，chunk 向量 %d，chunk 行 %d，concept %d",
			mode, sum.Themes, sum.ThemeVectors, sum.ChunkVectors, sum.ChunkRows, sum.Concepts)
		return
	}

	if *purgeCancelled {
		n, err := entitygc.PurgeCancelledNotes(db, entitygc.Options{Write: *write})
		if err != nil {
			log.Fatalf("entity-gc purge-cancelled: %v", err)
		}
		mode := "dry-run（未落库）"
		if *write {
			mode = "已落库"
		}
		log.Printf("[entity-gc] %s 完成：取消态笔记 %d 条", mode, n)
		return
	}

	sum, err := entitygc.Run(db, entitygc.Options{Write: *write})
	if err != nil {
		log.Fatalf("entity-gc: %v", err)
	}
	mode := "dry-run（未落库）"
	if *write {
		mode = "已落库"
	}
	log.Printf("[entity-gc] %s 完成：软删笔记 %d，笔记孤儿向量 %d，记忆孤儿向量 %d，笔记孤儿版本快照 %d",
		mode, sum.SoftDeletedNotes, sum.NoteEmbeddings, sum.MemoryEmbeddings, sum.NoteVersions)
}
