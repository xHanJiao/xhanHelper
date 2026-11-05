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

---

## 后续变更

- 2026-11-05：认证加固。
  - token 改为存 `SHA-256(token)` 的 64 位 hex（DB 列名仍为 `token`，宽度 36→64）；`FindByToken` / `RefreshToken` / `Logout` 按哈希查询。启动时清掉旧的 36 位明文行，**升级后所有客户端需重新登录一次**。
  - `ChangePassword` 在事务内更新密码并吊销该用户除当前 token 外的全部 token；管理员 `ResetPassword` 吊销全部 token。
  - 密码策略：最少 10 个字符、最多 72 字节（bcrypt 上限），注册 / 改密 / 管理员创建统一校验；管理员重置改为 `crypto/rand` 随机密码。
  - 登录时用户不存在也跑一次 dummy bcrypt，拉平用户名枚举的时序差。
  - 登录/注册限流：同一 IP+用户名 15 分钟 10 次失败，同一 IP 15 分钟 50 次失败，超限 429（IP 维度防换用户名绕过 dummy bcrypt 的成本放大）；登录成功/注册成功清零对应计数。
  - 错误码拆分：登录未知用户或密码错 401、注册重名 409、管理员重置未知用户 404；其余数据库故障记日志后回 500，不再伪装成 401/409/404。
  - 影响文件：`server/internal/model/user.go`、`server/internal/service/user_repo.go`、`server/internal/controller/auth.go`、`server/internal/controller/password.go`、`server/internal/controller/admin_user.go`、`server/internal/app/init_db.go`。
