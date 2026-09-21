package repository

import (
	"errors"

	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/util"
	"gorm.io/gorm"
)

// SettlementAdjustmentRepository 结算差额补退单仓储。
type SettlementAdjustmentRepository struct{ db *gorm.DB }

// NewSettlementAdjustmentRepository 构造差额补退单仓储。
func NewSettlementAdjustmentRepository(db *gorm.DB) *SettlementAdjustmentRepository {
	return &SettlementAdjustmentRepository{db: db}
}

// CreateTx 在指定事务中创建差额补退单（部分唯一索引兜底重复待复核）。
func (r *SettlementAdjustmentRepository) CreateTx(tx *gorm.DB, a *model.SettlementAdjustment) error {
	return tx.Create(a).Error
}

// ExistsPendingByOrderID 同一结算单是否已存在待复核差额单。
func (r *SettlementAdjustmentRepository) ExistsPendingByOrderID(tx *gorm.DB, orderID uint) (bool, error) {
	var count int64
	err := tx.Model(&model.SettlementAdjustment{}).
		Where("settlement_order_id = ? AND status = ?", orderID, "pending_review").
		Count(&count).Error
	return count > 0, err
}

// FindByNo 按差额补退单号查询。
func (r *SettlementAdjustmentRepository) FindByNo(no string) (*model.SettlementAdjustment, error) {
	var a model.SettlementAdjustment
	if err := r.db.Where("adjustment_no = ?", no).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// FindByNoTx 在指定事务中按差额补退单号查询。
func (r *SettlementAdjustmentRepository) FindByNoTx(tx *gorm.DB, no string) (*model.SettlementAdjustment, error) {
	var a model.SettlementAdjustment
	if err := tx.Where("adjustment_no = ?", no).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// UpdateTx 在指定事务中更新差额补退单（CAS：仅待复核可改）。
func (r *SettlementAdjustmentRepository) UpdateTx(tx *gorm.DB, a *model.SettlementAdjustment) error {
	return tx.Save(a).Error
}

// CompareAndSetReview 仅当差额单当前状态为 pending_review 时整体更新复核结果（并发复核只成功一次）。
// fields 中需包含新的 status；返回受影响行数：1 表示抢占成功，0 表示已被并发复核。
func (r *SettlementAdjustmentRepository) CompareAndSetReview(tx *gorm.DB, id uint, fields map[string]any) (int64, error) {
	res := tx.Model(&model.SettlementAdjustment{}).
		Where("id = ? AND status = ?", id, "pending_review").
		Updates(fields)
	return res.RowsAffected, res.Error
}

// FindByOrderID 查询某结算单最新的差额补退单。
func (r *SettlementAdjustmentRepository) FindByOrderID(orderID uint) (*model.SettlementAdjustment, error) {
	var a model.SettlementAdjustment
	if err := r.db.Where("settlement_order_id = ?", orderID).Order("id desc").First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// ListByOrderID 查询某结算单全部差额补退单（驳回后再次申请的历史）。
func (r *SettlementAdjustmentRepository) ListByOrderID(orderID uint) ([]model.SettlementAdjustment, error) {
	var items []model.SettlementAdjustment
	err := r.db.Where("settlement_order_id = ?", orderID).Order("id desc").Find(&items).Error
	return items, err
}

// List 分页查询差额补退单。
func (r *SettlementAdjustmentRepository) List(clientID uint, status string, page, pageSize int) ([]model.SettlementAdjustment, int64, error) {
	q := r.db.Model(&model.SettlementAdjustment{})
	if clientID > 0 {
		q = q.Where("client_id = ?", clientID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.SettlementAdjustment
	tx := r.db.Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize)
	if clientID > 0 {
		tx = tx.Where("client_id = ?", clientID)
	}
	if status != "" {
		tx = tx.Where("status = ?", status)
	}
	err := tx.Find(&items).Error
	return items, total, err
}
