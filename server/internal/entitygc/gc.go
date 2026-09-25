// Package entitygc 清理孤儿派生数据 / 硬删取消态笔记。
//
// 两类用途：
//   - Run：清理历史遗留的孤儿派生数据（bug 017 前的删除路径只 SoftDelete，
//     留下笔记的向量行 + 版本快照、记忆的向量行）。幂等。
//   - PurgeCancelledNotes：把 status=取消态的笔记连根删除（行 + chunk/向量/版本），
//     用户主动取消的笔记不再需要保留。整批一个事务，幂等。
//
// 只覆盖 note / memory：concept 孤儿由 topic-merge 的 GC 处理，message 孤儿另属子系统。
package entitygc

import (
	"context"
	"fmt"
	"log"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// Options — 运行参数。
type Options struct {
	Write bool // false = dry-run，只统计不落库
}

// Summary — 清理汇总。
type Summary struct {
	SoftDeletedNotes int
	NoteEmbeddings   int
	NoteVersions     int64
	MemoryEmbeddings int
}

// Run 扫描并清理 note / memory 的孤儿派生数据。
func Run(db *gorm.DB, opts Options) (*Summary, error) {
	ctx := context.Background()

	validNotes, err := pluckIDs(ctx, db, &model.Note{}, "id", "status = ?", model.NoteStatusNormal)
	if err != nil {
		return nil, fmt.Errorf("载入有效笔记 id: %w", err)
	}
	softNotes, err := pluckIDs(ctx, db, &model.Note{}, "id", "status <> ?", model.NoteStatusNormal)
	if err != nil {
		return nil, fmt.Errorf("载入软删笔记 id: %w", err)
	}
	validMems, err := pluckIDs(ctx, db, &model.UserMemory{}, "id")
	if err != nil {
		return nil, fmt.Errorf("载入记忆 id: %w", err)
	}
	noteEmbIDs, err := pluckIDs(ctx, db, &model.Embedding{}, "entity_id", "entity_type = ?", model.EntityTypeNote)
	if err != nil {
		return nil, fmt.Errorf("载入笔记向量 id: %w", err)
	}
	memEmbIDs, err := pluckIDs(ctx, db, &model.Embedding{}, "entity_id", "entity_type = ?", model.EntityTypeMemory)
	if err != nil {
		return nil, fmt.Errorf("载入记忆向量 id: %w", err)
	}

	orphanNoteEmb := diff(noteEmbIDs, toSet(validNotes))
	orphanMemEmb := diff(memEmbIDs, toSet(validMems))
	orphanNoteIDs := union(softNotes, orphanNoteEmb)

	sum := &Summary{
		SoftDeletedNotes: len(softNotes),
		NoteEmbeddings:   len(orphanNoteEmb),
		MemoryEmbeddings: len(orphanMemEmb),
	}

	if !opts.Write {
		n, err := countVersions(ctx, db, orphanNoteIDs)
		if err != nil {
			return nil, fmt.Errorf("统计笔记版本快照: %w", err)
		}
		sum.NoteVersions = n
		log.Printf("[entity-gc] (dry-run) 软删笔记 %d，笔记孤儿向量 %d，记忆孤儿向量 %d，笔记孤儿版本快照 %d",
			sum.SoftDeletedNotes, sum.NoteEmbeddings, sum.MemoryEmbeddings, sum.NoteVersions)
		return sum, nil
	}

	embRepo := service.NewEmbeddingRepo(db)
	if _, err := embRepo.DeleteByEntities(ctx, model.EntityTypeNote, orphanNoteEmb); err != nil {
		return nil, fmt.Errorf("删笔记孤儿向量: %w", err)
	}
	if _, err := embRepo.DeleteByEntities(ctx, model.EntityTypeMemory, orphanMemEmb); err != nil {
		return nil, fmt.Errorf("删记忆孤儿向量: %w", err)
	}
	n, err := deleteVersions(ctx, db, orphanNoteIDs)
	if err != nil {
		return nil, fmt.Errorf("删笔记孤儿版本快照: %w", err)
	}
	sum.NoteVersions = n
	log.Printf("[entity-gc] 已清理：笔记孤儿向量 %d，记忆孤儿向量 %d，笔记孤儿版本快照 %d",
		sum.NoteEmbeddings, sum.MemoryEmbeddings, sum.NoteVersions)
	return sum, nil
}

// PurgeCancelledNotes 硬删除所有取消态笔记，并批量清理其派生数据
// （chunk / 全文向量 / 版本快照）。整批一个事务，失败全回滚；幂等（重跑为 0）。
// CLI 场景无内存向量库，DB 行即事实来源（重启后重新加载）。
func PurgeCancelledNotes(db *gorm.DB, opts Options) (int, error) {
	ctx := context.Background()
	ids, err := pluckIDs(ctx, db, &model.Note{}, "id", "status = ?", model.NoteStatusCancelled)
	if err != nil {
		return 0, fmt.Errorf("载入取消态笔记 id: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if !opts.Write {
		return len(ids), nil
	}

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		embRepo := service.NewEmbeddingRepo(tx)
		chunkRepo := service.NewEmbeddingChunkRepo(tx)
		versionRepo := service.NewVersionRepo(tx)
		noteRepo := service.NewNoteRepo(tx)

		// chunk：先取 chunk 键（向量行以 chunk 键为 entity_id），再批量删向量行 + chunk 行。
		chunks, err := chunkRepo.BatchGetByParentIDs(ctx, model.EntityTypeNote, ids)
		if err != nil {
			return fmt.Errorf("载入笔记 chunk: %w", err)
		}
		chunkKeys := make([]string, 0, len(chunks))
		for _, c := range chunks {
			chunkKeys = append(chunkKeys, model.ChunkVectorKey(model.EntityTypeNote, c.ParentID, c.ChunkIndex))
		}
		if _, err := embRepo.DeleteByEntities(ctx, model.EntityTypeChunk, chunkKeys); err != nil {
			return fmt.Errorf("删笔记 chunk 向量: %w", err)
		}
		if _, err := chunkRepo.DeleteByParents(ctx, model.EntityTypeNote, ids); err != nil {
			return fmt.Errorf("删笔记 chunk 行: %w", err)
		}

		// 全文向量 + 版本快照 + 笔记行。
		if _, err := embRepo.DeleteByEntities(ctx, model.EntityTypeNote, ids); err != nil {
			return fmt.Errorf("删笔记全文向量: %w", err)
		}
		if _, err := versionRepo.DeleteByEntities(ctx, model.EntityTypeNote, ids); err != nil {
			return fmt.Errorf("删笔记版本快照: %w", err)
		}
		if _, err := noteRepo.DeleteByIDs(ctx, ids); err != nil {
			return fmt.Errorf("硬删笔记行: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("清理取消态笔记（共 %d 条，事务已回滚）: %w", len(ids), err)
	}
	return len(ids), nil
}

// pluckIDs 用 GORM Pluck 取一列（ids 在 Go 里比对，避免 SQL 跨列 collation 坑）。
func pluckIDs(ctx context.Context, db *gorm.DB, m interface{}, column string, conds ...interface{}) ([]string, error) {
	var ids []string
	q := db.WithContext(ctx).Model(m)
	if len(conds) > 0 {
		q = q.Where(conds[0], conds[1:]...)
	}
	return ids, q.Pluck(column, &ids).Error
}

func countVersions(ctx context.Context, db *gorm.DB, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var n int64
	err := db.WithContext(ctx).Model(&model.ContentVersion{}).
		Where("entity_type = ? AND entity_id IN ?", model.EntityTypeNote, ids).
		Count(&n).Error
	return n, err
}

func deleteVersions(ctx context.Context, db *gorm.DB, ids []string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := db.WithContext(ctx).
		Where("entity_type = ? AND entity_id IN ?", model.EntityTypeNote, ids).
		Delete(&model.ContentVersion{})
	return res.RowsAffected, res.Error
}

func toSet(ids []string) map[string]bool {
	s := make(map[string]bool, len(ids))
	for _, id := range ids {
		s[id] = true
	}
	return s
}

// diff 返回 child 中不在 valid 里的元素（保序、去重）。
func diff(child []string, valid map[string]bool) []string {
	seen := make(map[string]bool, len(child))
	var out []string
	for _, id := range child {
		if valid[id] || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// union 合并两串 id（去重，保序）。
func union(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, id := range append(append([]string{}, a...), b...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
