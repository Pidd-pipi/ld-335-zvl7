package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/blueship581/gbinsureapi/internal/constants"
	"github.com/blueship581/gbinsureapi/internal/dto"
	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/repository"
	"github.com/blueship581/gbinsureapi/internal/util"
	"gorm.io/gorm"
)

// SettlementAdjustmentService 结算差额补退服务：申请（生成待复核单）、复核（通过补退/驳回）、查询。
// 申请与复核按结算单分片互斥，配合数据库部分唯一索引与状态 CAS，保证并发下只成功一次。
type SettlementAdjustmentService struct {
	db         *gorm.DB
	orderRepo  *repository.SettlementOrderRepository
	adjustRepo *repository.SettlementAdjustmentRepository
	entryRepo  *repository.SettlementAccountEntryRepository
	log        *slog.Logger
	locks      *stripedLocks
}

// NewSettlementAdjustmentService 构造差额补退服务。
func NewSettlementAdjustmentService(db *gorm.DB, orderRepo *repository.SettlementOrderRepository, adjustRepo *repository.SettlementAdjustmentRepository, entryRepo *repository.SettlementAccountEntryRepository, log *slog.Logger) *SettlementAdjustmentService {
	return &SettlementAdjustmentService{
		db: db, orderRepo: orderRepo, adjustRepo: adjustRepo, entryRepo: entryRepo,
		log: log, locks: newStripedLocks(64),
	}
}

// stripedLocks 按结算单 ID 分片的互斥锁：同一订单的申请/复核串行，不同订单并行。
type stripedLocks struct {
	shards []sync.Mutex
}

func newStripedLocks(n uint) *stripedLocks { return &stripedLocks{shards: make([]sync.Mutex, n)} }

func (l *stripedLocks) lock(orderID uint) func() {
	m := &l.shards[orderID%uint(len(l.shards))]
	m.Lock()
	return m.Unlock
}

// ApplyAdjustment 已结算订单提交目标医保支付额与原因，生成待复核差额单。
// 目标金额小于零或不小于原支付额时拒绝；同一订单只允许一条待复核记录；任何失败不改订单与账目。
func (s *SettlementAdjustmentService) ApplyAdjustment(ctx context.Context, settlementNo string, target float64, reason string) (*model.SettlementAdjustment, error) {
	if target < 0 {
		return nil, util.NewAppError(constants.CodeAdjustAmount, 400, constants.MsgAdjustAmountInvalid,
			fmt.Errorf("SettlementAdjustment[no=%s] target insurance pay %v is negative", settlementNo, target))
	}
	adj := &model.SettlementAdjustment{}
	// 分片锁覆盖同一订单的全部数据库操作，避免与并发申请/复核争用连接或互相穿插。
	err := s.db.Transaction(func(tx *gorm.DB) error {
		ord, err := s.orderRepo.FindByNoTx(tx, settlementNo)
		if err != nil {
			if errors.Is(err, util.ErrNotFound) {
				return util.NotFoundError(constants.MsgSettlementNotFound, err)
			}
			return err
		}
		unlock := s.locks.lock(ord.ID)
		defer unlock()
		if err := s.orderRepo.TouchRow(tx, ord.ID); err != nil {
			return util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("lock order row: %w", err))
		}
		if err := tx.First(ord, ord.ID).Error; err != nil {
			return util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("reload order: %w", err))
		}
		if ord.Status != constants.SettlementSettled {
			return util.NewAppError(constants.CodeAdjustOrderState, 409, constants.MsgAdjustOrderNotSettled,
				fmt.Errorf("SettlementOrder[no=%s] status=%s, only settled can apply", settlementNo, ord.Status))
		}
		if target < 0 || target >= ord.InsurancePayAmount {
			return util.NewAppError(constants.CodeAdjustAmount, 400, constants.MsgAdjustAmountInvalid,
				fmt.Errorf("SettlementAdjustment target %v out of range [0,%v)", target, ord.InsurancePayAmount))
		}
		exists, err := s.adjustRepo.ExistsPendingByOrderID(tx, ord.ID)
		if err != nil {
			return util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("check pending adjustment: %w", err))
		}
		if exists {
			return util.ConflictError(constants.MsgAdjustmentExists,
				fmt.Errorf("SettlementOrder[id=%d] already has a pending_review SettlementAdjustment", ord.ID))
		}
		no, err := s.genAdjustmentNoTx(tx)
		if err != nil {
			return err
		}
		*adj = model.SettlementAdjustment{
			AdjustmentNo: no, SettlementOrderID: ord.ID, SettlementNo: ord.SettlementNo, ClientID: ord.ClientID,
			Status: constants.AdjustmentPendingReview, OriginalPayAmount: ord.InsurancePayAmount,
			TargetPayAmount: target, DifferenceAmount: roundMoney(target - ord.InsurancePayAmount),
			ApplyReason: reason,
		}
		if err := s.adjustRepo.CreateTx(tx, adj); err != nil {
			if isDuplicateKeyErr(err) {
				return util.ConflictError(constants.MsgAdjustmentExists,
					fmt.Errorf("duplicate pending_review SettlementAdjustment for order %d: %w", ord.ID, err))
			}
			return util.LogError(s.log, constants.LOG_ADJUSTMENT_SUBMIT_FAILED, fmt.Errorf("create adjustment: %w", err))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.log.InfoContext(ctx, constants.LOG_ADJUSTMENT_SUBMITTED,
		"adjustment_no", adj.AdjustmentNo, "settlement_no", settlementNo, "target", target, "difference", adj.DifferenceAmount)
	return adj, nil
}

