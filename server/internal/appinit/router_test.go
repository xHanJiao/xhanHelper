package appinit

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/config"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func newLocalTestRepo(t *testing.T) *service.UserRepo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	return service.NewUserRepo(db, 60, 4)
}

// localCfg 造一份「配套合法」的本地 dev 配置（第 4 期断言要求四者一致）。
func localCfg(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("APP_ENV", "local.dev")
	cfg := &config.Config{}
	cfg.Server.LocalSingleUser = true
	cfg.Server.LocalEnv = "dev"
	cfg.Server.LocalToken = "dev:tok"
	cfg.DB.Driver = "sqlite"
	cfg.DB.Database = "/tmp/xhanHelper-dev/xhan_helper.db"
	cfg.Storage.ImageDir = "/tmp/xhanHelper-dev/images"
	return cfg
}

// 空库 → 新建默认名 me，并自动进 AdminUsers
func TestEnsureLocalUser_EmptyDBCreatesDefault(t *testing.T) {
	repo := newLocalTestRepo(t)
	cfg := localCfg(t)

	id, err := EnsureLocalUser(cfg, repo)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if id == "" {
		t.Fatal("expected a created user id")
	}
	users, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Username != "me" || users[0].ID != id {
		t.Fatalf("want single user 'me' with id %q, got %+v", id, users)
	}
	if len(cfg.Server.AdminUsers) != 1 || cfg.Server.AdminUsers[0] != id {
		t.Fatalf("expected user appended to AdminUsers, got %v", cfg.Server.AdminUsers)
	}
}

// 空库时可用 local_username 覆盖默认名
func TestEnsureLocalUser_EmptyDBHonorsLocalUsername(t *testing.T) {
	repo := newLocalTestRepo(t)
	cfg := localCfg(t)
	cfg.Server.LocalUsername = "xhan"

	id, err := EnsureLocalUser(cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	users, _ := repo.ListAll(context.Background())
	if len(users) != 1 || users[0].Username != "xhan" || users[0].ID != id {
		t.Fatalf("want 'xhan', got %+v", users)
	}
}

// 库里唯一用户 → 原样认领（迁移回来的 xhan 即走此路）
func TestEnsureLocalUser_SingleExistingUserClaimed(t *testing.T) {
	repo := newLocalTestRepo(t)
	created, err := repo.EnsureByUsername(context.Background(), "xhan")
	if err != nil {
		t.Fatal(err)
	}

	id, err := EnsureLocalUser(localCfg(t), repo)
	if err != nil {
		t.Fatal(err)
	}
	if id != created.ID {
		t.Fatalf("want to claim existing id %q, got %q", created.ID, id)
	}
}

// 多于一个用户 → 报错（剪枝缺失），不静默挑一个
func TestEnsureLocalUser_MultipleUsersErrors(t *testing.T) {
	repo := newLocalTestRepo(t)
	ctx := context.Background()
	for _, name := range []string{"me", "other"} {
		if _, err := repo.EnsureByUsername(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EnsureLocalUser(localCfg(t), repo); err == nil {
		t.Fatal("expected error when >1 user, got nil")
	}
}

// 缺 token → 报错
func TestEnsureLocalUser_MissingTokenErrors(t *testing.T) {
	repo := newLocalTestRepo(t)
	cfg := &config.Config{}
	cfg.Server.LocalSingleUser = true
	if _, err := EnsureLocalUser(cfg, repo); err == nil {
		t.Fatal("expected error when local_token empty")
	}
}

// 非本地模式 → 空 user_id，不碰库
func TestEnsureLocalUser_NonLocalReturnsEmpty(t *testing.T) {
	id, err := EnsureLocalUser(&config.Config{}, newLocalTestRepo(t))
	if err != nil || id != "" {
		t.Fatalf("non-local: want (\"\", nil), got (%q, %v)", id, err)
	}
}

// AssertLocalEnv：四者配套才放行（第 4 期）
func TestAssertLocalEnv(t *testing.T) {
	prodCfg := func(t *testing.T) *config.Config {
		t.Helper()
		t.Setenv("APP_ENV", "local")
		cfg := &config.Config{}
		cfg.Server.LocalSingleUser = true
		cfg.Server.LocalEnv = "prod"
		cfg.Server.LocalToken = "prod:tok"
		cfg.DB.Driver = "sqlite"
		cfg.DB.Database = "/tmp/xhanHelper/xhan_helper.db"
		cfg.Storage.ImageDir = "/tmp/xhanHelper/images"
		return cfg
	}

	if err := AssertLocalEnv(localCfg(t)); err != nil {
		t.Fatalf("dev 配套配置应通过：%v", err)
	}
	if err := AssertLocalEnv(prodCfg(t)); err != nil {
		t.Fatalf("prod 配套配置应通过：%v", err)
	}
	// 非本地模式：完全不检查（也不要求 APP_ENV）
	if err := AssertLocalEnv(&config.Config{}); err != nil {
		t.Fatalf("非本地模式不该检查：%v", err)
	}

	cases := []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"local_env 非法", func(c *config.Config) { c.Server.LocalEnv = "staging" }},
		{"APP_ENV 不配套", func(c *config.Config) { t.Setenv("APP_ENV", "local") }},
		{"数据根不配套（dev 用了 prod 根）", func(c *config.Config) {
			c.DB.Database = "/tmp/xhanHelper/xhan_helper.db"
			c.Storage.ImageDir = "/tmp/xhanHelper/images"
		}},
		{"image_dir 跑出数据根", func(c *config.Config) { c.Storage.ImageDir = "/tmp/elsewhere/images" }},
		{"token 前缀不配套（串了 prod 的 token）", func(c *config.Config) { c.Server.LocalToken = "prod:tok" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := localCfg(t)
			tc.mutate(cfg)
			if err := AssertLocalEnv(cfg); err == nil {
				t.Fatal("期望报错，实际放行")
			}
		})
	}
}
