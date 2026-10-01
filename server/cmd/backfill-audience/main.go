// backfill-audience — 存量文档 audience 一次性回标。
//
// 机制见 docs/design/document-audience/design.md。存量兜底方向是「捞回来」：
//   - tags 命中底稿类 → internal（默认不呈现）；
//   - 其余一律 → finished（含没打标签的方案稿/阶段汇总/交接/SOP）。
//
// 幂等：只改 audience，不碰正文；重复跑结果一致。
//
// 用法:
//
//	APP_ENV=prod ./backfill-audience            # dry-run，只统计并打印清单
//	APP_ENV=prod ./backfill-audience --write    # 落库（prod 库需 CONFIRM_PROD=yes）
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
)

// draftTags 判为材料（internal）的标签；其余一律成品。
var draftTags = map[string]bool{
	"陪伴AI调研底稿": true,
	"记忆层调研底稿":  true,
	"底稿":       true,
}

type docRow struct {
	ID       string `gorm:"column:id"`
	Title    string `gorm:"column:title"`
	Tags     string `gorm:"column:tags"`
	Audience string `gorm:"column:audience"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	write := flag.Bool("write", false, "真正落库（默认只统计、打印清单）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `backfill-audience — 存量文档 audience 回标

用法:
  backfill-audience [flags]

Flags:
`)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
示例:
  backfill-audience            # dry-run，打印将判为 internal 的清单
  backfill-audience --write    # 落库
`)
	}
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	db := app.InitDB(cfg)

	if *write {
		config.GuardProdWrite(cfg.DB.Database)
	}

	var rows []docRow
	if err := db.Model(&model.Document{}).Select("id, title, tags, audience").Find(&rows).Error; err != nil {
		log.Fatalf("list documents: %v", err)
	}

	var internalIDs, finishedIDs []string
	byID := make(map[string]docRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
		if isDraft(r.Tags) {
			if r.Audience != model.DocumentAudienceInternal {
				internalIDs = append(internalIDs, r.ID)
			}
		} else if r.Audience != model.DocumentAudienceFinished {
			finishedIDs = append(finishedIDs, r.ID)
		}
	}

	// 复核清单：两张都要打。只打 internal 会看不见「要放开谁」——重跑时误伤正是藏在这里。
	log.Printf("[backfill-audience] 共 %d 篇；将置 internal %d 篇、finished %d 篇", len(rows), len(internalIDs), len(finishedIDs))
	fmt.Println("—— 将置 internal（默认不呈现）的文档 ——")
	for _, id := range internalIDs {
		r := byID[id]
		fmt.Printf("  internal  %s  [%s]  %s\n", r.ID, r.Tags, r.Title)
	}
	fmt.Println("—— 将置 finished（放进用户列表）的文档 ——")
	for _, id := range finishedIDs {
		r := byID[id]
		fmt.Printf("  finished  %s  audience=%s  [%s]  %s\n", r.ID, r.Audience, r.Tags, r.Title)
	}

	if !*write {
		log.Printf("[backfill-audience] dry-run（未落库）；确认无误后加 --write")
		return
	}

	// 一次性迁移：本命令是 feature 上线时的存量回标。重跑会把后来按 internal 写入、
	// 但 tags 不含底稿词的材料一并翻成 finished（上面「将置 finished」那张清单就是它）。
	log.Printf("[backfill-audience] ⚠️ 一次性迁移：仅应在 audience 上线首次执行。将把 %d 篇非底稿的 internal 文档放开到 finished，确认这是你要的。", len(finishedIDs))

	if len(internalIDs) > 0 {
		if err := db.Model(&model.Document{}).Where("id IN ?", internalIDs).
			Update("audience", model.DocumentAudienceInternal).Error; err != nil {
			log.Fatalf("set internal: %v", err)
		}
	}
	if len(finishedIDs) > 0 {
		if err := db.Model(&model.Document{}).Where("id IN ?", finishedIDs).
			Update("audience", model.DocumentAudienceFinished).Error; err != nil {
			log.Fatalf("set finished: %v", err)
		}
	}
	log.Printf("[backfill-audience] 已落库：internal %d 篇、finished %d 篇", len(internalIDs), len(finishedIDs))
}

// isDraft 判断标签串里是否含任一底稿标签（按半角/全角逗号切分、去空格、精确匹配）。
func isDraft(tags string) bool {
	for _, t := range strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == '，' }) {
		if draftTags[strings.TrimSpace(t)] {
			return true
		}
	}
	return false
}