// ReviewAdjustment 复核差额单。approved=true 通过（订单标记已补退并生成负向流水冲减差额）；
// approved=false 驳回（保留原因，可再次申请）。并发/重复复核只能成功一次，失败不改订单与账目。
func (s *SettlementAdjustmentService) ReviewAdjustment(ctx context.Context, adjustmentNo string, approved bool, reason string) (*model.SettlementAdjustment, error) {
	if !approved && strings.TrimSpace(reason) == "" {
		return nil, util.BadRequest(constants.MsgAdjustReviewInvalid,
			errors.New("reject reason is required when approved=false"))
	}
	result := &model.SettlementAdjustment{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		adj, err := s.adjustRepo.FindByNoTx(tx, adjustmentNo)
		if err != nil {
			if errors.Is(err, util.ErrNotFound) {
				return util.NotFoundError(constants.MsgAdjustmentNotFound, err)
			}
			return err
		}
		unlock := s.locks.lock(adj.SettlementOrderID)
		defer unlock()
		// 锁定结算单行，串行化同一订单的申请与复核。
		if err := s.orderRepo.TouchRow(tx, adj.SettlementOrderID); err != nil {
			return util.LogError(s.log, constants.LOG_ADJUSTMENT_REVIEW_FAILED, fmt.Errorf("lock order row: %w", err))
		}
		// 事务内回读差额单最新状态。
		if err := tx.First(adj, adj.ID).Error; err != nil {
			return util.LogError(s.log, constants.LOG_ADJUSTMENT_REVIEW_FAILED, fmt.Errorf("reload adjustment: %w", err))
		}
		if adj.Status != constants.AdjustmentPendingReview {
			return util.ConflictError(constants.MsgAdjustmentNotPending,
				fmt.Errorf("SettlementAdjustment[no=%s] status=%s, expect pending_review", adjustmentNo, adj.Status))
		}
		now := time.Now()
		fields := map[string]any{"reviewed_at": &now}
		if approved {
			// 订单仅在仍为 settled 时才能 CAS 为 adjusted；被并发冲正/补退则整笔复核失败回滚。
			n, err := s.orderRepo.CompareAndSetStatus(tx, adj.SettlementOrderID, constants.SettlementSettled, constants.SettlementAdjusted)
			if err != nil {
				return util.LogError(s.log, constants.LOG_ADJUSTMENT_REVIEW_FAILED, fmt.Errorf("cas order status: %w", err))
			}
			if n == 0 {
				return util.ConflictError(constants.MsgAdjustOrderNotSettled,
					fmt.Errorf("SettlementOrder[id=%d] no longer settled", adj.SettlementOrderID))
			}
			fields["status"] = constants.AdjustmentApproved
			n, err = s.adjustRepo.CompareAndSetReview(tx, adj.ID, fields)
			if err != nil {
				return util.LogError(s.log, constants.LOG_ADJUSTMENT_REVIEW_FAILED, fmt.Errorf("cas adjustment approved: %w", err))
			}
			if n == 0 {
				return util.ConflictError(constants.MsgAdjustmentNotPending,
					fmt.Errorf("SettlementAdjustment[id=%d] reviewed concurrently", adj.ID))
			}
			entryNo, err := s.genEntryNoTx(tx)
			if err != nil {
				return err
			}
			entry := &model.SettlementAccountEntry{
				EntryNo: entryNo, SettlementOrderID: adj.SettlementOrderID, SettlementNo: adj.SettlementNo,
				AdjustmentID: adj.ID, AdjustmentNo: adj.AdjustmentNo, ClientID: adj.ClientID,
				Direction: constants.AdjustmentDirectionNegative, Amount: adj.DifferenceAmount, Reason: adj.ApplyReason,
			}
			if err := s.entryRepo.CreateTx(tx, entry); err != nil {
				return util.LogError(s.log, constants.LOG_ADJUSTMENT_REVIEW_FAILED, fmt.Errorf("create negative entry: %w", err))
			}
			adj.Status = constants.AdjustmentApproved
			adj.ReviewedAt = &now
			*result = *adj
			return nil
		}
		// 驳回：仅更新差额单，保留驳回原因；订单状态不变，允许后续再次申请。
		fields["status"] = constants.AdjustmentRejected
		fields["reject_reason"] = reason
		n, err := s.adjustRepo.CompareAndSetReview(tx, adj.ID, fields)
		if err != nil {
			return util.LogError(s.log, constants.LOG_ADJUSTMENT_REVIEW_FAILED, fmt.Errorf("cas adjustment rejected: %w", err))
		}
		if n == 0 {
			return util.ConflictError(constants.MsgAdjustmentNotPending,
				fmt.Errorf("SettlementAdjustment[id=%d] reviewed concurrently", adj.ID))
		}
		adj.Status = constants.AdjustmentRejected
		adj.RejectReason = reason
		adj.ReviewedAt = &now
		*result = *adj
		return nil
	})
	if err != nil {
		return nil, err
	}
	if approved {
		s.log.InfoContext(ctx, constants.LOG_ADJUSTMENT_APPROVED,
			"adjustment_no", adjustmentNo, "settlement_no", result.SettlementNo, "difference", result.DifferenceAmount)
	} else {
		s.log.InfoContext(ctx, constants.LOG_ADJUSTMENT_REJECTED, "adjustment_no", adjustmentNo, "reason", reason)
	}
	return result, nil
}

