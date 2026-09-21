package model

import "time"

// SettlementAdjustment 结算差额补退单：对已结算订单提交目标医保支付额，经复核通过后冲减差额。
type SettlementAdjustment struct {
	ID                   uint       `gorm:"primaryKey" json:"id"`
	AdjustmentNo         string     `gorm:"size:32;uniqueIndex;not null" json:"adjustment_no"`
	SettlementOrderID    uint       `gorm:"index;not null" json:"settlement_order_id"`
	SettlementNo         string     `gorm:"size:32;index;not null" json:"settlement_no"`
	ClientID             uint       `gorm:"index;not null" json:"client_id"`
	OriginalInsurancePay float64    `json:"original_insurance_pay"`
	TargetInsurancePay   float64    `json:"target_insurance_pay"`
	DifferenceAmount     float64    `json:"difference_amount"`
	Reason               string     `gorm:"size:500;not null" json:"reason"`
	Status               string     `gorm:"size:20;default:pending_review;not null" json:"status"`
	ReviewRemark         string     `gorm:"size:500" json:"review_remark"`
	ReviewerClientID     uint       `gorm:"default:0" json:"reviewer_client_id"`
	ReviewedAt           *time.Time `json:"reviewed_at"`
	CreatedAt            time.Time  `json:"created_at"`

	// Ledger 复核通过时生成的负向冲减账目（非持久化，查询回读时装配）。
	Ledger *AdjustmentLedger `gorm:"-" json:"ledger,omitempty"`
}
