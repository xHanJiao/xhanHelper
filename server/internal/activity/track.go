package activity

import (
	"context"
	"fmt"
	"log"
	"reflect"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// 系统变更事件的服务端统一收口：在 root *gorm.DB 上挂 Create/Update/Delete 回调。
// 只在 trackedModels 登记里的模型上发事件；事件名/字段由声明表 + 每模型 classify 决定。
// 设计见 docs/design/activity-events/系统变更事件 · GORM 回调方案（人话版）.md。

const docOldContentKey = "activity:doc_old_content"

// RegisterChangeCallbacks 注册变更事件回调；db 或 store 为 nil 时不注册（不采集）。
func RegisterChangeCallbacks(db *gorm.DB, store *Store) {
	if db == nil || store == nil {
		return
	}
	h := &changeHook{store: store}
	db.Callback().Create().After("gorm:create").Register("activity:change_create", h.onCreate)
	db.Callback().Update().Before("gorm:update").Register("activity:change_before_update", h.onBeforeUpdate)
	db.Callback().Update().After("gorm:update").Register("activity:change_update", h.onUpdate)
	db.Callback().Delete().After("gorm:delete").Register("activity:change_delete", h.onDelete)
}

type changeHook struct{ store *Store }

// modelSpec — 每个 tracked 模型一行：create 从实例取，update/delete 从 stmt/PK 取。
// 返回的 typ 为空表示不发事件。biz_key 仅创建类使用（附表 A：更新类不带业务键）。
type modelSpec struct {
	create func(rv reflect.Value) (typ, bizKey string, attrs map[string]any)
	update func(stmt *gorm.Statement, pk string, vals map[string]any) (typ string, attrs map[string]any)
	delete func(pk string) (typ string, attrs map[string]any)
}

var trackedModels = map[reflect.Type]modelSpec{
	reflect.TypeOf(model.Document{}): {
		create: func(rv reflect.Value) (string, string, map[string]any) {
			d := rv.Interface().(model.Document)
			return TypeDocCreated, d.ID, map[string]any{"doc_id": d.ID, "size_delta": len(d.Content)}
		},
		update: func(stmt *gorm.Statement, pk string, vals map[string]any) (string, map[string]any) {
			if v, ok := vals["status"]; ok && toInt(v) == model.DocumentStatusDeleted {
				return TypeDocDeleted, map[string]any{"doc_id": pk, "size_delta": 0}
			}
			if _, ok := vals["content"]; ok {
				return TypeDocUpdated, map[string]any{"doc_id": pk, "size_delta": docSizeDelta(stmt, vals)}
			}
			if hasAny(vals, "title", "tags") {
				return TypeDocUpdated, map[string]any{"doc_id": pk, "size_delta": 0}
			}
			return "", nil // 恢复(→active)/其余：不发
		},
		delete: func(pk string) (string, map[string]any) {
			return TypeDocDeleted, map[string]any{"doc_id": pk, "size_delta": 0}
		},
	},
	reflect.TypeOf(model.UserMemory{}): {
		create: func(rv reflect.Value) (string, string, map[string]any) {
			m := rv.Interface().(model.UserMemory)
			return TypeMemoryEntityChanged, m.ID, map[string]any{"memory_id": m.ID, "op": "create", "topic": m.Topic}
		},
		update: func(stmt *gorm.Statement, pk string, vals map[string]any) (string, map[string]any) {
			topic, _ := vals["topic"].(string)
			if hasAny(vals, "memory_value", "facts") {
				return TypeMemoryEntityChanged, map[string]any{"memory_id": pk, "op": "update", "topic": topic}
			}
			if _, ok := vals["topic"]; ok {
				if pk != "" {
					return TypeMemoryEntityChanged, map[string]any{"memory_id": pk, "op": "retopic", "topic": topic}
				}
				// 无 PK 的批量改名 → topic.cleaned（count = 影响行数）
				return TypeMemoryTopicCleaned, map[string]any{"count": stmt.RowsAffected, "to": topic}
			}
			return "", nil // confirm 计数等：不发
		},
		delete: func(pk string) (string, map[string]any) {
			return TypeMemoryEntityChanged, map[string]any{"memory_id": pk, "op": "delete"}
		},
	},
	reflect.TypeOf(model.Bill{}): {
		create: func(rv reflect.Value) (string, string, map[string]any) {
			b := rv.Interface().(model.Bill)
			return TypeBillCreated, b.ID, map[string]any{"bill_id": b.ID, "category": b.Category1, "amount": b.Amount}
		},
		// 附表 A 无 bill.updated / 无删除事件
	},
	reflect.TypeOf(model.Reminder{}): {
		create: func(rv reflect.Value) (string, string, map[string]any) {
			r := rv.Interface().(model.Reminder)
			return TypeReminderCreated, r.ID, map[string]any{"reminder_id": r.ID}
		},
		update: func(stmt *gorm.Statement, pk string, vals map[string]any) (string, map[string]any) {
			if s, _ := vals["status"].(string); s == model.ReminderStatusAcknowledged {
				return TypeReminderAcknowledged, map[string]any{"reminder_id": pk}
			}
			return "", nil // cancel / mention / generate：不发
		},
	},
}

// eventPKAttr：这些事件按契约必须携带对应主键属性（附表 A）。update/delete 时若 resolvePK
// 拿到空串，说明调用方没把主键放进 Model/Dest（AGENTS.md 约定），事件照发但降级为无 PK——
// 依据回调方案文档「不做静默降级」，这里留一条警告。无 PK 的合法事件（memory.topic.cleaned）不在表内。
var eventPKAttr = map[string]string{
	TypeDocUpdated:           "doc_id",
	TypeDocDeleted:           "doc_id",
	TypeMemoryEntityChanged:  "memory_id",
	TypeReminderAcknowledged: "reminder_id",
}

// missingPKAttr 判定「该事件按契约应带主键、却拿到空 pk」的情形，返回应修的主键属性名。
// pk 非空、事件本无 PK（memory.topic.cleaned）、或 typ 为空（不发事件）都返回 ok=false。
func missingPKAttr(typ, pk string) (key string, ok bool) {
	if pk != "" {
		return "", false
	}
	key, ok = eventPKAttr[typ]
	return key, ok
}

func warnMissingPK(op, typ, pk string) {
	if key, ok := missingPKAttr(typ, pk); ok {
		log.Printf("[activity] 变更回调 %s：%s 事件缺主键（%s 为空），调用方未把 PK 放进 Model/Dest", op, typ, key)
	}
}

func (h *changeHook) onCreate(db *gorm.DB) { h.handle(db, "create") }
func (h *changeHook) onUpdate(db *gorm.DB) { h.handle(db, "update") }
func (h *changeHook) onDelete(db *gorm.DB) { h.handle(db, "delete") }

// onBeforeUpdate：正文类更新前把旧正文存进 stmt.Settings，供 After 算 size_delta。
func (h *changeHook) onBeforeUpdate(db *gorm.DB) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[activity] 变更回调 before_update panic（忽略）: %v", r)
		}
	}()
	stmt := db.Statement
	if stmt == nil || stmt.Schema == nil || NoEventFromContext(stmt.Context) {
		return
	}
	if stmt.Schema.ModelType != reflect.TypeOf(model.Document{}) {
		return
	}
	if _, ok := updateValues(stmt)["content"]; !ok {
		return
	}
	pk := resolvePK(stmt)
	if pk == "" {
		return
	}
	var old string
	// 只读旧正文；NewDB 避免继承本次 update 的 clauses。
	// ponytail: NewDB 会脱离当前事务——将来若在事务内更新正文，这里读到的是已提交的旧值、size_delta 会偏。
	// 当前所有正文更新都不在事务里，先按此实现。
	if err := db.Session(&gorm.Session{NewDB: true}).Model(&model.Document{}).
		Where("id = ?", pk).Pluck("content", &old).Error; err != nil {
		log.Printf("[activity] 取旧正文失败（size_delta 记 0）: id=%s: %v", pk, err)
		return
	}
	stmt.Settings.Store(docOldContentKey, old)
}

