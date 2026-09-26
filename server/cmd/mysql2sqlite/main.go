// mysql2sqlite 把线上 MySQL（本地副本）里 xhan 的数据搬进本地 prod 的 SQLite。
//
// 用法（在 server/ 下执行）：
//
//	go run ./cmd/mysql2sqlite              # 源 = APP_ENV=dev 的 MySQL，目标 = APP_ENV=local 的 SQLite
//	go run ./cmd/mysql2sqlite -from dev -to local
//
// 前提：`make db-pull` 已把最新 dump 导进本地 MySQL；app 必须已退出（工具会探测端口）。
// 全程只读源库；目标库先改名备份再重建（drop/recreate），搬完自动剪掉非 xhan 的行并逐表对账。
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"reflect"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"xhan-helper/internal/app"
	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

const (
	batchSize    = 50
	keepUsername = "xhan" // 只留这个用户的数据
)

var (
	fromEnv = flag.String("from", "dev", "源配置的 APP_ENV（本地 MySQL）")
	toEnv   = flag.String("to", "local", "目标配置的 APP_ENV（本地 SQLite）")
)

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)
	t0 := time.Now()

	// 工具不调 LLM，但 config.Load() 会校验 llm.api_key 非空 → 占位假值绕过
	mustSetenv("LLM_API_KEY", "unused-by-mysql2sqlite")
	mustSetenv("EMBEDDING_API_KEY", "unused-by-mysql2sqlite")

	srcCfg := loadConfig(*fromEnv)
	dstCfg := loadConfig(*toEnv)
	if dstCfg.DB.Driver != "sqlite" {
		log.Fatalf("[config] 目标 APP_ENV=%s 的 db.driver=%s，本工具只往 SQLite 搬", *toEnv, dstCfg.DB.Driver)
	}
	dstPath := dstCfg.DB.Database
	log.Printf("[migrate] 源   %s://%s:%d/%s", srcCfg.DB.Driver, srcCfg.DB.Host, srcCfg.DB.Port, srcCfg.DB.Database)
	log.Printf("[migrate] 目标 sqlite://%s", dstPath)

	requireAppStopped(dstCfg.Server.Port)
	backupExisting(dstPath)

	src := service.NewDB(srcCfg.DB) // 只读源库，不做迁移
	dst := app.InitDB(dstCfg)       // 建全新目标库（AutoMigrate 全部模型）

	keeper := findKeeper(src)
	log.Printf("[migrate] 保留用户：%s（%s）", keeper.Username, keeper.ID)

	if err := copyAll(src, dst); err != nil {
		log.Fatalf("[copy] %v", err)
	}
	prune(dst, keeper.ID)
	ok := reconcile(src, dst, keeper.ID)
	summarize(dst, dstCfg, keeper.ID, time.Since(t0))
	if !ok {
		log.Fatalf("[migrate] 对账存在不一致，见上面 ❌ 行")
	}
	log.Printf("[migrate] 完成，耗时 %s。下一步：重启 app（它会认领库里的 xhan，且不需要重新配置密钥）", time.Since(t0).Round(time.Second))
}

func mustSetenv(k, v string) {
	if err := os.Setenv(k, v); err != nil {
		log.Fatalf("[env] set %s: %v", k, err)
	}
}

func loadConfig(env string) *config.Config {
	mustSetenv("APP_ENV", env)
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[config] APP_ENV=%s: %v", env, err)
	}
	return cfg
}

// requireAppStopped 后端在跑时会一边写库一边被我们换掉 → 直接拒绝。
func requireAppStopped(port int) {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return
	}
	if err := conn.Close(); err != nil {
		log.Printf("[safety] 关闭探测连接: %v", err)
	}
	log.Fatalf("[safety] 127.0.0.1:%d 有监听（本地后端正在跑）→ 先退出 app 再迁移", port)
}

// backupExisting 旧库改名为备份（含 -wal/-shm 一并清理），出问题可人工退回。
func backupExisting(dbPath string) {
	ts := time.Now().Format("20060102-150405")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := dbPath + suffix
		info, err := os.Stat(p)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Fatalf("[backup] stat %s: %v", p, err)
			}
			continue
		}
		if suffix != "" { // -wal/-shm 属于旧库，主库改名后它们是孤儿
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				log.Fatalf("[backup] 删 %s: %v", p, err)
			}
			continue
		}
		bak := fmt.Sprintf("%s.pre-migrate-%s", p, ts)
		if err := os.Rename(p, bak); err != nil {
			log.Fatalf("[backup] 改名 %s → %s: %v", p, bak, err)
		}
		log.Printf("[backup] 旧库已备份：%s（%d KB）", bak, info.Size()/1024)
	}
}

