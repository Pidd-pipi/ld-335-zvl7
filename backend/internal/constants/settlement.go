package constants

// SettlementStatus 结算状态枚举（README 枚举出现位置清单必列）。
const (
	SettlementPresettled    = "presettled"     // 已预结算
	SettlementSettled       = "settled"        // 已正式结算
	SettlementReversed      = "reversed"       // 已冲正
	SettlementFailed        = "failed"         // 失败
	SettlementPendingManual = "pending_manual" // 待人工处理
	SettlementAdjusted      = "adjusted"       // 已差额补退
)

// SettlementStatuses 全部结算状态。
var SettlementStatuses = []string{
	SettlementPresettled, SettlementSettled, SettlementReversed,
	SettlementFailed, SettlementPendingManual, SettlementAdjusted,
}

// AdjustmentStatus 差额补退单状态。
const (
	AdjustmentPendingReview = "pending_review" // 待复核
	AdjustmentApproved      = "approved"       // 复核通过（已补退）
	AdjustmentRejected      = "rejected"       // 复核驳回（保留原因，可再次申请）
)

// AdjustmentStatuses 全部差额补退单状态。
var AdjustmentStatuses = []string{
	AdjustmentPendingReview, AdjustmentApproved, AdjustmentRejected,
}

// LedgerDirection 账目方向。
const (
	LedgerDirectionNegative = "negative" // 负向冲减
)

// UploadStatus 上传批次状态。
const (
	UploadValidating = "validating" // 校验中
	UploadValidated  = "validated"  // 校验通过
	UploadDuplicate  = "duplicate"  // 重复
	UploadFailed     = "failed"     // 校验失败
)

// ClientStatus 调用方状态。
const (
	ClientActive   = "active"
	ClientDisabled = "disabled"
)
