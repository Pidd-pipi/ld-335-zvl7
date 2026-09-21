package repository

import (
	"github.com/blueship581/gbinsureapi/internal/model"
	"gorm.io/gorm"
)

// SettlementAccountEntryRepository 结算账目流水仓储。
type SettlementAccountEntryRepository struct{ db *gorm.DB }

// NewSettlementAccountEntryRepository 构造账目流水仓储。
func NewSettlementAccountEntryRepository(db *gorm.DB) *SettlementAccountEntryRepository {
	return &SettlementAccountEntryRepository{db: db}
}

// CreateTx 在指定事务中创建账目流水（复核通过时生成负向冲减记录）。
func (r *SettlementAccountEntryRepository) CreateTx(tx *gorm.DB, e *model.SettlementAccountEntry) error {
	return tx.Create(e).Error
}

// ListByOrderID 查询某结算单的账目流水（回读负向冲减记录）。
func (r *SettlementAccountEntryRepository) ListByOrderID(orderID uint) ([]model.SettlementAccountEntry, error) {
	var items []model.SettlementAccountEntry
	err := r.db.Where("settlement_order_id = ?", orderID).Order("id desc").Find(&items).Error
	return items, err
}

// ListByAdjustmentID 查询某差额补退单生成的账目流水。
func (r *SettlementAccountEntryRepository) ListByAdjustmentID(adjustmentID uint) ([]model.SettlementAccountEntry, error) {
	var items []model.SettlementAccountEntry
	err := r.db.Where("adjustment_id = ?", adjustmentID).Order("id desc").Find(&items).Error
	return items, err
}
