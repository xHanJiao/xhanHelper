package service

import (
	"context"

	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// DocumentAnnotationReplyRepo — 文档批注回复（讨论线程）数据访问
type DocumentAnnotationReplyRepo struct {
	db *gorm.DB
}

func NewDocumentAnnotationReplyRepo(db *gorm.DB) *DocumentAnnotationReplyRepo {
	return &DocumentAnnotationReplyRepo{db: db}
}

// CreateBatch 批量写入回复行（一次 INSERT，原子）。
func (r *DocumentAnnotationReplyRepo) CreateBatch(ctx context.Context, replies []model.DocumentAnnotationReply) error {
	if len(replies) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&replies).Error
}

// ListByAnnotationID 拉某条批注的完整线程（分页，时间正序）。limit < 0 视为默认 100；limit == 0 只取总数。
func (r *DocumentAnnotationReplyRepo) ListByAnnotationID(ctx context.Context, annotationID, userID string, limit, offset int) ([]model.DocumentAnnotationReply, int64, error) {
	if limit < 0 {
		limit = 100
	}
	q := r.db.WithContext(ctx).Model(&model.DocumentAnnotationReply{}).
		Where("annotation_id = ? AND user_id = ?", annotationID, userID)

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if limit == 0 {
		return []model.DocumentAnnotationReply{}, total, nil
	}

	var list []model.DocumentAnnotationReply
	err := q.Order("created_at ASC, id ASC").Limit(limit).Offset(offset).Find(&list).Error
	return list, total, err
}

// CountByAnnotation 该批注已有多少条回复（配额校验用）
func (r *DocumentAnnotationReplyRepo) CountByAnnotation(ctx context.Context, annotationID, userID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.DocumentAnnotationReply{}).
		Where("annotation_id = ? AND user_id = ?", annotationID, userID).Count(&n).Error
	return n, err
}

// CountsByAnnotationIDs 一次算出多条批注各自的回复数（列表接口用，防 N+1）。
func (r *DocumentAnnotationReplyRepo) CountsByAnnotationIDs(ctx context.Context, annotationIDs []string, userID string) (map[string]int64, error) {
	out := make(map[string]int64, len(annotationIDs))
	if len(annotationIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		AnnotationID string
		N            int64
	}
	err := r.db.WithContext(ctx).Model(&model.DocumentAnnotationReply{}).
		Select("annotation_id, COUNT(*) AS n").
		Where("annotation_id IN ? AND user_id = ?", annotationIDs, userID).
		Group("annotation_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.AnnotationID] = row.N
	}
	return out, nil
}

// LatestByAnnotationIDs 一次取多条批注各自最新一条回复（列表接口折叠行预览用，防 N+1）。
func (r *DocumentAnnotationReplyRepo) LatestByAnnotationIDs(ctx context.Context, annotationIDs []string, userID string) (map[string]model.DocumentAnnotationReply, error) {
	out := make(map[string]model.DocumentAnnotationReply, len(annotationIDs))
	if len(annotationIDs) == 0 {
		return out, nil
	}
	var list []model.DocumentAnnotationReply
	err := r.db.WithContext(ctx).
		Where("annotation_id IN ? AND user_id = ?", annotationIDs, userID).
		Order("annotation_id ASC, created_at DESC, id DESC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	for _, rep := range list {
		if _, ok := out[rep.AnnotationID]; !ok {
			out[rep.AnnotationID] = rep // 已按时间倒序，第一条即最新
		}
	}
	return out, nil
}

// ListRecentByAnnotationIDs 一次取多条批注各自最近 perAnnotation 条回复（工具读线程用，时间正序）。
// 用窗口函数在每个 annotation 内部分组取最近 N 条（MySQL 8 / SQLite 3.25+ 均支持）。
// ponytail: 单条查询返回上限 = 批注数 × perAnnotation；调用方已对批注分页，量有界。
func (r *DocumentAnnotationReplyRepo) ListRecentByAnnotationIDs(ctx context.Context, annotationIDs []string, userID string, perAnnotation int) (map[string][]model.DocumentAnnotationReply, error) {
	out := make(map[string][]model.DocumentAnnotationReply, len(annotationIDs))
	if len(annotationIDs) == 0 || perAnnotation <= 0 {
		return out, nil
	}
	var list []model.DocumentAnnotationReply
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, annotation_id, document_id, user_id, role, content, source, created_at
		FROM (
			SELECT id, annotation_id, document_id, user_id, role, content, source, created_at,
			       ROW_NUMBER() OVER (PARTITION BY annotation_id ORDER BY created_at DESC, id DESC) AS rn
			FROM document_annotation_replies
			WHERE annotation_id IN ? AND user_id = ?
		) t
		WHERE rn <= ?
		ORDER BY annotation_id ASC, created_at ASC, id ASC`,
		annotationIDs, userID, perAnnotation).Scan(&list).Error
	if err != nil {
		return nil, err
	}
	for _, rep := range list {
		out[rep.AnnotationID] = append(out[rep.AnnotationID], rep)
	}
	return out, nil
}

// DeleteByAnnotation 删除某条批注的全部回复（删批注时级联）
func (r *DocumentAnnotationReplyRepo) DeleteByAnnotation(ctx context.Context, annotationID, userID string) (int64, error) {
	res := r.db.WithContext(ctx).Delete(&model.DocumentAnnotationReply{}, "annotation_id = ? AND user_id = ?", annotationID, userID)
	return res.RowsAffected, res.Error
}

// DeleteByDocument 删除某文档的全部回复（文档物理清理时级联）
func (r *DocumentAnnotationReplyRepo) DeleteByDocument(ctx context.Context, docID, userID string) (int64, error) {
	res := r.db.WithContext(ctx).Delete(&model.DocumentAnnotationReply{}, "document_id = ? AND user_id = ?", docID, userID)
	return res.RowsAffected, res.Error
}
