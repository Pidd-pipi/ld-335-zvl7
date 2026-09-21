package service

import (
	"context"
	"sync"
	"testing"

	"github.com/blueship581/gbinsureapi/internal/constants"
	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/repository"
	"github.com/blueship581/gbinsureapi/internal/util"
)

// seedSettledOrder 构造一条已结算订单，返回（服务, 结算单号, 原医保支付额）。
func seedSettledOrder(t *testing.T, ctx context.Context) (*AdjustmentService, string, float64) {
	t.Helper()
	db := newTestDB(t)
	clientID, personID := seedData(t, db)
	insurance := NewInsuranceService(repository.NewInsuredPersonRepository(db), testLogger())
	batchRepo := repository.NewUploadBatchRepository(db)
	feeRepo := repository.NewFeeItemRepository(db)
	batch := model.UploadBatch{BatchNo: util.BatchNo(1), ClientID: clientID, InsuredPersonID: personID, UploadStatus: constants.UploadValidated, TotalAmount: 1000, ItemCount: 2}
	if err := db.Create(&batch).Error; err != nil {
		t.Fatal(err)
	}
	if err := feeRepo.CreateBatch([]model.FeeItem{
		{BatchID: batch.ID, ItemCode: "DRUG01", ItemName: "阿莫西林", ItemType: constants.FeeItemDrug, Amount: 600, MedicalCategory: constants.MedicalCategoryClassA},
		{BatchID: batch.ID, ItemCode: "EXAM01", ItemName: "血常规", ItemType: constants.FeeItemExam, Amount: 400, MedicalCategory: constants.MedicalCategoryClassA},
	}); err != nil {
		t.Fatal(err)
	}
	settleSvc := NewSettlementService(
		repository.NewPresettlementRepository(db), repository.NewSettlementOrderRepository(db),
		feeRepo, batchRepo, insurance, util.NewSettlementCalculator(), testLogger(),
	)
	preset, err := settleSvc.CalculatePresettlement(ctx, batch.ID)
	if err != nil {
		t.Fatalf("CalculatePresettlement: %v", err)
	}
	order, err := settleSvc.SubmitSettlement(ctx, clientID, preset.ID)
	if err != nil {
		t.Fatalf("SubmitSettlement: %v", err)
	}
	adjSvc := NewAdjustmentService(
		db,
		repository.NewSettlementOrderRepository(db),
		repository.NewSettlementAdjustmentRepository(db),
		repository.NewAdjustmentLedgerRepository(db),
		testLogger(),
	)
	return adjSvc, order.SettlementNo, order.InsurancePayAmount
}

func TestAdjustmentService_SubmitValidation(t *testing.T) {
	ctx := context.Background()
	svc, no, original := seedSettledOrder(t, ctx)

	cases := []struct {
		name   string
		target float64
	}{
		{"目标金额为负", -1},
		{"目标金额等于原支付额", original},
		{"目标金额大于原支付额", original + 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Submit(ctx, no, 1, tc.target, "医院申报差额"); err == nil {
				t.Fatalf("expected rejection for target=%.2f", tc.target)
			}
		})
	}

	// 结算单不存在
	if _, err := svc.Submit(ctx, "S-NOT-EXIST", 1, original-10, "原因"); err == nil {
		t.Fatal("expected not found for missing order")
	}
}

func TestAdjustmentService_DuplicatePendingRejected(t *testing.T) {
	ctx := context.Background()
	svc, no, original := seedSettledOrder(t, ctx)

	first, err := svc.Submit(ctx, no, 1, original-50, "首次申报")
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if first.Status != constants.AdjustmentPendingReview {
		t.Fatalf("status=%s, want pending_review", first.Status)
	}
	// 同一订单再次提交待复核记录应被拒绝
	if _, err := svc.Submit(ctx, no, 1, original-30, "重复申报"); err == nil {
		t.Fatal("expected conflict on duplicate pending adjustment")
	}
}

func TestAdjustmentService_ApproveFlow(t *testing.T) {
	ctx := context.Background()
	svc, no, original := seedSettledOrder(t, ctx)

	adj, err := svc.Submit(ctx, no, 1, original-100, "医保局核定下调")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	approved, err := svc.Review(ctx, adj.AdjustmentNo, 2, true, "核定无误")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != constants.AdjustmentApproved {
		t.Fatalf("status=%s, want approved", approved.Status)
	}
	// 负向账目：金额为负且等于目标-原支付
	if approved.Ledger == nil {
		t.Fatal("expected negative ledger attached")
	}
	if approved.Ledger.Direction != constants.LedgerDirectionNegative {
		t.Fatalf("direction=%s, want negative", approved.Ledger.Direction)
	}
	if approved.Ledger.Amount >= 0 || approved.Ledger.Amount != -100 {
		t.Fatalf("ledger amount=%.2f, want -100", approved.Ledger.Amount)
	}
	// 订单标记已补退
	order, err := svc.orderRepo.FindByNo(no)
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != constants.SettlementAdjusted {
		t.Fatalf("order status=%s, want adjusted", order.Status)
	}
	// 已补退订单不允许再次申请
	if _, err := svc.Submit(ctx, no, 1, original-10, "再申请"); err == nil {
		t.Fatal("expected rejection: adjusted order cannot reapply")
	}
	// 重复复核已通过单只能成功一次
	if _, err := svc.Review(ctx, adj.AdjustmentNo, 2, true, ""); err == nil {
		t.Fatal("expected conflict on reviewing approved adjustment")
	}
	// 查询回读携带账目
	detail, err := svc.GetByNo(ctx, adj.AdjustmentNo)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if detail.Ledger == nil || detail.Ledger.Amount != -100 {
		t.Fatalf("detail ledger missing/wrong: %+v", detail.Ledger)
	}
	list, err := svc.ListBySettlementNo(ctx, no)
	if err != nil || len(list) != 1 || list[0].Ledger == nil {
		t.Fatalf("list by order wrong: len=%d err=%v", len(list), err)
	}
}

