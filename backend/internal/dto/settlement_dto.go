package dto

import "github.com/blueship581/gbinsureapi/internal/model"

// CalculatePresettlementRequest 预结算请求。
type CalculatePresettlementRequest struct {
	BatchID uint `json:"batch_id" binding:"required"`
}

// SubmitSettlementRequest 正式结算请求。
type SubmitSettlementRequest struct {
	PresettlementID uint `json:"presettlement_id" binding:"required"`
}

// CreateAdjustmentRequest 结算差额补退申请请求。
// TargetPayAmount 用指针区分“未传”与“传 0”；0 元为合法目标金额（全额冲减）。
type CreateAdjustmentRequest struct {
	TargetPayAmount *float64 `json:"target_pay_amount" binding:"required"`
	Reason          string   `json:"reason" binding:"required,max=500"`
}

// ReviewAdjustmentRequest 差额补退复核请求。
// approved=true 通过；approved=false 驳回且必须填写 reason。
type ReviewAdjustmentRequest struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason" binding:"max=500"`
}

// AdjustmentDetailResponse 差额补退单详情：差额单 + 复核通过后生成的负向账目流水（回读）。
type AdjustmentDetailResponse struct {
	model.SettlementAdjustment
	Entries []model.SettlementAccountEntry `json:"entries"`
}
