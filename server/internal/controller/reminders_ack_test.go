package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// 点通知后的 ack 必须把触发消息写回**提醒的来源会话**，不再落默认会话
// （默认会话概念正在淡出：启动已改开空白新对话）。
// 来源为空（秘书 / 反思自动生成的提醒）时只置已确认，不得凭空造会话。
func TestAckReminder_WritesIntoSourceConversation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReminderTestDB(t)
	convRepo := service.NewConversationRepo(db)
	reminderRepo := service.NewReminderRepo(db)
	msgRepo := service.NewMessageRepo(db)
	h := NewReminderHandler(reminderRepo, convRepo, msgRepo)
	ctx := t.Context()

	if err := convRepo.Create(ctx, &model.Conversation{ID: "c-src", UserID: "u1", Title: "提醒来源"}); err != nil {
		t.Fatalf("create conv: %v", err)
	}
	seedReminder(t, db, &model.Reminder{
		ID: "r-src", UserID: "u1", ConversationID: "c-src",
		Content: "交房租", TriggerMessage: "该交房租了", Status: model.ReminderStatusPending,
	})

	resp := ackReminder(t, h, "r-src")

	if got := resp["conversation_id"]; got != "c-src" {
		t.Errorf("应返回来源会话 c-src，got %v", got)
	}
	if got := resp["conversation_title"]; got != "提醒来源" {
		t.Errorf("应返回来源会话标题，got %v", got)
	}
	var msgs []model.Message
	if err := db.Where("conversation_id = ?", "c-src").Find(&msgs).Error; err != nil {
		t.Fatalf("query messages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "该交房租了" || msgs[0].Role != "assistant" {
		t.Fatalf("触发消息应写进来源会话，got %+v", msgs)
	}
	if msgs[0].ID != resp["trigger_message_msg_id"] {
		t.Errorf("trigger_message_msg_id 应指向刚插入的消息")
	}
	assertAcknowledged(t, db, "r-src")
}

func TestAckReminder_NoSourceConversationDoesNotCreateOne(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReminderTestDB(t)
	convRepo := service.NewConversationRepo(db)
	h := NewReminderHandler(service.NewReminderRepo(db), convRepo, service.NewMessageRepo(db))

	// 秘书 / 反思自动生成的提醒：没有来源会话
	seedReminder(t, db, &model.Reminder{
		ID: "r-orphan", UserID: "u1", Content: "该写周报了", Status: model.ReminderStatusPending,
	})

	resp := ackReminder(t, h, "r-orphan")

	if got := resp["conversation_id"]; got != "" {
		t.Errorf("没有来源会话时应返回空，got %v", got)
	}
	if got := resp["trigger_message"]; got != "该写周报了" {
		t.Errorf("trigger_message 空时应回落到 content，got %v", got)
	}
	var msgCount, convCount int64
	if err := db.Model(&model.Message{}).Count(&msgCount).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := db.Model(&model.Conversation{}).Count(&convCount).Error; err != nil {
		t.Fatalf("count convs: %v", err)
	}
	if msgCount != 0 || convCount != 0 {
		t.Errorf("不该写消息也不该建会话，got msgs=%d convs=%d", msgCount, convCount)
	}
	assertAcknowledged(t, db, "r-orphan")

	// 再点一次：没有 msgID 可当幂等键，必须靠 status 判（否则每次点通知都重跑一遍）。
	// 比的是库里的 acknowledged_at —— 首次响应里的时间取自 handler 的 now，比落库晚一点点。
	var acked model.Reminder
	if err := db.Where("id = ?", "r-orphan").First(&acked).Error; err != nil {
		t.Fatalf("query reminder: %v", err)
	}
	again := ackReminder(t, h, "r-orphan")
	if again["status"] != model.ReminderStatusAcknowledged {
		t.Errorf("重复点应仍是 acknowledged，got %v", again["status"])
	}
	if got := ackedAtUnix(t, again); got != acked.AcknowledgedAt.Unix() {
		t.Errorf("重复点不该刷新 acknowledged_at，got %d vs %d", got, acked.AcknowledgedAt.Unix())
	}
}

