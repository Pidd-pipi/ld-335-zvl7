package dto

// CalculatePresettlementRequest 预结算请求。
type CalculatePresettlementRequest struct {
	BatchID uint `json:"batch_id" binding:"required"`
}

// SubmitSettlementRequest 正式结算请求。
type SubmitSettlementRequest struct {
	PresettlementID uint `json:"presettlement_id" binding:"required"`
}

// CreateAdjustmentRequest 差额补退提交请求：目标医保支付额 + 申请原因。
type CreateAdjustmentRequest struct {
	TargetInsurancePay float64 `json:"target_insurance_pay"`
	Reason             string  `json:"reason" binding:"required,min=1,max=500"`
}

// ReviewAdjustmentRequest 差额补退复核请求：approve=true 通过，approve=false 驳回（驳回须填 review_remark）。
type ReviewAdjustmentRequest struct {
	Approve      bool   `json:"approve"`
	ReviewRemark string `json:"review_remark" binding:"max=500"`
}
