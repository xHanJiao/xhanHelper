package service

import (
	"context"
	"errors"
	"log"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
)

// DocumentShareRepo 文档分享仓库
type DocumentShareRepo struct {
	db           *gorm.DB
	documentRepo *DocumentRepo
}

// NewDocumentShareRepo 创建文档分享仓库
func NewDocumentShareRepo(db *gorm.DB, docRepo *DocumentRepo) *DocumentShareRepo {
	return &DocumentShareRepo{db: db, documentRepo: docRepo}
}

// Create 创建或重置分享链接
func (r *DocumentShareRepo) Create(ctx context.Context, docID, userID string) (*model.DocumentShare, error) {
	if _, err := r.documentRepo.GetByID(ctx, docID, userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("文档不存在")
		}
		return nil, err
	}

	if err := r.db.WithContext(ctx).Where("document_id = ?", docID).Delete(&model.DocumentShare{}).Error; err != nil {
		log.Printf("[share] delete old share failed: %v", err)
	}

	share := &model.DocumentShare{
		ID:         uuid.New().String(),
		DocumentID: docID,
		Token:      uuid.New().String(),
	}

	if err := r.db.WithContext(ctx).Create(share).Error; err != nil {
		return nil, err
	}

	return share, nil
}

// GetByToken 根据 token 获取分享信息及关联文档
func (r *DocumentShareRepo) GetByToken(ctx context.Context, token string) (*model.DocumentShare, *model.Document, error) {
	var share model.DocumentShare
	if err := r.db.WithContext(ctx).Where("token = ?", token).First(&share).Error; err != nil {
		return nil, nil, err
	}

	doc, err := r.documentRepo.GetByIDPublic(ctx, share.DocumentID)
	if err != nil {
		return &share, nil, err
	}

	return &share, doc, nil
}

// GetByDocumentID 根据文档 ID 查询当前分享（需验证用户归属）
func (r *DocumentShareRepo) GetByDocumentID(ctx context.Context, docID, userID string) (*model.DocumentShare, error) {
	if _, err := r.documentRepo.GetByID(ctx, docID, userID); err != nil {
		return nil, err
	}

	var share model.DocumentShare
	if err := r.db.WithContext(ctx).Where("document_id = ?", docID).First(&share).Error; err != nil {
		return nil, err
	}

	return &share, nil
}

// IncrementVisitCount 递增分享链接的访问次数
func (r *DocumentShareRepo) IncrementVisitCount(ctx context.Context, token string) {
	if err := r.db.WithContext(ctx).Model(&model.DocumentShare{}).Where("token = ?", token).UpdateColumn("visit_count", gorm.Expr("visit_count + 1")).Error; err != nil {
		log.Printf("[share] increment visit count failed: token=%s err=%v", token, err)
	}
}
