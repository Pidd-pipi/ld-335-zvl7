package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/blueship581/gbinsureapi/internal/constants"
	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/repository"
	"github.com/blueship581/gbinsureapi/internal/util"
	"gorm.io/gorm"
)

// AdjustmentService 结算差额补退服务：提交待复核差额单、复核（通过生成负向账目冲减差额 / 驳回）、查询回读。
type AdjustmentService struct {
	db         *gorm.DB
	orderRepo  *repository.SettlementOrderRepository
	adjRepo    *repository.SettlementAdjustmentRepository
	ledgerRepo *repository.AdjustmentLedgerRepository
	log        *slog.Logger
}

// NewAdjustmentService 构造差额补退服务。
func NewAdjustmentService(db *gorm.DB, orderRepo *repository.SettlementOrderRepository, adjRepo *repository.SettlementAdjustmentRepository, ledgerRepo *repository.AdjustmentLedgerRepository, log *slog.Logger) *AdjustmentService {
	return &AdjustmentService{db: db, orderRepo: orderRepo, adjRepo: adjRepo, ledgerRepo: ledgerRepo, log: log}
}

// Submit 已结算订单提交目标医保支付额与原因，生成待复核差额单。
// 目标金额小于零或不小于原支付额拒绝；同一订单只允许一条待复核记录。
func (s *AdjustmentService) Submit(ctx context.Context, settlementNo string, clientID uint, target float64, reason string) (*model.SettlementAdjustment, error) {
	order, err := s.orderRepo.FindByNo(settlementNo)
	if err != nil {
		if errors.Is(err, util.ErrNotFound) {
			return nil, util.NotFoundError(constants.MsgSettlementNotFound, err)
		}
		return nil, err
	}
	// 状态不符：仅已结算订单允许申请
	if order.Status != constants.SettlementSettled {
		return nil, util.NewAppError(constants.CodeAdjustmentOrderState, 409, constants.MsgAdjustmentOrderState,
			fmt.Errorf("SettlementAdjustment[settlement_no=%s] submit rejected: order status=%s is not %s", settlementNo, order.Status, constants.SettlementSettled))
	}
	// 目标金额小于零或不小于原支付额时拒绝
	if target < 0 || target >= order.InsurancePayAmount {
		return nil, util.NewAppError(constants.CodeAdjustmentTargetInvalid, 400, constants.MsgAdjustmentTargetInvalid,
			fmt.Errorf("SettlementAdjustment[settlement_no=%s] target=%.2f original=%.2f out of range", settlementNo, target, order.InsurancePayAmount))
	}
	// 同一订单只允许一条待复核记录（应用层预检）
	if pending, perr := s.adjRepo.FindPendingByOrderID(order.ID); perr == nil && pending != nil {
		return nil, util.NewAppError(constants.CodeAdjustmentExists, 409, constants.MsgAdjustmentPendingExists,
			fmt.Errorf("SettlementAdjustment[no=%s] already pending for settlement=%s", pending.AdjustmentNo, settlementNo))
	} else if perr != nil && !errors.Is(perr, util.ErrNotFound) {
		return nil, util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("check pending adjustment: %w", perr))
	}
	// 唯一补退单号（冲突重试）
	no, err := s.generateAdjustmentNo()
	if err != nil {
		return nil, err
	}
	adj := &model.SettlementAdjustment{
		AdjustmentNo:         no,
		SettlementOrderID:    order.ID,
		SettlementNo:         order.SettlementNo,
		ClientID:             clientID,
		OriginalInsurancePay: order.InsurancePayAmount,
		TargetInsurancePay:   target,
		DifferenceAmount:     roundMoney(target - order.InsurancePayAmount),
		Reason:               reason,
		Status:               constants.AdjustmentPendingReview,
	}
	if err := s.adjRepo.Create(adj); err != nil {
		// 并发提交兜底：数据库部分唯一索引拦截，保证只有一条待复核记录
		if repository.IsDuplicateKey(err) {
			return nil, util.NewAppError(constants.CodeAdjustmentExists, 409, constants.MsgAdjustmentPendingExists,
				fmt.Errorf("SettlementAdjustment concurrent insert conflicts for settlement=%s", settlementNo))
		}
		return nil, util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("create adjustment: %w", err))
	}
	s.log.InfoContext(ctx, constants.LOG_ADJUSTMENT_SUBMITTED, "adjustment_no", no, "settlement_no", settlementNo, "client_id", clientID, "target", target)
	return adj, nil
}