func TestAdjustmentService_RejectThenReapply(t *testing.T) {
	ctx := context.Background()
	svc, no, original := seedSettledOrder(t, ctx)

	adj, err := svc.Submit(ctx, no, 1, original-80, "首次申报")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// 驳回缺少复核意见应拒绝
	if _, err := svc.Review(ctx, adj.AdjustmentNo, 2, false, ""); err == nil {
		t.Fatal("expected reject to require review_remark")
	}
	rejected, err := svc.Review(ctx, adj.AdjustmentNo, 2, false, "材料不全")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejected.Status != constants.AdjustmentRejected {
		t.Fatalf("status=%s, want rejected", rejected.Status)
	}
	// 驳回保留原因，且不生成账目
	if rejected.Ledger != nil {
		t.Fatal("rejected adjustment must not have ledger")
	}
	order, _ := svc.orderRepo.FindByNo(no)
	if order.Status != constants.SettlementSettled {
		t.Fatalf("order status=%s, want still settled", order.Status)
	}
	// 驳回后可再次申请
	reapplied, err := svc.Submit(ctx, no, 1, original-60, "补充材料后再次申报")
	if err != nil {
		t.Fatalf("reapply after reject: %v", err)
	}
	if reapplied.Status != constants.AdjustmentPendingReview {
		t.Fatalf("reapply status=%s, want pending_review", reapplied.Status)
	}
	// 再次通过
	approved2, err := svc.Review(ctx, reapplied.AdjustmentNo, 2, true, "通过")
	if err != nil || approved2.Ledger == nil || approved2.Ledger.Amount != -60 {
		t.Fatalf("approve reapplied wrong: %+v err=%v", approved2, err)
	}
}

func TestAdjustmentService_ConcurrentApproveOnlyOnce(t *testing.T) {
	ctx := context.Background()
	svc, no, original := seedSettledOrder(t, ctx)
	adj, err := svc.Submit(ctx, no, 1, original-40, "并发复核")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, results[idx] = svc.Review(ctx, adj.AdjustmentNo, uint(2+idx), true, "并发通过")
		}(i)
	}
	close(start)
	wg.Wait()

	success, fail := 0, 0
	for _, e := range results {
		if e == nil {
			success++
		} else {
			fail++
		}
	}
	if success != 1 || fail != 1 {
		t.Fatalf("concurrent review: success=%d fail=%d, want exactly 1 success", success, fail)
	}
	// 仅一条负向账目，订单状态为已补退
	var ledgerCount int64
	if err := svc.db.Model(&model.AdjustmentLedger{}).Where("adjustment_id = ?", adj.ID).Count(&ledgerCount).Error; err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 1 {
		t.Fatalf("ledger count=%d, want exactly 1", ledgerCount)
	}
	order, _ := svc.orderRepo.FindByNo(no)
	if order.Status != constants.SettlementAdjusted {
		t.Fatalf("order status=%s, want adjusted", order.Status)
	}
	final, _ := svc.GetByNo(ctx, adj.AdjustmentNo)
	if final.Status != constants.AdjustmentApproved {
		t.Fatalf("final status=%s, want approved", final.Status)
	}
}

func TestAdjustmentService_ConcurrentSubmitOnlyOnePending(t *testing.T) {
	ctx := context.Background()
	svc, no, original := seedSettledOrder(t, ctx)

	var wg sync.WaitGroup
	const n = 5
	results := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, results[idx] = svc.Submit(ctx, no, 1, original-float64(10*idx+5), "并发提交")
		}(i)
	}
	close(start)
	wg.Wait()

	success := 0
	for _, e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent submit success=%d, want exactly 1 pending record", success)
	}
	var pendingCount int64
	if err := svc.db.Model(&model.SettlementAdjustment{}).
		Where("settlement_no = ? AND status = ?", no, constants.AdjustmentPendingReview).
		Count(&pendingCount).Error; err != nil {
		t.Fatal(err)
	}
	if pendingCount != 1 {
		t.Fatalf("pending count=%d, want exactly 1", pendingCount)
	}
}
