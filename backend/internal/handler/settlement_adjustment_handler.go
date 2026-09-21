package handler

import (
	"log/slog"

	"github.com/blueship581/gbinsureapi/internal/dto"
	"github.com/blueship581/gbinsureapi/internal/service"
	"github.com/blueship581/gbinsureapi/internal/util"
	"github.com/gin-gonic/gin"
)

// SettlementAdjustmentHandler 结算差额补退接口。
type SettlementAdjustmentHandler struct {
	svc *service.SettlementAdjustmentService
	log *slog.Logger
}

// NewSettlementAdjustmentHandler 构造差额补退接口。
func NewSettlementAdjustmentHandler(svc *service.SettlementAdjustmentService, log *slog.Logger) *SettlementAdjustmentHandler {
	return &SettlementAdjustmentHandler{svc: svc, log: log}
}

// Apply 提交结算差额补退申请。
// @Summary 结算差额补退申请
// @Description 已结算订单提交目标医保支付额与原因后生成待复核差额单；同一订单只允许一条待复核记录，目标金额小于零或不小于原支付额时拒绝
// @Tags settlement-adjustments
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param settlement_no path string true "结算单号"
// @Param body body dto.CreateAdjustmentRequest true "目标医保支付额与原因"
// @Success 201 {object} util.Response
// @Router /api/v1/settlements/{settlement_no}/adjustments [post]
func (h *SettlementAdjustmentHandler) Apply(c *gin.Context) {
	var req dto.CreateAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(util.BadRequest("差额补退参数（SettlementAdjustment）不合法", err))
		return
	}
	adj, err := h.svc.ApplyAdjustment(c.Request.Context(), c.Param("settlement_no"), *req.TargetPayAmount, req.Reason)
	if err != nil {
		c.Error(err)
		return
	}
	util.Created(c, adj)
}

// Review 复核差额补退单。
// @Summary 差额补退复核
// @Description 复核通过后订单标记已补退并生成负向记录冲减差额；驳回保留原因且可再次申请；重复/并发复核只成功一次
// @Tags settlement-adjustments
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param adjustment_no path string true "差额补退单号"
// @Param body body dto.ReviewAdjustmentRequest true "复核结论（approved + reason）"
// @Success 200 {object} util.Response
// @Router /api/v1/settlement-adjustments/{adjustment_no}/review [post]
func (h *SettlementAdjustmentHandler) Review(c *gin.Context) {
	var req dto.ReviewAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(util.BadRequest("差额补退复核参数（SettlementAdjustment）不合法", err))
		return
	}
	adj, err := h.svc.ReviewAdjustment(c.Request.Context(), c.Param("adjustment_no"), req.Approved, req.Reason)
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, adj)
}

// Detail 差额补退单详情（回读差额单与负向账目流水）。
// @Summary 差额补退单详情
// @Tags settlement-adjustments
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param adjustment_no path string true "差额补退单号"
// @Success 200 {object} util.Response
// @Router /api/v1/settlement-adjustments/{adjustment_no} [get]
func (h *SettlementAdjustmentHandler) Detail(c *gin.Context) {
	detail, err := h.svc.GetAdjustment(c.Request.Context(), c.Param("adjustment_no"))
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, detail)
}

// List 差额补退单分页查询。
// @Summary 差额补退单列表
// @Tags settlement-adjustments
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param client_id query int false "调用方 ID"
// @Param status query string false "状态(pending_review/approved/rejected)"
// @Param page query int false "页码"
// @Param page_size query int false "每页数量"
// @Success 200 {object} util.Response
// @Router /api/v1/settlement-adjustments [get]
func (h *SettlementAdjustmentHandler) List(c *gin.Context) {
	clientID := parseUint(c.Query("client_id"))
	page := parseQueryInt(c.Query("page"), 1)
	pageSize := parseQueryInt(c.Query("page_size"), 20)
	items, total, err := h.svc.ListAdjustments(c.Request.Context(), clientID, c.Query("status"), page, pageSize)
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, util.PageData{List: items, Total: total, Page: page, Size: pageSize})
}

// ListByOrder 查询某结算单的差额补退记录。
// @Summary 结算单差额补退记录
// @Tags settlement-adjustments
// @Security ApiKeyAuth
// @Security BearerAuth
// @Param settlement_no path string true "结算单号"
// @Success 200 {object} util.Response
// @Router /api/v1/settlements/{settlement_no}/adjustments [get]
func (h *SettlementAdjustmentHandler) ListByOrder(c *gin.Context) {
	items, err := h.svc.ListOrderAdjustments(c.Request.Context(), c.Param("settlement_no"))
	if err != nil {
		c.Error(err)
		return
	}
	util.OK(c, items)
}
