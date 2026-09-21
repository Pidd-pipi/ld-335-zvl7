package model

import "time"

// SettlementAdjustment 结算差额补退单：已结算订单提交目标医保支付额后生成待复核记录。
// 同一结算单只允许一条 pending_review 记录（由部分唯一索引 idx_adjust_pending_order 保证）；
// 驳回后原记录保留 reject_reason，允许再次申请（生成新记录）。
type SettlementAdjustment struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	AdjustmentNo      string     `gorm:"size:32;uniqueIndex;not null" json:"adjustment_no"`
	SettlementOrderID uint       `gorm:"index;not null" json:"settlement_order_id"`
	SettlementNo      string     `gorm:"size:32;index;not null" json:"settlement_no"`
	ClientID          uint       `gorm:"index;not null" json:"client_id"`
	Status            string     `gorm:"size:20;default:pending_review;not null" json:"status"`
	OriginalPayAmount float64    `gorm:"not null" json:"original_pay_amount"`
	TargetPayAmount   float64    `gorm:"not null" json:"target_pay_amount"`
	DifferenceAmount  float64    `gorm:"not null" json:"difference_amount"`
	ApplyReason       string     `gorm:"size:500;not null" json:"apply_reason"`
	RejectReason      string     `gorm:"size:500" json:"reject_reason"`
	ReviewedAt        *time.Time `json:"reviewed_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// PendingOrderUniqueIndexSQL 同一结算单仅允许一条待复核差额单的部分唯一索引。
// PostgreSQL 与 SQLite 均支持 WHERE 条件的部分索引；IF NOT EXISTS 保证重启幂等。
const PendingOrderUniqueIndexSQL = `CREATE UNIQUE INDEX IF NOT EXISTS idx_adjust_pending_order ` +
	`ON settlement_adjustments (settlement_order_id) WHERE status = 'pending_review'`
