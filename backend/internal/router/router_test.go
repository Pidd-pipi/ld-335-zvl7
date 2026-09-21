package router

import (
	"testing"

	"github.com/blueship581/gbinsureapi/internal/config"
)

// TestNew_RoutesRegister 确保差额补退的静态段 adjustments 与既有 :settlement_no 通配路由可共存，
// Gin 在启动期对冲突路由会 panic（httprouter 行为），本测试用于回归。
func TestNew_RoutesRegister(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("router.New panicked on route registration: %v", rec)
		}
	}()
	r := New(config.Config{}, nil, Handlers{}, nil, nil, nil)
	if r == nil {
		t.Fatal("router nil")
	}
}
