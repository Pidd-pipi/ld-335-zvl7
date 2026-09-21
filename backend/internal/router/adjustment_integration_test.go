package router

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blueship581/gbinsureapi/internal/config"
	"github.com/blueship581/gbinsureapi/internal/constants"
	"github.com/blueship581/gbinsureapi/internal/handler"
	"github.com/blueship581/gbinsureapi/internal/middleware"
	"github.com/blueship581/gbinsureapi/internal/model"
	"github.com/blueship581/gbinsureapi/internal/repository"
	"github.com/blueship581/gbinsureapi/internal/service"
	"github.com/blueship581/gbinsureapi/internal/util"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTestRouter(t *testing.T) (*gorm.DB, http.Handler, string, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&model.ApiClient{}, &model.InsuredPerson{}, &model.UploadBatch{}, &model.FeeItem{},
		&model.Presettlement{}, &model.SettlementOrder{}, &model.DailyReconciliation{}, &model.AuditLog{},
		&model.SettlementAdjustment{}, &model.SettlementAccountEntry{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Exec(model.PendingOrderUniqueIndexSQL).Error; err != nil {
		t.Fatalf("index: %v", err)
	}
	cfg := config.Config{
		JWTSecret: "test-jwt-secret-long", APIKeySecret: "test-api-key-secret-long",
		JWTExpireHours: 24, CORSOrigins: "http://localhost:19935",
	}
	log := util.NewLogger()
	clientRepo := repository.NewApiClientRepository(db)
	insuredRepo := repository.NewInsuredPersonRepository(db)
	batchRepo := repository.NewUploadBatchRepository(db)
	feeRepo := repository.NewFeeItemRepository(db)
	presetRepo := repository.NewPresettlementRepository(db)
	orderRepo := repository.NewSettlementOrderRepository(db)
	adjustRepo := repository.NewSettlementAdjustmentRepository(db)
	entryRepo := repository.NewSettlementAccountEntryRepository(db)
	recRepo := repository.NewDailyReconciliationRepository(db)
	auditRepo := repository.NewAuditLogRepository(db)

	calc := util.NewSettlementCalculator()
	clientSvc := service.NewApiClientService(clientRepo, cfg.APIKeySecret, cfg.JWTSecret, cfg.JWTExpireHours, log)
	insuranceSvc := service.NewInsuranceService(insuredRepo, log)
	feeSvc := service.NewFeeService(batchRepo, feeRepo, insuranceSvc, log)
	settlementSvc := service.NewSettlementService(presetRepo, orderRepo, feeRepo, batchRepo, insuranceSvc, calc, log)
	adjustmentSvc := service.NewSettlementAdjustmentService(db, orderRepo, adjustRepo, entryRepo, log)
	reconSvc := service.NewReconciliationService(orderRepo, recRepo, log)

	h := Handlers{
		Auth:          handler.NewAuthHandler(cfg, log),
		ApiClient:     handler.NewApiClientHandler(clientSvc, log),
		Insured:       handler.NewInsuredPersonHandler(insuranceSvc, log),
		UploadBatch:   handler.NewUploadBatchHandler(feeSvc, log),
		FeeItem:       handler.NewFeeItemHandler(feeSvc, log),
		Presettlement: handler.NewPresettlementHandler(settlementSvc, log),
		Settlement:    handler.NewSettlementOrderHandler(settlementSvc, log),
		Adjustment:    handler.NewSettlementAdjustmentHandler(adjustmentSvc, log),
		Recon:         handler.NewDailyReconciliationHandler(reconSvc, log),
	}
	r := New(cfg, log, h, clientSvc, auditRepo, middleware.NewRateLimiter())

	// 种子调用方
	client := model.ApiClient{
		Name: "测试HIS", ClientType: constants.ClientTypeHIS,
		APIKeyHash: util.HashAPIKey("ak_test_his", cfg.APIKeySecret),
		Role:       "settlement", Status: constants.ClientActive, RateLimitQPS: 100,
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	token, err := util.GenerateToken(cfg.JWTSecret, 24, client.ID, client.Name, client.Role, "service")
	if err != nil {
		t.Fatal(err)
	}
	return db, r, "ak_test_his", token
}

func doJSON(t *testing.T, r http.Handler, method, path, apiKey, token string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestAdjustmentHTTP_FullFlow(t *testing.T) {
	db, r, apiKey, token := setupTestRouter(t)

	// 落库一条已结算订单
	order := &model.SettlementOrder{
		SettlementNo: util.SettlementNo(1), BatchID: 1, InsuredPersonID: 1, PresettlementID: 1,
		ClientID: 1, Status: constants.SettlementSettled, TotalAmount: 1000, InsurancePayAmount: 800,
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatal(err)
	}

	// 未携带认证头 → 401
	if code, _ := doJSON(t, r, http.MethodPost, "/api/v1/settlements/"+order.SettlementNo+"/adjustments", "", "", map[string]any{"target_pay_amount": 700, "reason": "x"}); code != http.StatusUnauthorized {
		t.Fatalf("no-auth apply code = %d, want 401", code)
	}

	// 目标金额不合法 → 400
	if code, body := doJSON(t, r, http.MethodPost, "/api/v1/settlements/"+order.SettlementNo+"/adjustments", apiKey, token, map[string]any{"target_pay_amount": 900, "reason": "x"}); code != http.StatusBadRequest {
		t.Fatalf("invalid target code = %d body=%v, want 400", code, body)
	}

	// 正常申请 → 201
	code, body := doJSON(t, r, http.MethodPost, "/api/v1/settlements/"+order.SettlementNo+"/adjustments", apiKey, token, map[string]any{"target_pay_amount": 700, "reason": "医保局核减"})
	if code != http.StatusCreated {
		t.Fatalf("apply code = %d body=%v, want 201", code, body)
	}
	data := body["data"].(map[string]any)
	adjNo := data["adjustment_no"].(string)
	if data["status"].(string) != constants.AdjustmentPendingReview {
		t.Fatalf("status = %v, want pending_review", data["status"])
	}
	if data["difference_amount"].(float64) != -100 {
		t.Fatalf("difference = %v, want -100", data["difference_amount"])
	}

	// 重复申请 → 409
	if code, _ := doJSON(t, r, http.MethodPost, "/api/v1/settlements/"+order.SettlementNo+"/adjustments", apiKey, token, map[string]any{"target_pay_amount": 600, "reason": "dup"}); code != http.StatusConflict {
		t.Fatalf("duplicate apply code = %d, want 409", code)
	}

	// 驳回缺少原因 → 400
	if code, _ := doJSON(t, r, http.MethodPost, "/api/v1/settlement-adjustments/"+adjNo+"/review", apiKey, token, map[string]any{"approved": false, "reason": ""}); code != http.StatusBadRequest {
		t.Fatalf("reject no reason code = %d, want 400", code)
	}
	// 驳回 → 200
	if code, body := doJSON(t, r, http.MethodPost, "/api/v1/settlement-adjustments/"+adjNo+"/review", apiKey, token, map[string]any{"approved": false, "reason": "凭证不足"}); code != http.StatusOK {
		t.Fatalf("reject code = %d body=%v, want 200", code, body)
	} else {
		if body["data"].(map[string]any)["status"].(string) != constants.AdjustmentRejected {
			t.Fatalf("status = %v, want rejected", body["data"].(map[string]any)["status"])
		}
	}
	// 驳回后可再次申请 → 201
	code, body = doJSON(t, r, http.MethodPost, "/api/v1/settlements/"+order.SettlementNo+"/adjustments", apiKey, token, map[string]any{"target_pay_amount": 650, "reason": "补全凭证"})
	if code != http.StatusCreated {
		t.Fatalf("reapply code = %d body=%v, want 201", code, body)
	}
	adjNo2 := body["data"].(map[string]any)["adjustment_no"].(string)

	// 复核通过 → 200
	code, body = doJSON(t, r, http.MethodPost, "/api/v1/settlement-adjustments/"+adjNo2+"/review", apiKey, token, map[string]any{"approved": true})
	if code != http.StatusOK {
		t.Fatalf("approve code = %d body=%v, want 200", code, body)
	}
	// 重复复核 → 409
	if code, _ := doJSON(t, r, http.MethodPost, "/api/v1/settlement-adjustments/"+adjNo2+"/review", apiKey, token, map[string]any{"approved": true}); code != http.StatusConflict {
		t.Fatalf("duplicate review code = %d, want 409", code)
	}

	// 详情回读：状态已补退 + 一条负向流水
	code, body = doJSON(t, r, http.MethodGet, "/api/v1/settlement-adjustments/"+adjNo2, apiKey, token, nil)
	if code != http.StatusOK {
		t.Fatalf("detail code = %d body=%v, want 200", code, body)
	}
	d := body["data"].(map[string]any)
	if d["status"].(string) != constants.AdjustmentApproved {
		t.Fatalf("detail status = %v, want approved", d["status"])
	}
	entries := d["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	e := entries[0].(map[string]any)
	if e["direction"].(string) != "negative" || e["amount"].(float64) != -150 {
		t.Fatalf("entry invalid: %+v", e)
	}

	// 订单已补退，再申请 → 409
	if code, _ := doJSON(t, r, http.MethodPost, "/api/v1/settlements/"+order.SettlementNo+"/adjustments", apiKey, token, map[string]any{"target_pay_amount": 600, "reason": "after"}); code != http.StatusConflict {
		t.Fatalf("apply on adjusted code = %d, want 409", code)
	}

	// 结算单详情状态已变为 adjusted
	var ord model.SettlementOrder
	if err := db.First(&ord, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ord.Status != constants.SettlementAdjusted {
		t.Fatalf("order status = %s, want adjusted", ord.Status)
	}

	// 列表查询
	code, body = doJSON(t, r, http.MethodGet, "/api/v1/settlement-adjustments?client_id=1&status=approved", apiKey, token, nil)
	if code != http.StatusOK {
		t.Fatalf("list code = %d, want 200", code)
	}
	if body["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("approved total = %v, want 1", body["data"].(map[string]any)["total"])
	}
	// 按结算单查询历史（驳回 + 通过共两条）
	code, body = doJSON(t, r, http.MethodGet, "/api/v1/settlements/"+order.SettlementNo+"/adjustments", apiKey, token, nil)
	if code != http.StatusOK {
		t.Fatalf("list by order code = %d, want 200", code)
	}
	if len(body["data"].([]any)) != 2 {
		t.Fatalf("order history len = %d, want 2", len(body["data"].([]any)))
	}
}
