package router

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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

const (
	testJWTSecret = "test-jwt-secret-long-enough"
	testKeySecret = "test-apikey-secret-long-enough"
	testAPIKey    = "ak_integration_his"
)

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newIntegrationEngine(t *testing.T) (*gorm.DB, http.Handler) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&model.ApiClient{}, &model.InsuredPerson{}, &model.UploadBatch{}, &model.FeeItem{},
		&model.Presettlement{}, &model.SettlementOrder{}, &model.DailyReconciliation{}, &model.AuditLog{},
		&model.SettlementAdjustment{}, &model.AdjustmentLedger{},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_adjustment_order_pending
		ON settlement_adjustments(settlement_order_id) WHERE status = 'pending_review'`).Error; err != nil {
		t.Fatal(err)
	}
	client := model.ApiClient{Name: "集成HIS", ClientType: constants.ClientTypeHIS, APIKeyHash: util.HashAPIKey(testAPIKey, testKeySecret), Role: "settlement", Status: constants.ClientActive, RateLimitQPS: 100}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&model.SettlementOrder{
		SettlementNo: "SINTEG001", Status: constants.SettlementSettled,
		TotalAmount: 1000, InsurancePayAmount: 400, ClientID: client.ID, SettledAt: &now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	orderRepo := repository.NewSettlementOrderRepository(db)
	clientRepo := repository.NewApiClientRepository(db)
	adjRepo := repository.NewSettlementAdjustmentRepository(db)
	ledgerRepo := repository.NewAdjustmentLedgerRepository(db)
	auditRepo := repository.NewAuditLogRepository(db)
	clientSvc := service.NewApiClientService(clientRepo, testKeySecret, testJWTSecret, 24, testLog())
	adjSvc := service.NewAdjustmentService(db, orderRepo, adjRepo, ledgerRepo, testLog())
	h := Handlers{
		ApiClient:  handler.NewApiClientHandler(clientSvc, testLog()),
		Adjustment: handler.NewSettlementAdjustmentHandler(adjSvc, testLog()),
	}
	cfg := config.Config{JWTSecret: testJWTSecret, CORSOrigins: "http://localhost:19935"}
	return db, New(cfg, testLog(), h, clientSvc, auditRepo, middleware.NewRateLimiter())
}

func serviceToken(t *testing.T, h http.Handler) string {
	t.Helper()
	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clients/token", bytes.NewBufferString(body))
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != 0 || resp.Data.Token == "" {
		t.Fatalf("issue token failed: %s", rec.Body.String())
	}
	return resp.Data.Token
}

func doJSON(t *testing.T, h http.Handler, method, path, apiKey, token, payload string) (int, map[string]any) {
	t.Helper()
	var buf *bytes.Buffer
	if payload != "" {
		buf = bytes.NewBufferString(payload)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestAdjustmentHTTP_FullLifecycle(t *testing.T) {
	_, h := newIntegrationEngine(t)
	token := serviceToken(t, h)
	auth := func(p string) (int, map[string]any) {
		return doJSON(t, h, http.MethodPost, "/api/v1/settlements/SINTEG001/adjustments", testAPIKey, token, p)
	}

	// 未携带认证头应 401
	if code, _ := doJSON(t, h, http.MethodPost, "/api/v1/settlements/SINTEG001/adjustments", "", "", `{"target_insurance_pay":300,"reason":"x"}`); code != http.StatusUnauthorized {
		t.Fatalf("no-auth code=%d, want 401", code)
	}

	// 目标金额非法（>= 原支付额 400）应 400
	if code, body := auth(`{"target_insurance_pay":400,"reason":"等于原额"}`); code != http.StatusBadRequest {
		t.Fatalf("invalid target code=%d body=%v", code, body)
	}
	// 目标金额为负应 400
	if code, _ := auth(`{"target_insurance_pay":-1,"reason":"负数"}`); code != http.StatusBadRequest {
		t.Fatalf("negative target code=%d, want 400", code)
	}
	// 原因为空应 400（validator）
	if code, _ := auth(`{"target_insurance_pay":300,"reason":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty reason code=%d, want 400", code)
	}

	// 合法提交：201 + 待复核
	code, body := auth(`{"target_insurance_pay":300,"reason":"医保局核定下调"}`)
	if code != http.StatusCreated || body["code"].(float64) != 0 {
		t.Fatalf("submit code=%d body=%v", code, body)
	}
	data := body["data"].(map[string]any)
	adjNo := data["adjustment_no"].(string)
	if data["status"].(string) != constants.AdjustmentPendingReview {
		t.Fatalf("status=%v", data["status"])
	}

	// 重复提交：409，订单与已存在差额单不变
	if code, _ := auth(`{"target_insurance_pay":350,"reason":"重复申报"}`); code != http.StatusConflict {
		t.Fatalf("duplicate submit code=%d, want 409", code)
	}

	// 驳回缺少意见：400
	if code, _ := doJSON(t, h, http.MethodPost, "/api/v1/settlements/adjustments/"+adjNo+"/review", testAPIKey, token, `{"approve":false,"review_remark":""}`); code != http.StatusBadRequest {
		t.Fatalf("reject without remark code=%d, want 400", code)
	}
	// 驳回：200，保留原因
	code, body = doJSON(t, h, http.MethodPost, "/api/v1/settlements/adjustments/"+adjNo+"/review", testAPIKey, token, `{"approve":false,"review_remark":"材料不全"}`)
	if code != http.StatusOK || body["data"].(map[string]any)["status"].(string) != constants.AdjustmentRejected {
		t.Fatalf("reject code=%d body=%v", code, body)
	}

	// 驳回后再次申请并通过
	code, body = auth(`{"target_insurance_pay":320,"reason":"补充材料"}`)
	if code != http.StatusCreated {
		t.Fatalf("reapply code=%d body=%v", code, body)
	}
	adjNo2 := body["data"].(map[string]any)["adjustment_no"].(string)
	code, body = doJSON(t, h, http.MethodPost, "/api/v1/settlements/adjustments/"+adjNo2+"/review", testAPIKey, token, `{"approve":true,"review_remark":"核定无误"}`)
	if code != http.StatusOK {
		t.Fatalf("approve code=%d body=%v", code, body)
	}
	approved := body["data"].(map[string]any)
	if approved["status"].(string) != constants.AdjustmentApproved {
		t.Fatalf("approved status=%v", approved["status"])
	}
	ledger := approved["ledger"].(map[string]any)
	if ledger["direction"].(string) != constants.LedgerDirectionNegative || ledger["amount"].(float64) != -80 {
		t.Fatalf("ledger wrong: %v", ledger)
	}

	// 重复复核已通过单：409
	if code, _ := doJSON(t, h, http.MethodPost, "/api/v1/settlements/adjustments/"+adjNo2+"/review", testAPIKey, token, `{"approve":true}`); code != http.StatusConflict {
		t.Fatalf("double review code=%d, want 409", code)
	}
	// 已补退订单再次申请：409
	if code, _ := auth(`{"target_insurance_pay":300,"reason":"已补退再申请"}`); code != http.StatusConflict {
		t.Fatalf("submit after adjusted code=%d, want 409", code)
	}

	// 查询回读：详情携带账目；列表两条（rejected + approved）
	if code, body = doJSON(t, h, http.MethodGet, "/api/v1/settlements/adjustments/"+adjNo2, testAPIKey, token, ""); code != http.StatusOK || body["data"].(map[string]any)["ledger"] == nil {
		t.Fatalf("detail code=%d body=%v", code, body)
	}
	if code, body = doJSON(t, h, http.MethodGet, "/api/v1/settlements/SINTEG001/adjustments", testAPIKey, token, ""); code != http.StatusOK || body["data"].(map[string]any)["total"].(float64) != 2 {
		t.Fatalf("list code=%d body=%v", code, body)
	}
}