func findKeeper(src *gorm.DB) model.User {
	var u model.User
	if err := src.Where("username = ?", keepUsername).First(&u).Error; err != nil {
		log.Fatalf("[migrate] 源库里读不到用户 %q：%v（先 make db-pull？）", keepUsername, err)
	}
	return u
}

// tableModel — 表名 → 模型类型（拷贝按 app.AllModels() 的顺序，先用户再内容）。
type tableModel struct {
	Name string
	Type reflect.Type
}

func modelTableList(db *gorm.DB) []tableModel {
	out := make([]tableModel, 0, len(app.AllModels()))
	for _, m := range app.AllModels() {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(m); err != nil {
			log.Fatalf("[copy] 解析模型 %T: %v", m, err)
		}
		out = append(out, tableModel{Name: stmt.Schema.Table, Type: stmt.Schema.ModelType})
	}
	return out
}

func copyAll(src, dst *gorm.DB) error {
	srcTables, err := src.Migrator().GetTables()
	if err != nil {
		return fmt.Errorf("列出源表: %w", err)
	}
	dstTables, err := dst.Migrator().GetTables()
	if err != nil {
		return fmt.Errorf("列出目标表: %w", err)
	}
	has := func(list []string, t string) bool {
		for _, x := range list {
			if x == t {
				return true
			}
		}
		return false
	}

	models := modelTableList(src)
	var total int64
	for _, m := range models {
		if !has(srcTables, m.Name) {
			log.Printf("[copy] 跳过 %-24s 源库没有这张表", m.Name)
			continue
		}
		if !has(dstTables, m.Name) {
			log.Printf("[copy] 跳过 %-24s 目标 schema 没有这张表", m.Name)
			continue
		}
		n, err := copyTable(src, dst, m)
		if err != nil {
			return fmt.Errorf("搬 %s: %w", m.Name, err)
		}
		total += n
		log.Printf("[copy] %-24s %7d 行", m.Name, n)
	}

	// 源库有、模型清单里没有的表 = 已下线功能的残留
	for _, t := range srcTables {
		known := false
		for _, m := range models {
			if m.Name == t {
				known = true
				break
			}
		}
		if known {
			continue
		}
		var n int64
		if err := src.Table(t).Count(&n).Error; err != nil {
			return fmt.Errorf("数 %s: %w", t, err)
		}
		log.Printf("[copy] 跳过 %-24s 模型清单外（已下线残留？）%d 行", t, n)
	}
	log.Printf("[copy] 合计 %d 行", total)
	return nil
}

// copyTable 按主键 keyset 分页读源、批量写目标。
// Omit(Associations) + SkipHooks：搬的是数据本身，不要 hook 与嵌套保存。
func copyTable(src, dst *gorm.DB, m tableModel) (int64, error) {
	dest := reflect.New(reflect.SliceOf(m.Type))
	var total int64
	err := src.Table(m.Name).FindInBatches(dest.Interface(), batchSize, func(tx *gorm.DB, batch int) error {
		n := tx.RowsAffected
		if n == 0 {
			return nil
		}
		if err := dst.Session(&gorm.Session{SkipHooks: true}).
			Omit(clause.Associations).
			Table(m.Name).
			CreateInBatches(dest.Interface(), batchSize).Error; err != nil {
			return fmt.Errorf("第 %d 批（%d 行）: %w", batch, n, err)
		}
		total += n
		return nil
	}).Error
	return total, err
}

// prune 剪掉非 xhan 的行：带 user_id 的表运行时发现（靠 PRAGMA，不手维护清单）。
func prune(dst *gorm.DB, keeperID string) {
	tables, err := dst.Migrator().GetTables()
	if err != nil {
		log.Fatalf("[prune] 列出目标表: %v", err)
	}
	var cut int64
	for _, t := range tables {
		cols, err := tableColumns(dst, t)
		if err != nil {
			log.Fatalf("[prune] %s: %v", t, err)
		}
		if !cols["user_id"] {
			continue
		}
		res := dst.Exec(fmt.Sprintf(`DELETE FROM %q WHERE COALESCE(user_id, '') <> ?`, t), keeperID)
		if res.Error != nil {
			log.Fatalf("[prune] %s: %v", t, res.Error)
		}
		if res.RowsAffected > 0 {
			log.Printf("[prune] %-24s -%d 行", t, res.RowsAffected)
			cut += res.RowsAffected
		}
	}
	res := dst.Exec(`DELETE FROM users WHERE username <> ?`, keepUsername)
	if res.Error != nil {
		log.Fatalf("[prune] users: %v", res.Error)
	}
	log.Printf("[prune] 合计删掉 %d 行（users 表 -%d）", cut, res.RowsAffected)
}