// handle 是回调总入口：查表 → 分发。全程 recover，绝不让埋点弄崩写库。
func (h *changeHook) handle(db *gorm.DB, op string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[activity] 变更回调 %s panic（忽略）: %v", op, r)
		}
	}()
	stmt := db.Statement
	if stmt == nil || stmt.Schema == nil || NoEventFromContext(stmt.Context) {
		return
	}
	spec, ok := trackedModels[stmt.Schema.ModelType]
	if !ok {
		return
	}
	switch op {
	case "create":
		forEachCreated(stmt, func(rv reflect.Value) {
			if spec.create == nil {
				return
			}
			typ, biz, attrs := spec.create(rv)
			h.emit(stmt, typ, biz, attrs)
		})
	case "update":
		// 一条都没命中（wrong user / 已删 / 重复操作）时 GORM 照样触发 After 回调，跳过以免发幻影事件。
		if spec.update == nil || stmt.RowsAffected == 0 {
			return
		}
		pk := resolvePK(stmt)
		typ, attrs := spec.update(stmt, pk, updateValues(stmt))
		warnMissingPK(op, typ, pk)
		h.emit(stmt, typ, "", attrs)
	case "delete":
		if spec.delete == nil || stmt.RowsAffected == 0 {
			return
		}
		pk := resolvePK(stmt)
		typ, attrs := spec.delete(pk)
		warnMissingPK(op, typ, pk)
		h.emit(stmt, typ, "", attrs)
	}
}

