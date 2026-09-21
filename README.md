# InsureSettle（医保智能结算 API 网关）

> 项目类型：纯后端 API 服务（医疗健康）

为医院信息系统提供标准化的医保结算接口服务，支持参保人身份核验、费用上传、预结算、正式结算、结算单查询、结算冲正与日终对账，对接医保局接口规范。业务接口采用 **API Key + JWT 双认证** 与按调用方 QPS 限流。

## 快速启动（Docker Compose 一键部署，首选）

```bash
# 1. 首次启动前复制环境变量
cp .env.example .env

# 2. 启动后端 + PostgreSQL
docker compose up -d

# 3. 查看健康状态
docker compose ps
```

访问地址：

- 后端 API：http://localhost:19935
- 健康检查：http://localhost:19935/healthz
- Swagger 文档：http://localhost:19935/swagger/index.html

演示凭证：

| 用途 | 凭证 |
| --- | --- |
| 网关管理员 | admin / admin123 |
| 演示医院 HIS | X-API-Key: ak_demo_his |
| 第三方药房 | X-API-Key: ak_demo_third |

演示参保人：

| 姓名 | 身份证号 | 医保卡号 | 医保类型 |
| --- | --- | --- | --- |
| 张三 | 110101199001011234 | M110101199001011234 | 职工 |
| 李四 | 310101198505052345 | M310101198505052345 | 居民 |
| 王五 | 440101197803036789 | M440101197803036789 | 新农合 |

## 本地开发

```bash
cd backend
go mod tidy
go run ./cmd/server
```

## 技术栈

| 层次 | 技术 |
| --- | --- |
| 后端 | Go 1.22 + Gin + GORM |
| 数据库 | PostgreSQL 15 |
| 认证 | API Key（HMAC-SHA256）+ JWT (github.com/golang-jwt/jwt/v5) 双认证 |
| 校验 | github.com/go-playground/validator/v10 |
| 文档 | github.com/swaggo/gin-swagger + swaggo/files |
| 日志 | log/slog 结构化日志 |

## 项目目录结构

```
ld-335/
├── docker-compose.yml        # backend + db 编排
├── .env.example              # 环境变量示例
├── database/init.sql         # 建表 + 种子数据（首次启动自动执行）
└── backend/
    ├── cmd/server/main.go    # 入口：装配依赖、启动 Gin + Swagger
    ├── docs/                 # swag 生成的 OpenAPI 文档
    └── internal/
        ├── config/           # 环境变量解析
        ├── model/            # api_client/insured_person/upload_batch/fee_item/presettlement/settlement_order/settlement_adjustment/adjustment_ledger/daily_reconciliation/audit_log
        ├── repository/       # 按实体分文件
        ├── service/          # api_client/insurance/fee/settlement/adjustment/reconciliation
        ├── handler/          # 按实体分文件（含 swagger 注解）
        ├── router/           # router.go + 按实体分文件
        ├── middleware/       # auth/api_key/rate_limiter/error_handler/audit_log/request_logger
        ├── dto/              # 请求/响应结构体
        ├── constants/        # medical/settlement/error_codes/log_templates/messages
        └── util/             # jwt/api_key/settlement_calculator/formatters/logger/app_error/response
```

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| COMPOSE_PROJECT_NAME | gbinsureapi | Docker Compose 项目短名 |
| DB_NAME | gbinsureapi_db | 数据库名 |
| DB_USER | gbinsureapi_user | 数据库用户 |
| DB_PASSWORD | gbinsureapi_pwd | 数据库密码 |
| JWT_SECRET | change_me_to_a_long_random_string | JWT 签名密钥 |
| API_KEY_SECRET | change_me_to_a_long_random_string | API Key HMAC 加盐密钥 |
| APP_CORS_ORIGINS | http://localhost:19935 | CORS 来源白名单（逗号分隔，生产请显式配置，不允许 `*`） |
| BACKEND_PORT | 19935 | 后端对外端口 |
| DB_PORT | 5432 | 数据库对外端口 |

