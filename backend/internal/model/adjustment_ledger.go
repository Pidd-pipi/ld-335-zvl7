package model

import "time"

// AdjustmentLedger 差额补退账目：复核通过后生成的负向记录，冲减原医保支付差额。
type AdjustmentLedger struct {
	ID                   uint      `gorm:"primaryKey" json:"id"`
	AdjustmentID         uint      `gorm:"uniqueIndex;not null" json:"adjustment_id"`
	AdjustmentNo         string    `gorm:"size:32;index;not null" json:"adjustment_no"`
	SettlementOrderID    uint      `gorm:"index;not null" json:"settlement_order_id"`
	SettlementNo         string    `gorm:"size:32;index;not null" json:"settlement_no"`
	Direction            string    `gorm:"size:10;not null" json:"direction"`
	Amount               float64   `json:"amount"`
	OriginalInsurancePay float64   `json:"original_insurance_pay"`
	TargetInsurancePay   float64   `json:"target_insurance_pay"`
	Note                 string    `gorm:"size:200" json:"note"`
	CreatedAt            time.Time `json:"created_at"`
}