// Review 复核差额单：通过则订单标记已补退并生成负向记录冲减差额；驳回保留原因可再次申请。
// 重复提交、状态不符或并发复核只能成功一次，失败不得改动订单和账目（事务保证）。
func (s *AdjustmentService) Review(ctx context.Context, adjustmentNo string, reviewerClientID uint, approve bool, remark string) (*model.SettlementAdjustment, error) {
	adj, err := s.adjRepo.FindByNo(adjustmentNo)
	if err != nil {
		if errors.Is(err, util.ErrNotFound) {
			return nil, util.NotFoundError(constants.MsgAdjustmentNotFound, err)
		}
		return nil, err
	}
	// 状态不符预检
	if adj.Status != constants.AdjustmentPendingReview {
		return nil, util.NewAppError(constants.CodeAdjustmentStateInvalid, 409, constants.MsgAdjustmentStateInvalid,
			fmt.Errorf("SettlementAdjustment[no=%s] review rejected: status=%s", adjustmentNo, adj.Status))
	}
	if !approve && remark == "" {
		return nil, util.NewAppError(constants.CodeAdjustmentReviewInvalid, 400, constants.MsgAdjustmentReviewInvalid,
			fmt.Errorf("SettlementAdjustment[no=%s] reject requires review_remark", adjustmentNo))
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		// 事务内对补退单与订单加行锁并再次校验状态，串行化并发复核
		lockedAdj, lerr := s.adjRepo.FindByNoForUpdate(tx, adjustmentNo)
		if lerr != nil {
			return fmt.Errorf("lock adjustment: %w", lerr)
		}
		if lockedAdj.Status != constants.AdjustmentPendingReview {
			return util.NewAppError(constants.CodeAdjustmentStateInvalid, 409, constants.MsgAdjustmentStateInvalid,
				fmt.Errorf("SettlementAdjustment[no=%s] concurrent review: status=%s", adjustmentNo, lockedAdj.Status))
		}
		lockedOrder, oerr := s.orderRepo.FindByIDForUpdate(tx, lockedAdj.SettlementOrderID)
		if oerr != nil {
			return fmt.Errorf("lock settlement order: %w", oerr)
		}
		if lockedOrder.Status != constants.SettlementSettled {
			return util.NewAppError(constants.CodeAdjustmentOrderState, 409, constants.MsgAdjustmentOrderState,
				fmt.Errorf("SettlementOrder[no=%s] status=%s not adjustable", lockedOrder.SettlementNo, lockedOrder.Status))
		}

		now := time.Now()
		lockedAdj.ReviewerClientID = reviewerClientID
		lockedAdj.ReviewedAt = &now
		lockedAdj.ReviewRemark = remark
		if approve {
			lockedAdj.Status = constants.AdjustmentApproved
			if err := s.adjRepo.UpdateWithTx(tx, lockedAdj); err != nil {
				return fmt.Errorf("update adjustment approved: %w", err)
			}
			// 订单标记已补退
			lockedOrder.Status = constants.SettlementAdjusted
			if err := s.orderRepo.UpdateWithTx(tx, lockedOrder); err != nil {
				return fmt.Errorf("mark order adjusted: %w", err)
			}
			// 生成负向记录冲减差额（adjustment_id 唯一索引兜底，防止重复入账）
			ledger := &model.AdjustmentLedger{
				AdjustmentID:         lockedAdj.ID,
				AdjustmentNo:         lockedAdj.AdjustmentNo,
				SettlementOrderID:    lockedOrder.ID,
				SettlementNo:         lockedOrder.SettlementNo,
				Direction:            constants.LedgerDirectionNegative,
				Amount:               lockedAdj.DifferenceAmount,
				OriginalInsurancePay: lockedAdj.OriginalInsurancePay,
				TargetInsurancePay:   lockedAdj.TargetInsurancePay,
				Note:                 fmt.Sprintf("结算差额补退冲减：原医保支付 %.2f 调整为 %.2f", lockedAdj.OriginalInsurancePay, lockedAdj.TargetInsurancePay),
			}
			if err := s.ledgerRepo.CreateWithTx(tx, ledger); err != nil {
				return fmt.Errorf("write negative ledger: %w", err)
			}
			lockedAdj.Ledger = ledger
		} else {
			lockedAdj.Status = constants.AdjustmentRejected
			if err := s.adjRepo.UpdateWithTx(tx, lockedAdj); err != nil {
				return fmt.Errorf("update adjustment rejected: %w", err)
			}
			// 驳回不改动订单和账目
		}
		adj = lockedAdj
		return nil
	})
	if err != nil {
		var appErr *util.AppError
		if errors.As(err, &appErr) {
			return nil, appErr
		}
		return nil, util.LogError(s.log, constants.LOG_ADJUSTMENT_REVIEW_FAILED, err)
	}
	if approve {
		s.log.InfoContext(ctx, constants.LOG_ADJUSTMENT_REVIEW_APPROVED, "adjustment_no", adjustmentNo, "reviewer", reviewerClientID, "difference", adj.DifferenceAmount)
	} else {
		s.log.InfoContext(ctx, constants.LOG_ADJUSTMENT_REVIEW_REJECTED, "adjustment_no", adjustmentNo, "reviewer", reviewerClientID)
	}
	return adj, nil
}