func (h *changeHook) emit(stmt *gorm.Statement, typ, bizKey string, attrs map[string]any) {
	if typ == "" {
		return
	}
	ctx := stmt.Context
	if ctx == nil {
		ctx = context.Background()
	}
	h.store.EmitChange(ctx, typ, bizKey, attrs)
}

// forEachCreated 遍历本次 Create 的实例（单条或切片）。
func forEachCreated(stmt *gorm.Statement, fn func(reflect.Value)) {
	rv := deref(stmt.ReflectValue)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			if e := deref(rv.Index(i)); e.IsValid() && e.Kind() == reflect.Struct {
				fn(e)
			}
		}
	case reflect.Struct:
		fn(rv)
	}
}

// resolvePK 从 stmt 的 Model/Dest 里读结构化主键；拿不到返回空串。
// 约定：tracked 实体的 update/delete 要把主键放进 Model/Dest（见 AGENTS.md）。
func resolvePK(stmt *gorm.Statement) string {
	f := stmt.Schema.PrioritizedPrimaryField
	if f == nil {
		return ""
	}
	for _, src := range []any{stmt.Model, stmt.Dest} {
		rv := deref(reflect.ValueOf(src))
		if !rv.IsValid() || rv.Kind() != reflect.Struct {
			continue
		}
		if v, zero := f.ValueOf(stmt.Context, rv); !zero {
			return fmt.Sprint(v)
		}
	}
	return ""
}

// updateValues 取本次更新的「列 → 新值」。map 更新直接用；结构体更新读脏字段。
func updateValues(stmt *gorm.Statement) map[string]any {
	out := map[string]any{}
	switch d := stmt.Dest.(type) {
	case map[string]any:
		for k, v := range d {
			out[k] = v
		}
		return out
	}
	rv := deref(reflect.ValueOf(stmt.Dest))
	if rv.IsValid() && rv.Kind() == reflect.Struct {
		for _, f := range stmt.Schema.Fields {
			if f.DBName != "" && stmt.Changed(f.DBName) {
				if v, zero := f.ValueOf(stmt.Context, rv); !zero {
					out[f.DBName] = v
				}
			}
		}
	}
	return out
}

func docSizeDelta(stmt *gorm.Statement, vals map[string]any) int {
	c, ok := vals["content"].(string)
	if !ok {
		return 0
	}
	old := 0
	if v, ok := stmt.Settings.Load(docOldContentKey); ok {
		if s, ok := v.(string); ok {
			old = len(s)
		}
	}
	return len(c) - old
}

func deref(v reflect.Value) reflect.Value {
	for v.IsValid() && v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

func hasAny(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int8:
		return int(n)
	case int16:
		return int(n)
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	}
	return 0
}