## API 接口清单

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/healthz` | 无 | 健康检查 |
| GET | `/swagger/*any` | 无 | Swagger UI 文档 |
| POST | `/api/v1/auth/login` | 无 | 管理员登录（返回 JWT） |
| POST | `/api/v1/clients` | JWT（admin） | 创建调用方 |
| GET | `/api/v1/clients` | JWT（admin） | 调用方列表 |
| PUT | `/api/v1/clients/:id/status` | JWT（admin） | 启用/停用调用方 |
| POST | `/api/v1/clients/token` | X-API-Key | 调用方换取服务 JWT |
| POST | `/api/v1/clients/verify` | X-API-Key + JWT | 参保人身份核验 |
| GET | `/api/v1/insured-persons` | X-API-Key + JWT | 参保人查询 |
| POST | `/api/v1/fees/upload` | X-API-Key + JWT | 费用明细上传 |
| GET | `/api/v1/fees/items` | X-API-Key + JWT | 费用明细查询 |
| GET | `/api/v1/batches` | X-API-Key + JWT | 上传批次列表 |
| GET | `/api/v1/batches/:id` | X-API-Key + JWT | 上传批次详情 |
| POST | `/api/v1/presettlements/calculate` | X-API-Key + JWT | 预结算计算 |
| GET | `/api/v1/presettlements` | X-API-Key + JWT | 批次预结算记录 |
| POST | `/api/v1/settlements/submit` | X-API-Key + JWT | 正式结算提交 |
| POST | `/api/v1/settlements/:settlement_no/reverse` | X-API-Key + JWT | 结算冲正 |
| POST | `/api/v1/settlements/:settlement_no/adjustments` | X-API-Key + JWT | 差额补退提交（生成待复核差额单） |
| POST | `/api/v1/settlements/adjustments/:adjustment_no/review` | X-API-Key + JWT | 差额补退复核（通过/驳回） |
| GET | `/api/v1/settlements/:settlement_no/adjustments` | X-API-Key + JWT | 结算单差额补退记录查询 |
| GET | `/api/v1/settlements/adjustments/:adjustment_no` | X-API-Key + JWT | 差额补退单详情（含负向账目） |
| GET | `/api/v1/settlements` | X-API-Key + JWT | 结算单列表 |
| GET | `/api/v1/settlements/:settlement_no` | X-API-Key + JWT | 结算单详情 |
| GET | `/api/v1/reconciliations/daily` | X-API-Key + JWT | 日终对账 |
| GET | `/api/v1/reconciliations` | X-API-Key + JWT | 对账记录列表 |

> 所有请求响应头均携带 `X-Request-ID`，日志按请求 ID 串联；业务接口统一返回 `{code, message, data}`。

## API 调用示例（curl）

```bash
BASE=http://localhost:19935
# 1. 管理员登录（调用方管理接口）
ADMIN_TOKEN=$(curl -s -X POST $BASE/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])')

# 2. 创建调用方（管理员）→ 返回明文 API Key
curl -s -X POST $BASE/api/v1/clients -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"测试HIS","client_type":"HIS","role":"settlement","rate_limit_qps":20}'

# 3. 调用方用 API Key 换取服务 JWT
API_KEY=ak_demo_his
SVC_TOKEN=$(curl -s -X POST $BASE/api/v1/clients/token -H "X-API-Key: $API_KEY" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])')

# 4. 参保人身份核验（业务接口：X-API-Key + Bearer JWT）
curl -s -X POST $BASE/api/v1/clients/verify -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"id_card_no":"110101199001011234","medical_card_no":"M110101199001011234"}'

# 5. 费用明细上传
curl -s -X POST $BASE/api/v1/fees/upload -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":1,"insured_person_id":1,"items":[{"item_code":"DRUG01","item_name":"阿莫西林","item_type":"drug","unit_price":50,"quantity":10,"self_pay_ratio":0,"medical_category":"class_a"}]}'

# 6. 预结算计算（batch_id 取上一步返回）
curl -s -X POST $BASE/api/v1/presettlements/calculate -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN" \
  -H 'Content-Type: application/json' -d '{"batch_id":1}'

# 7. 正式结算（presettlement_id 取上一步返回）
curl -s -X POST $BASE/api/v1/settlements/submit -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN" \
  -H 'Content-Type: application/json' -d '{"presettlement_id":1}'

# 8. 结算冲正（settlement_no 取上一步返回）
curl -s -X POST $BASE/api/v1/settlements/{SETTLEMENT_NO}/reverse \
  -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN"

# 9. 差额补退提交：目标医保支付额必须 ≥ 0 且 < 原医保支付额（同一结算单仅允许一条待复核）
curl -s -X POST $BASE/api/v1/settlements/{SETTLEMENT_NO}/adjustments \
  -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"target_insurance_pay":300.00,"reason":"医保局核定医保支付下调"}'

# 10. 差额补退复核（approve=true 通过：订单标记已补退并生成负向账目；false 驳回须填 review_remark）
curl -s -X POST $BASE/api/v1/settlements/adjustments/{ADJUSTMENT_NO}/review \
  -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN" \
  -H 'Content-Type: application/json' -d '{"approve":true,"review_remark":"核定无误"}'

# 11. 差额补退查询回读（详情含负向账目 ledger；列表按结算单查询）
curl -s $BASE/api/v1/settlements/adjustments/{ADJUSTMENT_NO} \
  -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN"
curl -s $BASE/api/v1/settlements/{SETTLEMENT_NO}/adjustments \
  -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN"

# 12. 日终对账
curl -s "$BASE/api/v1/reconciliations/daily?client_id=1" -H "X-API-Key: $API_KEY" -H "Authorization: Bearer $SVC_TOKEN"
```

## 结算差额补退（补退流程说明）

对**已结算（settled）**订单，医院可按医保局核定结果提交目标医保支付额，发起差额补退：

1. **提交**：`POST /api/v1/settlements/:settlement_no/adjustments`，提交 `target_insurance_pay`（目标医保支付额）与 `reason`（原因）。目标金额 **小于零** 或 **不小于原医保支付额** 时拒绝；同一结算单只允许一条 `pending_review` 待复核记录（应用层预检 + 数据库部分唯一索引 `idx_adjustment_order_pending` 双重保证）。
2. **复核**：`POST /api/v1/settlements/adjustments/:adjustment_no/review`
   - `approve=true`：在一个数据库事务内对差额单与订单加行锁并二次校验状态，通过后订单置为 `adjusted`（已补退），同时生成一条**负向账目**（`adjustment_ledgers`，金额 = 目标 − 原支付，恒为负）冲减差额。
   - `approve=false`：差额单置为 `rejected`，**必须**携带 `review_remark`，保留申请原因，不改动订单与账目；驳回后可对同一订单再次申请。
3. **并发安全**：并发复核借助 `SELECT ... FOR UPDATE`（PostgreSQL）+ 状态二次校验串行化，保证只成功一次；重复提交、订单状态不符（非 `settled`）或重复复核全部返回 `409`，失败时事务回滚，**订单与账目不发生任何改动**。
4. **查询回读**：提交、复核结果与负向账目均可通过详情/列表接口回读；状态全部落库，服务重启后一致。

## Docker 部署说明

- 端口映射：后端 `19935:8080`、数据库 `5432:5432`
- 数据卷：`db_data` 命名卷持久化 PostgreSQL 数据
- 依赖顺序：backend `depends_on` db 健康后启动
- 常见问题：
  - 端口冲突：修改 `.env` 中的 `BACKEND_PORT` / `DB_PORT`
  - 数据重置：`docker compose down -v` 后重新 `docker compose up -d`
  - 中文目录：容器名使用 `COMPOSE_PROJECT_NAME` 前缀，不依赖目录名

## 枚举出现位置清单

### MedicalCategory（医保目录分类：class_a/class_b/class_c）

- `backend/internal/constants/medical.go`（定义）
- `backend/internal/model/fee_item.go`（模型）
- `backend/internal/service/fee_service.go`（上传校验）
- `backend/internal/service/settlement_service.go`（预结算输入）
- `backend/internal/util/settlement_calculator.go`（报销计算分支）
- `backend/internal/util/formatters.go`（中文文案）
- `backend/internal/constants/log_templates.go`（日志模板）
- `backend/internal/constants/error_codes.go`（错误码）
- `backend/internal/dto/fee_dto.go`（DTO 校验 tag）
- `backend/internal/handler/fee_item_handler.go`（上传接口）

### SettlementStatus（结算状态：presettled/settled/reversed/failed/pending_manual/adjusted）

- `backend/internal/constants/settlement.go`（定义，含差额补退新增的 `adjusted=已补退`）
- `backend/internal/model/settlement_order.go`（模型）
- `backend/internal/service/settlement_service.go`（状态机：提交→settled、冲正→reversed）
- `backend/internal/service/adjustment_service.go`（差额补退状态机：复核通过 settled→adjusted）
- `backend/internal/service/reconciliation_service.go`（对账统计分支）
- `backend/internal/util/formatters.go`（中文文案，含 SettlementStatusText/AdjustmentStatusText）
- `backend/internal/constants/log_templates.go`（日志模板）
- `backend/internal/constants/error_codes.go`（错误码）
- `backend/internal/repository/settlement_order_repository.go`（按状态查询、行锁复核）
- `backend/internal/repository/settlement_adjustment_repository.go`（差额单状态与部分唯一索引）
- `backend/internal/model/settlement_adjustment.go` / `backend/internal/model/adjustment_ledger.go`（补退单与负向账目）

### AdjustmentStatus（差额补退单状态：pending_review/approved/rejected）

- `backend/internal/constants/settlement.go`（定义）
- `backend/internal/model/settlement_adjustment.go`（模型）
- `backend/internal/service/adjustment_service.go`（提交→pending_review、复核→approved/rejected 状态机）
- `backend/internal/repository/settlement_adjustment_repository.go`（按状态查询、待复核部分唯一索引、行锁）
- `backend/internal/util/formatters.go`（AdjustmentStatusText 中文文案）
- `backend/internal/constants/log_templates.go`（日志模板）
- `backend/internal/constants/error_codes.go`（错误码）
- `backend/internal/constants/messages.go`（返回文案）
- `backend/internal/dto/settlement_dto.go`（提交/复核 DTO）
- `backend/internal/handler/settlement_adjustment_handler.go`（提交/复核/查询接口）

### InsuranceType（医保类型：employee/resident/new_rural）

- `backend/internal/constants/medical.go`（定义）
- `backend/internal/model/insured_person.go`（模型）
- `backend/internal/service/insurance_service.go`（身份核验）
- `backend/internal/service/settlement_service.go`（预结算政策分支）
- `backend/internal/util/settlement_calculator.go`（报销政策）
- `backend/internal/util/formatters.go`（中文文案）
- `backend/internal/constants/log_templates.go`（日志模板）

## License

MIT