// ListBySettlementNo 查询某结算单的全部差额补退单（含通过时生成的负向账目）。
func (s *AdjustmentService) ListBySettlementNo(ctx context.Context, settlementNo string) ([]model.SettlementAdjustment, error) {
	order, err := s.orderRepo.FindByNo(settlementNo)
	if err != nil {
		if errors.Is(err, util.ErrNotFound) {
			return nil, util.NotFoundError(constants.MsgSettlementNotFound, err)
		}
		return nil, err
	}
	list, err := s.adjRepo.ListBySettlementNo(order.SettlementNo)
	if err != nil {
		return nil, util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("list adjustments: %w", err))
	}
	if err := s.attachLedgers(list); err != nil {
		return nil, err
	}
	return list, nil
}

// GetByNo 查询单条差额补退单（含通过时生成的负向账目）。
func (s *AdjustmentService) GetByNo(ctx context.Context, adjustmentNo string) (*model.SettlementAdjustment, error) {
	adj, err := s.adjRepo.FindByNo(adjustmentNo)
	if err != nil {
		if errors.Is(err, util.ErrNotFound) {
			return nil, util.NotFoundError(constants.MsgAdjustmentNotFound, err)
		}
		return nil, err
	}
	if ledger, lerr := s.ledgerRepo.FindByAdjustmentID(adj.ID); lerr == nil {
		adj.Ledger = ledger
	} else if !errors.Is(lerr, util.ErrNotFound) {
		return nil, util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("load adjustment ledger: %w", lerr))
	}
	return adj, nil
}

// attachLedgers 批量装配负向账目。
func (s *AdjustmentService) attachLedgers(list []model.SettlementAdjustment) error {
	ids := make([]uint, 0, len(list))
	for i := range list {
		if list[i].Status == constants.AdjustmentApproved {
			ids = append(ids, list[i].ID)
		}
	}
	ledgerMap, err := s.ledgerRepo.MapByAdjustmentIDs(ids)
	if err != nil {
		return util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("map adjustment ledgers: %w", err))
	}
	for i := range list {
		if l, ok := ledgerMap[list[i].ID]; ok {
			l := l
			list[i].Ledger = &l
		}
	}
	return nil
}

// generateAdjustmentNo 生成唯一补退单号（冲突重试）。
func (s *AdjustmentService) generateAdjustmentNo() (string, error) {
	no := ""
	for i := 0; i < 5; i++ {
		seq, _ := s.adjRepo.Count()
		candidate := util.AdjustmentNo(seq + 1 + int64(i))
		exists, err := s.adjRepo.ExistsByNo(candidate)
		if err != nil {
			return "", util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("check adjustment no: %w", err))
		}
		if !exists {
			no = candidate
			break
		}
	}
	if no == "" {
		return "", util.InternalError("差额补退单号（SettlementAdjustment.adjustment_no）生成冲突", errors.New("adjustment no conflict"))
	}
	return no, nil
}

// roundMoney 四舍五入到分（对正负值均正确，避免浮点表示导致 100.00 变为 99.99）。
func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}
