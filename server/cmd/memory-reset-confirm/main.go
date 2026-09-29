// memory-reset-confirm — 一次性迁移：把存量 user_memories.confirm_count 清零。
//
// 背景：confirm_count 旧口径是「检索候选排过名」（且在新鲜度过滤/limit 裁剪之前计数），
// 语义与「真的被注入给了模型」不符，且全系统无消费方。本次改为口径 B
// （被当轮新召回且进入最终注入上下文的次数，见 docs/design/memory-audit），
// 历史脏值统一清 0，从新链路重新累计。
//
// 用法（默认只读）：
//
//	go run ./cmd/memory-reset-confirm                          # dry-run：打印将清零的条数
//	go run ./cmd/memory-reset-confirm --apply                  # 落库（prod 库需 CONFIRM_PROD=yes）
//
// 只在本机执行；云端 MySQL 需另行处理。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"gorm.io/gorm"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	user := flag.String("user", "", "只处理指定 user_id（支持前缀，默认全部）")
	apply := flag.Bool("apply", false, "真正落库（默认只打印）")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `memory-reset-confirm — 存量 confirm_count 清零

用法:
  memory-reset-confirm [flags]

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// 本工具只面向本地 SQLite（prod 库或 dev 沙盒库），不做 MySQL。
	// 未设 APP_ENV 时会落到 base config.yaml（MySQL），大概率是误用——直接拦下。
	if cfg.DB.Driver != "sqlite" {
		log.Fatalf("本工具只支持 SQLite（driver=%s）。本地库请用 APP_ENV=local（prod）或 local.dev（沙盒）重跑", cfg.DB.Driver)
	}

	db := app.InitDB(cfg)
	ctx := context.Background()

	// 命中过滤：confirm_count != 0（可再加 user_id 前缀）
	where := func(tx *gorm.DB) *gorm.DB {
		q := tx.Where("confirm_count <> 0")
		if *user != "" {
			q = q.Where("user_id LIKE ?", *user+"%")
		}
		return q
	}

	var total, affected int64
	if err := where(db.WithContext(ctx).Model(&model.UserMemory{})).Count(&total).Error; err != nil {
		log.Fatalf("count: %v", err)
	}

	if !*apply {
		fmt.Printf("dry-run：将清零 %d 条 user_memories.confirm_count（db=%s）\n", total, cfg.DB.Database)
		fmt.Println("确认无误后加 --apply 落库")
		return
	}

	config.GuardProdWrite(cfg.DB.Database)

	res := where(db.WithContext(ctx).Model(&model.UserMemory{})).Update("confirm_count", 0)
	if res.Error != nil {
		log.Fatalf("update: %v", res.Error)
	}
	affected = res.RowsAffected
	log.Printf("[memory-reset-confirm] 已清零 %d 条（db=%s，user=%q）", affected, cfg.DB.Database, *user)
}
