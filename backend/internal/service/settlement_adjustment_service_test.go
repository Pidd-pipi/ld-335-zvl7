package service

import (
	"context"
	"sync"
	"testing"

	"github.com/blueship581/gbinsureapi/internal/constants"
	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/repository"
	"github.com/blueship581/gbinsureapi/internal/util"
	"gorm.io/gorm"
)

// newAdjustmentService 构造差额补退服务与所需仓储（SQLite 内存库）。
func newAdjustmentService(t *testing.T) (svc *SettlementAdjustmentService, db *gorm.DB) {
	t.Helper()
	gdb := newTestDB(t)
	orderRepo := repository.NewSettlementOrderRepository(gdb)
	adjustRepo := repository.NewSettlementAdjustmentRepository(gdb)
	entryRepo := repository.NewSettlementAccountEntryRepository(gdb)
	return NewSettlementAdjustmentService(gdb, orderRepo, adjustRepo, entryRepo, testLogger()), gdb
}

// seedSettledOrder 直接落库一条已结算订单，返回订单（默认医保支付额 800）。
func seedSettledOrder(t *testing.T, db *gorm.DB, clientID, personID uint, insurancePay float64) *model.SettlementOrder {
	t.Helper()
	order := &model.SettlementOrder{
		SettlementNo: util.SettlementNo(1), BatchID: 1, InsuredPersonID: personID,
		PresettlementID: 1, ClientID: clientID, Status: constants.SettlementSettled,
		TotalAmount: 1000, InsurancePayAmount: insurancePay,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatalf("create settled order: %v", err)
	}
	return order
}

func TestAdjustmentService_Apply(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	adj, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 700, "医保局核减多报费用")
	if err != nil {
		t.Fatalf("ApplyAdjustment() error = %v", err)
	}
	if adj.Status != constants.AdjustmentPendingReview {
		t.Fatalf("status = %s, want pending_review", adj.Status)
	}
	if adj.DifferenceAmount != -100 {
		t.Fatalf("difference = %v, want -100", adj.DifferenceAmount)
	}
	if adj.AdjustmentNo == "" {
		t.Fatal("adjustment_no should be generated")
	}
}

func TestAdjustmentService_ApplyInvalidTarget(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	// 目标金额小于零拒绝
	if _, err := svc.ApplyAdjustment(ctx, order.SettlementNo, -1, "neg"); err == nil {
		t.Fatal("expected error for negative target")
	}
	// 目标金额等于原支付额拒绝（不小于）
	if _, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 800, "eq"); err == nil {
		t.Fatal("expected error for target == original")
	}
	// 目标金额大于原支付额拒绝
	if _, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 900, "gt"); err == nil {
		t.Fatal("expected error for target > original")
	}
	// 0 元合法（全额冲减）
	adj, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 0, "全额冲减")
	if err != nil {
		t.Fatalf("target=0 should be allowed: %v", err)
	}
	if adj.TargetPayAmount != 0 || adj.DifferenceAmount != -800 {
		t.Fatalf("target=0 adjustment invalid: %+v", adj)
	}
}

func TestAdjustmentService_ApplyOrderNotFoundAndState(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	ctx := context.Background()

	// 结算单不存在
	if _, err := svc.ApplyAdjustment(ctx, "S20990101000001", 100, "x"); err == nil {
		t.Fatal("expected not found error")
	}

	// 已冲正订单不可申请
	reversed := seedSettledOrder(t, db, clientID, personID, 800)
	reversed.SettlementNo = util.SettlementNo(2)
	reversed.Status = constants.SettlementReversed
	if err := db.Save(reversed).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyAdjustment(ctx, reversed.SettlementNo, 700, "x"); err == nil {
		t.Fatal("expected error applying on reversed order")
	}
}

