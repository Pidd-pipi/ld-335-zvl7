package repository

import (
	"errors"

	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/util"
	"gorm.io/gorm"
)

// SettlementOrderRepository 结算单仓储。
type SettlementOrderRepository struct{ db *gorm.DB }

// NewSettlementOrderRepository 构造结算单仓储。
func NewSettlementOrderRepository(db *gorm.DB) *SettlementOrderRepository {
	return &SettlementOrderRepository{db: db}
}

// Create 创建结算单。
func (r *SettlementOrderRepository) Create(order *model.SettlementOrder) error {
	return r.db.Create(order).Error
}

// FindByNo 按结算单号查询。
func (r *SettlementOrderRepository) FindByNo(no string) (*model.SettlementOrder, error) {
	var order model.SettlementOrder
	if err := r.db.Where("settlement_no = ?", no).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.ErrNotFound
		}
		return nil, err
	}
	return &order, nil
}

// FindByNoTx 在指定事务中按结算单号查询。
func (r *SettlementOrderRepository) FindByNoTx(tx *gorm.DB, no string) (*model.SettlementOrder, error) {
	var order model.SettlementOrder
	if err := tx.Where("settlement_no = ?", no).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.ErrNotFound
		}
		return nil, err
	}
	return &order, nil
}

// ExistsByNo 结算单号是否存在。
func (r *SettlementOrderRepository) ExistsByNo(no string) (bool, error) {
	var count int64
	err := r.db.Model(&model.SettlementOrder{}).Where("settlement_no = ?", no).Count(&count).Error
	return count > 0, err
}

// Update 更新结算单。
func (r *SettlementOrderRepository) Update(order *model.SettlementOrder) error {
	return r.db.Save(order).Error
}

// TouchRow 对结算单行做一次无变化的状态写操作，用于在事务中加行级锁。
// PostgreSQL 下等价于 SELECT ... FOR UPDATE 的并发串行效果；SQLite 写事务同样串行化。
func (r *SettlementOrderRepository) TouchRow(tx *gorm.DB, id uint) error {
	return tx.Model(&model.SettlementOrder{}).Where("id = ?", id).
		Update("status", gorm.Expr("status")).Error
}

// CompareAndSetStatus 仅当订单当前状态为 expect 时更新为 target（并发安全的 CAS）。
// 返回受影响行数：1 表示抢占成功，0 表示状态已被并发改动。
func (r *SettlementOrderRepository) CompareAndSetStatus(tx *gorm.DB, id uint, expect, target string) (int64, error) {
	res := tx.Model(&model.SettlementOrder{}).
		Where("id = ? AND status = ?", id, expect).
		Update("status", target)
	return res.RowsAffected, res.Error
}

// List 分页查询。
func (r *SettlementOrderRepository) List(clientID uint, status string, page, pageSize int) ([]model.SettlementOrder, int64, error) {
	q := r.db.Model(&model.SettlementOrder{})
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
	var orders []model.SettlementOrder
	err := r.db.Where("client_id = ?", clientID).Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&orders).Error
	return orders, total, err
}

// TodaySettled 当日已结算（对账，date 为 Asia/Shanghai 日期串）。
func (r *SettlementOrderRepository) TodaySettled(clientID uint, date string) ([]model.SettlementOrder, error) {
	var orders []model.SettlementOrder
	q := r.db.Where("settled_at::date = ?", date)
	if clientID > 0 {
		q = q.Where("client_id = ?", clientID)
	}
	err := q.Find(&orders).Error
	return orders, err
}

// Count 统计。
func (r *SettlementOrderRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.SettlementOrder{}).Count(&count).Error
	return count, err
}
