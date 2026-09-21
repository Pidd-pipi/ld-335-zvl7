package handler

import (
	"log/slog"

	"github.com/blueship581/gbinsureapi/internal/dto"
	"github.com/blueship581/gbinsureapi/internal/middleware"
	"github.com/blueship581/gbinsureapi/internal/service"
	"github.com/blueship581/gbinsureapi/internal/util"
	"github.com/gin-gonic/gin"
)

// SettlementAdjustmentHandler 结算差额补退接口。
type SettlementAdjustmentHandler struct {
	svc *service.AdjustmentService
	log *slog.Logger
}

// NewSettlementAdjustmentHandler 构造差额补退接口。
func NewSettlementAdjustmentHandler(svc *service.AdjustmentService, log *slog.Logger) *SettlementAdjustmentHandler {
	return &SettlementAdjustmentHandler{svc: svc, log: log}
}

// Submit 差额补退提交。
// @Summary 差额补退提交
// @Description 已结算订单提交目标医保支付额与原因，生成待复核差额单；同一订单只允许一条待复核记录
// @Tags settlements
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param settlement_no path string true "结算单号"
// @Param body body dto.CreateAdjustmentRequest true "目标医保支付额与原因"
// @Success 201 {object} util.Response
// @Router /api/v1/settlements/{settlement_no}/adjustments [post]
func (h *SettlementAdjustmentHandler) Submit(c *gin.Context) {
	var req dto.CreateAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(util.BadRequest("差额补退参数（SettlementAdjustment）不合法", err))
		return
	}
	clientID, _ := c.Get(middleware.ClientIDKey)
	adj, err := h.svc.Submit(c.Request.Context(), c.Param("settlement_no"), clientID.(uint), req.TargetInsurancePay, req.Reason)
	if err != nil {
		c.Error(err)
		return
	}
	util.Created(c, adj)
}

// Review 差额补退复核。
// @Summary 差额补退复核
// @Description 复核通过订单标记已补退并生成负向记录冲减差额；驳回保留原因且可再次申请；并发复核只能成功一次
// @Tags settlements
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param adjustment_no path string true "差额补退单号"
// @Param body body dto.ReviewAdjustmentRequest true "复核结论"
// @Success 200 {object} util.Response
// @Router /api/v1/settlements/adjustments/{adjustment_no}/review [post]
func (h *SettlementAdjustmentHandler) Review(c *gin.Context) {
	var req dto.ReviewAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(util.BadRequest("差额补退复核参数（SettlementAdjustment）不合法", err))
		return
	}
	reviewerID, _ := c.Get(middleware.ClientIDKey)
	adj, err := h.svc.Review(c.Request.Context(), c.Param("adjustment_no"), reviewerID.(uint), req.Approve, req.ReviewRemark)
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, adj)
}

// ListByOrder 查询某结算单的差额补退单。
// @Summary 结算单差额补退列表
// @Tags settlements
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param settlement_no path string true "结算单号"
// @Success 200 {object} util.Response
// @Router /api/v1/settlements/{settlement_no}/adjustments [get]
func (h *SettlementAdjustmentHandler) ListByOrder(c *gin.Context) {
	list, err := h.svc.ListBySettlementNo(c.Request.Context(), c.Param("settlement_no"))
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, gin.H{"list": list, "total": len(list)})
}

// Detail 查询单条差额补退单。
// @Summary 差额补退单详情
// @Tags settlements
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param adjustment_no path string true "差额补退单号"
// @Success 200 {object} util.Response
// @Router /api/v1/settlements/adjustments/{adjustment_no} [get]
func (h *SettlementAdjustmentHandler) Detail(c *gin.Context) {
	adj, err := h.svc.GetByNo(c.Request.Context(), c.Param("adjustment_no"))
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, adj)
}
