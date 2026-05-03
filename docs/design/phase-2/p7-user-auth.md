# P7 — 用户账号密码认证 技术方案

> 日期: 2026-05-03 | 状态: 方案评审

---

## 一、目标

用最简单的方案实现多用户隔离，为向量记忆系统打好基础。不引入 JWT。

---

## 二、数据模型

```go
// model/user.go

type User struct {
    ID           string    `gorm:"primaryKey;size:36"`
    Username     string    `gorm:"size:50;uniqueIndex;not null"`
    PasswordHash string    `gorm:"size:255;not null"`      // bcrypt
    CreatedAt    time.Time `gorm:"autoCreateTime"`
}

type UserToken struct {
    ID        string    `gorm:"primaryKey;size:36"`
    UserID    string    `gorm:"size:36;not null;index"`
    Token     string    `gorm:"size:36;uniqueIndex;not null"`
    CreatedAt time.Time `gorm:"autoCreateTime"`
}
```

---

## 三、API

```
POST /api/auth/register  {username, password}
  → bcrypt → INSERT user + INSERT token
  → {user_id, token}

POST /api/auth/login     {username, password}
  → 查 user → bcrypt compare → INSERT token
  → {user_id, token}

POST /api/auth/logout    (需要认证)
  → DELETE token WHERE token = ?
  → {ok: true}
```

---

## 四、认证中间件

跟现在 device middleware 一模一样，只是查的表换了：

```
Authorization: Bearer <token>
  → 查 user_tokens WHERE token = ?
  → 命中 → 注入 UserID 到 context
  → 未命中 → 401
```

---

## 五、全局迁移：DeviceID → UserID

所有数据模型用 `DeviceID` 的地方换成 `UserID`：

| 模型 | 影响 |
|------|------|
| Bill | DeviceID → UserID |
| Note | DeviceID → UserID |
| Conversation | DeviceID → UserID |
| UserMemory | DeviceID → UserID |

对应 repo 全部跟随改名。

---

## 六、实施顺序

| 步骤 | 内容 |
|------|------|
| 1 | model/user.go + service/user_repo.go |
| 2 | controller/auth.go（register/login/logout） |
| 3 | middleware/auth.go 改造 |
| 4 | DeviceID → UserID 全局重命名 |
| 5 | 客户端加登录页 |
| 6 | make test 更新 |