// 来源会话不可用时（别人的 / 已删的 / 子会话）一律不写消息：往别人的会话写是越权，
// 往子会话写是写进用户看不见的地方。
func TestAckReminder_RejectsUnusableConversations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		conv *model.Conversation
	}{
		{"别人的会话", &model.Conversation{ID: "c-other", UserID: "u2", Title: "别家的"}},
		{"已删的会话", &model.Conversation{ID: "c-gone", UserID: "u1", Title: "删了", IsDeleted: true}},
		{"子任务会话", &model.Conversation{ID: "c-sub", UserID: "u1", Title: "子任务", ParentID: stringPtr("c-src")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openReminderTestDB(t)
			convRepo := service.NewConversationRepo(db)
			if err := convRepo.Create(t.Context(), &model.Conversation{ID: "c-src", UserID: "u1", Title: "来源"}); err != nil {
				t.Fatalf("create parent conv: %v", err)
			}
			if err := convRepo.Create(t.Context(), tc.conv); err != nil {
				t.Fatalf("create conv: %v", err)
			}
			h := NewReminderHandler(service.NewReminderRepo(db), convRepo, service.NewMessageRepo(db))
			seedReminder(t, db, &model.Reminder{
				ID: "r1", UserID: "u1", ConversationID: tc.conv.ID,
				Content: "交房租", TriggerMessage: "该交房租了", Status: model.ReminderStatusPending,
			})

			if got := ackReminder(t, h, "r1")["conversation_id"]; got != "" {
				t.Errorf("不可用会话不应被返回/写入，got %v", got)
			}
			var msgCount int64
			if err := db.Model(&model.Message{}).Where("conversation_id = ?", tc.conv.ID).Count(&msgCount).Error; err != nil {
				t.Fatalf("count messages: %v", err)
			}
			if msgCount != 0 {
				t.Errorf("不该往这段会话写消息，got %d 条", msgCount)
			}
			assertAcknowledged(t, db, "r1")
		})
	}
}

// 重复点通知必须幂等：不重复插消息、不刷新 acknowledged_at，响应仍带会话（还能跳）。
func TestAckReminder_RepeatTapIsIdempotent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openReminderTestDB(t)
	convRepo := service.NewConversationRepo(db)
	h := NewReminderHandler(service.NewReminderRepo(db), convRepo, service.NewMessageRepo(db))
	if err := convRepo.Create(t.Context(), &model.Conversation{ID: "c-src", UserID: "u1", Title: "来源"}); err != nil {
		t.Fatalf("create conv: %v", err)
	}
	seedReminder(t, db, &model.Reminder{
		ID: "r1", UserID: "u1", ConversationID: "c-src",
		Content: "交房租", TriggerMessage: "该交房租了", Status: model.ReminderStatusPending,
	})

	first := ackReminder(t, h, "r1")
	second := ackReminder(t, h, "r1")

	if second["conversation_id"] != "c-src" {
		t.Errorf("重复点也要能跳回来源会话，got %v", second["conversation_id"])
	}
	if second["trigger_message_msg_id"] != first["trigger_message_msg_id"] {
		t.Errorf("重复点应返回同一条消息 id，got %v vs %v",
			second["trigger_message_msg_id"], first["trigger_message_msg_id"])
	}
	var msgCount int64
	if err := db.Model(&model.Message{}).Count(&msgCount).Error; err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if msgCount != 1 {
		t.Errorf("重复点不该重复插消息，got %d 条", msgCount)
	}
	var rem model.Reminder
	if err := db.Where("id = ?", "r1").First(&rem).Error; err != nil {
		t.Fatalf("query reminder: %v", err)
	}
	if rem.AcknowledgedAt.Unix() != ackedAtUnix(t, first) {
		t.Errorf("重复点不该刷新 acknowledged_at")
	}
}

func ackedAtUnix(t *testing.T, resp map[string]interface{}) int64 {
	t.Helper()
	s, _ := resp["acknowledged_at"].(string)
	ts, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse acknowledged_at %q: %v", s, err)
	}
	return ts.Unix()
}

func stringPtr(s string) *string { return &s }

func openReminderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "t.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Conversation{}, &model.Reminder{}, &model.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedReminder(t *testing.T, db *gorm.DB, rem *model.Reminder) {
	t.Helper()
	if err := service.NewReminderRepo(db).Create(t.Context(), rem); err != nil {
		t.Fatalf("create reminder: %v", err)
	}
}

// ackReminder 走一遍 gin handler，返回 JSONOK 的 data 段。
func ackReminder(t *testing.T, h *ReminderHandler, id string) map[string]interface{} {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/reminders/"+id+"/ack", nil)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Set("user_id", "u1")
	h.AckReminder(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, w.Body.String())
	}
	return resp.Data
}

func assertAcknowledged(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	var rem model.Reminder
	if err := db.Where("id = ?", id).First(&rem).Error; err != nil {
		t.Fatalf("query reminder: %v", err)
	}
	if rem.Status != model.ReminderStatusAcknowledged {
		t.Errorf("状态应为 acknowledged，got %q", rem.Status)
	}
}