func tableColumns(db *gorm.DB, table string) (map[string]bool, error) {
	var rows []struct {
		Name string `gorm:"column:name"`
	}
	if err := db.Raw(fmt.Sprintf("PRAGMA table_info(%q)", table)).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("PRAGMA table_info: %w", err)
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.Name] = true
	}
	return out, nil
}

// reconcile 逐表核对「源（剪枝后应有）vs 目标」行数。
func reconcile(src, dst *gorm.DB, keeperID string) bool {
	log.Printf("[reconcile] %-24s %10s %10s", "表", "源(应搬)", "目标")
	bad := 0
	for _, m := range modelTableList(dst) {
		var srcN, dstN int64
		q := src.Table(m.Name)
		cols, err := tableColumns(dst, m.Name)
		if err != nil {
			log.Fatalf("[reconcile] %s: %v", m.Name, err)
		}
		if cols["user_id"] {
			q = q.Where("COALESCE(user_id, '') = ?", keeperID)
		} else if m.Name == "users" {
			q = q.Where("username = ?", keepUsername)
		}
		if err := q.Count(&srcN).Error; err != nil {
			log.Fatalf("[reconcile] 数源 %s: %v", m.Name, err)
		}
		if err := dst.Table(m.Name).Count(&dstN).Error; err != nil {
			log.Fatalf("[reconcile] 数目标 %s: %v", m.Name, err)
		}
		mark := "✅"
		if srcN != dstN {
			mark = "❌"
			bad++
		}
		log.Printf("[reconcile] %s %-22s %10d %10d", mark, m.Name, srcN, dstN)
	}
	return bad == 0
}

func summarize(dst *gorm.DB, dstCfg *config.Config, keeperID string, took time.Duration) {
	var users int64
	if err := dst.Model(&model.User{}).Count(&users).Error; err != nil {
		log.Fatalf("[summary] 数 users: %v", err)
	}
	fmt.Println()
	log.Printf("[summary] 耗时 %s，users 表 %d 行（必须是 1，否则 app 启动会拒绝）", took.Round(time.Second), users)

	key := []string{
		"conversations", "messages", "conversation_summaries", "user_memories",
		"notes", "bills", "documents", "reminders", "theme_states",
		"embeddings", "embedding_chunks", "concept_embeddings",
	}
	for _, t := range key {
		var n int64
		if err := dst.Table(t).Count(&n).Error; err != nil {
			log.Fatalf("[summary] 数 %s: %v", t, err)
		}
		log.Printf("[summary] %-22s %7d", t, n)
	}

	// SQLite 把 datetime 存成文本，MIN/MAX 回来的是字符串（不是 time.Time）
	var span struct {
		Min string
		Max string
	}
	if err := dst.Table("messages").Select("MIN(created_at) as min, MAX(created_at) as max").Scan(&span).Error; err != nil {
		log.Fatalf("[summary] 取消息时间范围: %v", err)
	}
	if span.Min != "" {
		log.Printf("[summary] 消息时间范围 %s → %s", span.Min, span.Max)
	}

	var samples []model.Message
	if err := dst.Where("role = 'user'").Order("created_at DESC").Limit(3).Find(&samples).Error; err != nil {
		log.Fatalf("[summary] 抽样消息: %v", err)
	}
	for _, s := range samples {
		log.Printf("[summary] 样本 %s %s", s.CreatedAt.Format("01-02 15:04"), truncate(s.Content, 70))
	}

	// 向量能不能用取决于存的 model 与当前配置一致（不一致 → 余弦比较没意义）
	var models []string
	if err := dst.Model(&model.Embedding{}).Distinct().Pluck("model", &models).Error; err != nil {
		log.Fatalf("[summary] 取 embedding model: %v", err)
	}
	log.Printf("[summary] 库内 embedding.model=%v，当前配置 embedding.model=%s", models, dstCfg.Embedding.Model)
	for _, m := range models {
		if m != dstCfg.Embedding.Model {
			log.Printf("[summary] ⚠️  %s 与配置不一致 → 向量检索会静默退化，需在设置页把模型调回 %s", m, m)
		}
	}

	var u model.User
	if err := dst.First(&u, "id = ?", keeperID).Error; err != nil {
		log.Fatalf("[summary] 读保留用户: %v", err)
	}
	log.Printf("[summary] 保留用户 %s（%s），注册于 %s", u.Username, u.ID, u.CreatedAt.Format("2006-01-02"))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
