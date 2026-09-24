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

func localCfg() *config.Config {
	cfg := &config.Config{}
	cfg.Server.LocalSingleUser = true
	cfg.Server.LocalToken = "tok"
	return cfg
}

// 空库 → 新建默认名 me，并自动进 AdminUsers
func TestEnsureLocalUser_EmptyDBCreatesDefault(t *testing.T) {
	repo := newLocalTestRepo(t)
	cfg := localCfg()

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
	cfg := localCfg()
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

	id, err := EnsureLocalUser(localCfg(), repo)
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
	if _, err := EnsureLocalUser(localCfg(), repo); err == nil {
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
