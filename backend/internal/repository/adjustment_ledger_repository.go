package repository

import (
	"errors"

	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/util"
	"gorm.io/gorm"
)

// AdjustmentLedgerRepository 差额补退账目仓储。
type AdjustmentLedgerRepository struct{ db *gorm.DB }

// NewAdjustmentLedgerRepository 构造差额补退账目仓储。
func NewAdjustmentLedgerRepository(db *gorm.DB) *AdjustmentLedgerRepository {
	return &AdjustmentLedgerRepository{db: db}
}

// CreateWithTx 在给定事务内创建负向账目记录。
func (r *AdjustmentLedgerRepository) CreateWithTx(tx *gorm.DB, l *model.AdjustmentLedger) error {
	return tx.Create(l).Error
}

// FindByAdjustmentID 按补退单 ID 查询负向账目（无则返回 util.ErrNotFound）。
func (r *AdjustmentLedgerRepository) FindByAdjustmentID(adjustmentID uint) (*model.AdjustmentLedger, error) {
	var l model.AdjustmentLedger
	if err := r.db.Where("adjustment_id = ?", adjustmentID).First(&l).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, util.ErrNotFound
		}
		return nil, err
	}
	return &l, nil
}

// MapByAdjustmentIDs 批量查询账目并按补退单 ID 建立映射（列表回读装配用）。
func (r *AdjustmentLedgerRepository) MapByAdjustmentIDs(ids []uint) (map[uint]model.AdjustmentLedger, error) {
	out := make(map[uint]model.AdjustmentLedger)
	if len(ids) == 0 {
		return out, nil
	}
	var ledgers []model.AdjustmentLedger
	if err := r.db.Where("adjustment_id IN ?", ids).Find(&ledgers).Error; err != nil {
		return nil, err
	}
	for i := range ledgers {
		out[ledgers[i].AdjustmentID] = ledgers[i]
	}
	return out, nil
}
