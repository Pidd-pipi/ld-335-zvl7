package model

import "time"

// SettlementAccountEntry 结算账目流水：复核通过后生成负向记录冲减医保支付差额。
type SettlementAccountEntry struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	EntryNo           string    `gorm:"size:32;uniqueIndex;not null" json:"entry_no"`
	SettlementOrderID uint      `gorm:"index;not null" json:"settlement_order_id"`
	SettlementNo      string    `gorm:"size:32;index;not null" json:"settlement_no"`
	AdjustmentID      uint      `gorm:"index;not null" json:"adjustment_id"`
	AdjustmentNo      string    `gorm:"size:32;index;not null" json:"adjustment_no"`
	ClientID          uint      `gorm:"index;not null" json:"client_id"`
	Direction         string    `gorm:"size:10;not null" json:"direction"`
	Amount            float64   `gorm:"not null" json:"amount"`
	Reason            string    `gorm:"size:500" json:"reason"`
	CreatedAt         time.Time `json:"created_at"`
}