// genAdjustmentNoTx 在事务内生成唯一差额补退单号（冲突重试）。
func (s *SettlementAdjustmentService) genAdjustmentNoTx(tx *gorm.DB) (string, error) {
	for i := 0; i < 5; i++ {
		var seq int64
		if err := tx.Model(&model.SettlementAdjustment{}).Count(&seq).Error; err != nil {
			return "", err
		}
		candidate := util.AdjustmentNo(seq + 1 + int64(i))
		var cnt int64
		if err := tx.Model(&model.SettlementAdjustment{}).Where("adjustment_no = ?", candidate).Count(&cnt).Error; err != nil {
			return "", err
		}
		if cnt == 0 {
			return candidate, nil
		}
	}
	return "", util.InternalError(constants.MsgSettlementNoUnique, errors.New("adjustment no conflict"))
}

// genEntryNoTx 在事务内生成唯一账目流水号（冲突重试）。
func (s *SettlementAdjustmentService) genEntryNoTx(tx *gorm.DB) (string, error) {
	for i := 0; i < 5; i++ {
		var seq int64
		if err := tx.Model(&model.SettlementAccountEntry{}).Count(&seq).Error; err != nil {
			return "", err
		}
		candidate := util.AccountEntryNo(seq + 1 + int64(i))
		var cnt int64
		if err := tx.Model(&model.SettlementAccountEntry{}).Where("entry_no = ?", candidate).Count(&cnt).Error; err != nil {
			return "", err
		}
		if cnt == 0 {
			return candidate, nil
		}
	}
	return "", util.InternalError(constants.MsgSettlementNoUnique, errors.New("account entry no conflict"))
}

// GetAdjustment 查询差额单详情（含生成的负向账目流水，供回读）。
func (s *SettlementAdjustmentService) GetAdjustment(ctx context.Context, adjustmentNo string) (*dto.AdjustmentDetailResponse, error) {
	adj, err := s.adjustRepo.FindByNo(adjustmentNo)
	if err != nil {
		if errors.Is(err, util.ErrNotFound) {
			return nil, util.NotFoundError(constants.MsgAdjustmentNotFound, err)
		}
		return nil, err
	}
	entries, err := s.entryRepo.ListByAdjustmentID(adj.ID)
	if err != nil {
		return nil, err
	}
	return &dto.AdjustmentDetailResponse{SettlementAdjustment: *adj, Entries: entries}, nil
}

// ListAdjustments 分页查询差额补退单。
func (s *SettlementAdjustmentService) ListAdjustments(ctx context.Context, clientID uint, status string, page, pageSize int) ([]model.SettlementAdjustment, int64, error) {
	return s.adjustRepo.List(clientID, status, page, pageSize)
}

// ListOrderAdjustments 查询某结算单的全部差额补退单（驳回后再次申请的历史）。
func (s *SettlementAdjustmentService) ListOrderAdjustments(ctx context.Context, settlementNo string) ([]model.SettlementAdjustment, error) {
	order, err := s.orderRepo.FindByNo(settlementNo)
	if err != nil {
		if errors.Is(err, util.ErrNotFound) {
			return nil, util.NotFoundError(constants.MsgSettlementNotFound, err)
		}
		return nil, err
	}
	return s.adjustRepo.ListByOrderID(order.ID)
}

// isDuplicateKeyErr 识别 PostgreSQL（23505）与 SQLite（UNIQUE constraint）唯一约束冲突。
func isDuplicateKeyErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(strings.ToUpper(msg), "UNIQUE CONSTRAINT")
}

// roundMoney 金额保留两位小数（math.Round 对负数四舍五入，规避浮点误差导致的 -99.99）。
func roundMoney(v float64) float64 {
	return math.Round(v*100) / 100
}