func TestAdjustmentService_ApplyDuplicatePending(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	if _, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 700, "first"); err != nil {
		t.Fatalf("first apply error = %v", err)
	}
	// 待复核期间重复提交拒绝
	if _, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 600, "dup"); err == nil {
		t.Fatal("expected conflict on duplicate pending adjustment")
	}
}

func TestAdjustmentService_ReviewApprove(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	adj, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 700, "核减100")
	if err != nil {
		t.Fatalf("apply error = %v", err)
	}
	approved, err := svc.ReviewAdjustment(ctx, adj.AdjustmentNo, true, "")
	if err != nil {
		t.Fatalf("review approve error = %v", err)
	}
	if approved.Status != constants.AdjustmentApproved || approved.ReviewedAt == nil {
		t.Fatalf("approved adjustment invalid: %+v", approved)
	}
	// 订单标记已补退
	var refreshed model.SettlementOrder
	if err := db.First(&refreshed, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != constants.SettlementAdjusted {
		t.Fatalf("order status = %s, want adjusted", refreshed.Status)
	}
	// 已补退订单不允许再次申请
	if _, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 600, "again"); err == nil {
		t.Fatal("expected error applying on adjusted order")
	}
	// 回读负向流水
	entries, err := repository.NewSettlementAccountEntryRepository(db).ListByAdjustmentID(adj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Direction != constants.AdjustmentDirectionNegative {
		t.Fatalf("direction = %s, want negative", e.Direction)
	}
	if e.Amount != -100 {
		t.Fatalf("entry amount = %v, want -100", e.Amount)
	}
	if e.EntryNo == "" {
		t.Fatal("entry_no should be generated")
	}
	// 详情回读包含流水
	detail, err := svc.GetAdjustment(ctx, adj.AdjustmentNo)
	if err != nil {
		t.Fatalf("get adjustment error = %v", err)
	}
	if len(detail.Entries) != 1 || detail.Entries[0].Amount != -100 {
		t.Fatalf("detail entries invalid: %+v", detail.Entries)
	}
}

func TestAdjustmentService_ReviewRejectThenReapply(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	adj, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 700, "first")
	if err != nil {
		t.Fatalf("apply error = %v", err)
	}
	// 驳回必须给原因
	if _, err := svc.ReviewAdjustment(ctx, adj.AdjustmentNo, false, "  "); err == nil {
		t.Fatal("expected error when rejecting without reason")
	}
	rejected, err := svc.ReviewAdjustment(ctx, adj.AdjustmentNo, false, "凭证不完整")
	if err != nil {
		t.Fatalf("review reject error = %v", err)
	}
	if rejected.Status != constants.AdjustmentRejected || rejected.RejectReason != "凭证不完整" {
		t.Fatalf("rejected adjustment invalid: %+v", rejected)
	}
	// 订单保持已结算
	var ord model.SettlementOrder
	if err := db.First(&ord, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ord.Status != constants.SettlementSettled {
		t.Fatalf("order status = %s, want settled after reject", ord.Status)
	}
	// 驳回后可再次申请
	adj2, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 650, "补充凭证后再申请")
	if err != nil {
		t.Fatalf("reapply after reject error = %v", err)
	}
	if adj2.ID == adj.ID {
		t.Fatal("reapplication should create a new adjustment record")
	}
	// 历史记录保留两条
	items, err := svc.ListOrderAdjustments(ctx, order.SettlementNo)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("order adjustments = %d, want 2 (rejected + pending)", len(items))
	}
}

