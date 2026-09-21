package repository

import (
	"errors"
	"strings"

	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SettlementAdjustmentRepository 差额补退单仓储。
type SettlementAdjustmentRepository struct{ db *gorm.DB }

// NewSettlementAdjustmentRepository 构造差额补退单仓储。
func NewSettlementAdjustmentRepository(db *gorm.DB) *SettlementAdjustmentRepository {
	return &SettlementAdjustmentRepository{db: db}
}

// Create 创建差额补退单。
func (r *SettlementAdjustmentRepository) Create(a *model.SettlementAdjustment) error {
	return r.db.Create(a).Error
}

// Count 统计（生成补退单号用）。
func (r *SettlementAdjustmentRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.SettlementAdjustment{}).Count(&count).Error
	return count, err
}

// ExistsByNo 补退单号是否存在。
func (r *SettlementAdjustmentRepository) ExistsByNo(no string) (bool, error) {
	var count int64
	err := r.db.Model(&model.SettlementAdjustment{}).Where("adjustment_no = ?", no).Count(&count).Error
	return count > 0, err
}

// FindPendingByOrderID 查询某结算单当前待复核补退单（无则返回 ErrNotFound）。
func (r *SettlementAdjustmentRepository) FindPendingByOrderID(orderID uint) (*model.SettlementAdjustment, error) {
	var a model.SettlementAdjustment
	err := r.db.Where("settlement_order_id = ? AND status = ?", orderID, "pending_review").
		Order("id desc").First(&a).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// FindByNo 按补退单号查询。
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

// FindByNoForUpdate 按补退单号查询并加行锁（事务内调用；PostgreSQL 生效，SQLite 为 no-op）。
func (r *SettlementAdjustmentRepository) FindByNoForUpdate(tx *gorm.DB, no string) (*model.SettlementAdjustment, error) {
	var a model.SettlementAdjustment
	q := tx.Where("adjustment_no = ?", no)
	if r.db.Dialector.Name() == "postgres" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// UpdateWithTx 在给定事务内更新补退单。
func (r *SettlementAdjustmentRepository) UpdateWithTx(tx *gorm.DB, a *model.SettlementAdjustment) error {
	return tx.Save(a).Error
}

// ListBySettlementNo 查询某结算单的全部补退单（按时间倒序）。
func (r *SettlementAdjustmentRepository) ListBySettlementNo(settlementNo string) ([]model.SettlementAdjustment, error) {
	var list []model.SettlementAdjustment
	err := r.db.Where("settlement_no = ?", settlementNo).Order("id desc").Find(&list).Error
	return list, err
}

// IsDuplicateKey 是否唯一约束冲突（PostgreSQL 23505 / SQLite UNIQUE / MySQL Duplicate entry）。
func IsDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "23505") || strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate entry") || strings.Contains(msg, "duplicated key")
}
