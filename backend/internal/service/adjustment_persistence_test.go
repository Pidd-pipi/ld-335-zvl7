package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueship581/gbinsureapi/internal/constants"
	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/repository"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestAdjustmentService_PersistsAcrossRestart 模拟服务重启：提交+复核通过落库后，
// 用新的 GORM 句柄重新打开同一数据库，已补退订单与负向账目必须仍可回读且一致。
func TestAdjustmentService_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	svc, no, original := seedSettledOrder(t, ctx)

	adj, err := svc.Submit(ctx, no, 1, original-120, "需持久化的差额补退")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := svc.Review(ctx, adj.AdjustmentNo, 2, true, "通过"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// 重新装配一个全新的服务句柄，复用原内存库在当前测试中等价于"进程重启后重新查询"
	// （GORM 不缓存业务状态，所有状态均来自数据库）。
	freshSvc := NewAdjustmentService(
		svc.db,
		repository.NewSettlementOrderRepository(svc.db),
		repository.NewSettlementAdjustmentRepository(svc.db),
		repository.NewAdjustmentLedgerRepository(svc.db),
		testLogger(),
	)
	detail, err := freshSvc.GetByNo(ctx, adj.AdjustmentNo)
	if err != nil {
		t.Fatalf("reload adjustment: %v", err)
	}
	if detail.Status != constants.AdjustmentApproved || detail.ReviewerClientID != 2 {
		t.Fatalf("reloaded adjustment wrong: %+v", detail)
	}
	if detail.Ledger == nil || detail.Ledger.Amount != -120 || detail.Ledger.Direction != constants.LedgerDirectionNegative {
		t.Fatalf("reloaded ledger wrong: %+v", detail.Ledger)
	}
	order, err := freshSvc.orderRepo.FindByNo(no)
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != constants.SettlementAdjusted {
		t.Fatalf("reloaded order status=%s, want adjusted", order.Status)
	}
}

// TestAdjustmentService_FileDBRestart 使用文件型 SQLite 真实模拟进程重启，验证落库一致。
func TestAdjustmentService_FileDBRestart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "restart.db")
	openFileDB := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
		if err != nil {
			t.Fatalf("open file db: %v", err)
		}
		if err := db.AutoMigrate(
			&model.SettlementOrder{}, &model.SettlementAdjustment{}, &model.AdjustmentLedger{},
		); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_adjustment_order_pending
			ON settlement_adjustments(settlement_order_id) WHERE status = 'pending_review'`).Error; err != nil {
			t.Fatalf("index: %v", err)
		}
		return db
	}

	ctx := context.Background()
	// 第一次"进程"：建单 + 提交差额单（保持待复核）
	db1 := openFileDB()
	now := time.Now()
	order := &model.SettlementOrder{SettlementNo: "SRESTART1", Status: constants.SettlementSettled, InsurancePayAmount: 500, SettledAt: &now}
	if err := db1.Create(order).Error; err != nil {
		t.Fatal(err)
	}
	svc1 := NewAdjustmentService(db1, repository.NewSettlementOrderRepository(db1),
		repository.NewSettlementAdjustmentRepository(db1), repository.NewAdjustmentLedgerRepository(db1), testLogger())
	adj, err := svc1.Submit(ctx, "SRESTART1", 7, 380, "重启前提交")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	sqlDB1, _ := db1.DB()
	_ = sqlDB1.Close()

	// 第二次"进程"：重新打开文件库，待复核单仍在并可复核通过
	db2 := openFileDB()
	svc2 := NewAdjustmentService(db2, repository.NewSettlementOrderRepository(db2),
		repository.NewSettlementAdjustmentRepository(db2), repository.NewAdjustmentLedgerRepository(db2), testLogger())
	pending, err := svc2.GetByNo(ctx, adj.AdjustmentNo)
	if err != nil {
		t.Fatalf("reload pending after restart: %v", err)
	}
	if pending.Status != constants.AdjustmentPendingReview || pending.Reason != "重启前提交" {
		t.Fatalf("pending not preserved across restart: %+v", pending)
	}
	approved, err := svc2.Review(ctx, adj.AdjustmentNo, 9, true, "重启后复核")
	if err != nil {
		t.Fatalf("review after restart: %v", err)
	}
	if approved.Ledger == nil || approved.Ledger.Amount != -120 {
		t.Fatalf("ledger after restart wrong: %+v", approved.Ledger)
	}
	sqlDB2, _ := db2.DB()
	_ = sqlDB2.Close()

	// 第三次"进程"：订单已补退、负向账目持久存在
	db3 := openFileDB()
	var o3 model.SettlementOrder
	if err := db3.Where("settlement_no = ?", "SRESTART1").First(&o3).Error; err != nil {
		t.Fatal(err)
	}
	if o3.Status != constants.SettlementAdjusted {
		t.Fatalf("order status after restart=%s, want adjusted", o3.Status)
	}
	var ledgers int64
	db3.Model(&model.AdjustmentLedger{}).Where("adjustment_id = ?", adj.ID).Count(&ledgers)
	if ledgers != 1 {
		t.Fatalf("ledger count after restart=%d, want 1", ledgers)
	}
}