func TestAdjustmentService_ReviewOnlyOnce(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	adj, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 700, "x")
	if err != nil {
		t.Fatalf("apply error = %v", err)
	}
	if _, err := svc.ReviewAdjustment(ctx, adj.AdjustmentNo, true, ""); err != nil {
		t.Fatalf("first review error = %v", err)
	}
	// 重复复核（通过）失败，且不改动订单/账目
	if _, err := svc.ReviewAdjustment(ctx, adj.AdjustmentNo, true, ""); err == nil {
		t.Fatal("expected conflict on duplicate approve")
	}
	// 已通过再驳回也失败
	if _, err := svc.ReviewAdjustment(ctx, adj.AdjustmentNo, false, "迟来的驳回"); err == nil {
		t.Fatal("expected conflict reviewing an approved adjustment")
	}
	// 账目只有一条负向流水
	entries, _ := repository.NewSettlementAccountEntryRepository(db).ListByAdjustmentID(adj.ID)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1 (failed review must not write ledger)", len(entries))
	}
}

func TestAdjustmentService_ConcurrentReview(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	adj, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 700, "x")
	if err != nil {
		t.Fatalf("apply error = %v", err)
	}
	// 多个 goroutine 同时复核，仅允许一次成功
	const n = 8
	var wg sync.WaitGroup
	var okCount, failCount int64
	var mu sync.Mutex
	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.ReviewAdjustment(ctx, adj.AdjustmentNo, true, "")
			mu.Lock()
			if err == nil {
				okCount++
			} else {
				failCount++
			}
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	if okCount != 1 || failCount != n-1 {
		t.Fatalf("concurrent review ok=%d fail=%d, want ok=1 fail=%d", okCount, failCount, n-1)
	}
	// 订单只补退一次，账目仅一条负向流水
	var ord model.SettlementOrder
	if err := db.First(&ord, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ord.Status != constants.SettlementAdjusted {
		t.Fatalf("order status = %s, want adjusted", ord.Status)
	}
	entries, _ := repository.NewSettlementAccountEntryRepository(db).ListByAdjustmentID(adj.ID)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want exactly 1", len(entries))
	}
}

func TestAdjustmentService_ConcurrentApply(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	// 多个 goroutine 同时对同一订单提交，仅允许一条待复核记录
	const n = 8
	var wg sync.WaitGroup
	var okCount, failCount int64
	var mu sync.Mutex
	wg.Add(n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 700, "race")
			mu.Lock()
			if err == nil {
				okCount++
			} else {
				failCount++
			}
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	if okCount != 1 || failCount != n-1 {
		t.Fatalf("concurrent apply ok=%d fail=%d, want ok=1 fail=%d", okCount, failCount, n-1)
	}
	var cnt int64
	db.Model(&model.SettlementAdjustment{}).
		Where("settlement_order_id = ? AND status = ?", order.ID, constants.AdjustmentPendingReview).
		Count(&cnt)
	if cnt != 1 {
		t.Fatalf("pending adjustments = %d, want 1", cnt)
	}
	// 订单未被改动
	var ord model.SettlementOrder
	if err := db.First(&ord, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ord.Status != constants.SettlementSettled {
		t.Fatalf("order status = %s, apply must not change order", ord.Status)
	}
}

func TestAdjustmentService_ListAndDetail(t *testing.T) {
	svc, db := newAdjustmentService(t)
	clientID, personID := seedData(t, db)
	order := seedSettledOrder(t, db, clientID, personID, 800)
	ctx := context.Background()

	adj, err := svc.ApplyAdjustment(ctx, order.SettlementNo, 700, "x")
	if err != nil {
		t.Fatalf("apply error = %v", err)
	}
	items, total, err := svc.ListAdjustments(ctx, clientID, constants.AdjustmentPendingReview, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("list total=%d len=%d, want 1/1", total, len(items))
	}
	// 按单号回读
	detail, err := svc.GetAdjustment(ctx, adj.AdjustmentNo)
	if err != nil {
		t.Fatalf("get detail error = %v", err)
	}
	if detail.SettlementNo != order.SettlementNo || detail.OriginalPayAmount != 800 {
		t.Fatalf("detail invalid: %+v", detail)
	}
	// 不存在的差额单
	if _, err := svc.GetAdjustment(ctx, "A20990101000001"); err == nil {
		t.Fatal("expected not found for missing adjustment")
	}
}
