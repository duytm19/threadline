# THREADLINE — Distributed Commerce Backend Platform
### Mock Project để vượt kỳ vọng JD "Software Engineer II – Global Fashion Group (GFG)"

> **Tóm tắt 1 dòng:** Xây dựng backend nền tảng thương mại điện tử thời trang **multi-tenant, multi-country, event-driven** trên Kubernetes/AWS — với bài toán lõi khó nhất của ngành: **không bao giờ oversell** khi flash sale, **orchestrate đơn hàng phân tán bằng Saga**, và **đo được p99 bằng số liệu thật**.

---

## 0. Phân tích JD → Chiến lược Project

### 0.1. Giải mã JD (đọc giữa các dòng)

| JD nói gì | Thực tế nhà tuyển dụng muốn gì | Project sẽ chứng minh bằng cách nào |
|---|---|---|
| "high-traffic distributed systems" | Bạn từng xử lý race condition, partial failure, network split, retry storm | Flash-sale test: 10.000 concurrent request tranh 100 SKU → 0 oversell, có report k6 |
| "microservices architecture (Kubernetes)" | Bạn hiểu service boundary, không chỉ "chia nhỏ monolith" | 5 service với bounded context rõ ràng + ADR giải thích **vì sao không chia 12 service** |
| "build and maintain APIs" | API versioning, backward compatibility, error contract | OpenAPI 3.1, RFC 7807, cursor pagination, `Sunset` header, contract test (Pact) |
| "clean and well-tested code" | Test pyramid thật, không phải 100% coverage giả | Domain layer ≥ 90%, testcontainers integration, mutation testing |
| "internal tools suite / architectural vision" | Bạn viết được ADR và bảo vệ được trade-off | 12 ADR trong repo |
| "site reliability, developer experience, raw performance" | SRE mindset: SLO, error budget, golden signals | Grafana dashboard + SLO 99.9% + alert rules + runbook |
| GFG có 4 platform / 4 khu vực (LATAM, CIS, SEA, ANZ) | Multi-tenant, multi-currency, multi-locale là DNA của họ | Tenant = (platform, country). Data isolation + pricing/i18n theo tenant |
| Fashion e-commerce | Size/color variant explosion, tỉ lệ return 30–40% | Variant model chuẩn + module Returns/RMA (rất ít ứng viên làm) |

### 0.2. Độ khó: JD yêu cầu vs Project (+40%)

| Khía cạnh | Mức JD | Mức Project | Delta |
|---|---|---|---|
| Service | Làm việc trong 1–2 service | Thiết kế + build 5 service + gateway | +40% |
| Consistency | CRUD + transaction | Saga + Outbox + CDC + idempotency + exactly-once effect | +60% |
| Concurrency | Optimistic lock cơ bản | Atomic SQL guard + Redis Lua + sharded counter + reconciliation | +70% |
| Data | 1 Postgres | Aurora + Redis + Kafka + OpenSearch (CQRS) + S3 | +50% |
| Ops | Deploy được | IaC + GitOps + canary + SLO + tracing xuyên Kafka + cost model | +60% |
| Testing | Unit test | Unit + integration (testcontainers) + contract + E2E + load + chaos | +50% |

> **Nguyên tắc vàng:** Nhà tuyển dụng không impress vì số lượng feature. Họ impress vì **một bài toán khó được giải đúng, đo được, và giải thích được**. Nếu thời gian hạn chế → cắt feature, **đừng cắt phần Inventory + Saga + Observability**.

### 0.3. Tech stack

| Layer | Lựa chọn | Lý do (nói được trong interview) |
|---|---|---|
| Ngôn ngữ chính | **Go 1.23** | JD liệt kê Golang; goroutine phù hợp I/O-bound high-traffic; binary nhỏ → container 15MB → k8s scale nhanh; static typing giảm bug production |
| Ngôn ngữ phụ (1 service) | **Java 21 + Spring Boot 3** hoặc **Python FastAPI** | Chứng minh polyglot microservices thật (khác nhau về runtime → buộc phải chuẩn hoá contract, observability, CI). Gợi ý: dùng cho `search-service` hoặc `payment-service` |
| DB | PostgreSQL 16 (Aurora) | ACID, JSONB cho product attributes, partial index, partitioning, logical replication cho CDC |
| Cache / lock | Redis 7 (ElastiCache) | Hot SKU counter, rate limit, session blacklist, distributed lock |
| Message bus | Kafka (Amazon MSK) | Ordering theo key (order_id), replay, log compaction cho read model |
| Search | OpenSearch | Read model CQRS cho catalog (facet, typo tolerance, multi-locale) |
| Object storage | S3 + CloudFront | Media, invoice PDF, export report |
| Container orchestration | EKS + Helm + ArgoCD | Đúng JD |
| Observability | OpenTelemetry → Prometheus / Loki / Tempo / Grafana | Vendor-neutral |

---

## 1. Business Requirement

### 1.1. Bối cảnh nghiệp vụ

**Threadline** là nền tảng backend cho một tập đoàn thương mại điện tử thời trang vận hành **nhiều storefront tại nhiều quốc gia** (mô phỏng đúng mô hình GFG: dafiti / lamoda / ZALORA / THE ICONIC).

Mỗi **tenant** = cặp `(platform, country)`, ví dụ `zalora-vn`, `zalora-sg`, `iconic-au`. Mỗi tenant có:
- Catalog riêng (một sản phẩm có thể bán ở nhiều nước, nhưng giá/tồn/nội dung khác nhau)
- Giá và tiền tệ riêng (VND, SGD, AUD, BRL, RUB)
- Kho hàng riêng hoặc dùng chung theo vùng
- Quy tắc thuế, phí ship, chính sách đổi trả riêng

### 1.2. Business Goals

| # | Mục tiêu kinh doanh | Metric | Ràng buộc kỹ thuật kéo theo |
|---|---|---|---|
| BG-1 | Không bao giờ bán vượt tồn kho (oversell) | Oversell rate = 0 | Reservation phải atomic ở mức global, kể cả khi có 3 instance service |
| BG-2 | Không mất đơn hàng khi hệ thống lỗi cục bộ | Order loss = 0 | Outbox pattern + at-least-once + idempotency |
| BG-3 | Không thu tiền 2 lần của khách | Duplicate charge = 0 | Idempotency key + payment state machine |
| BG-4 | Flash sale (Black Friday) chịu tải x20 | 500 orders/s peak, p99 checkout < 300ms | Async checkout, hot SKU sharding, autoscale theo lag |
| BG-5 | Trải nghiệm tìm kiếm dưới 100ms | p99 search < 100ms | CQRS read model trên OpenSearch |
| BG-6 | Đổi trả (return rate ngành fashion 30–40%) phải tự động | Refund SLA < 24h sau khi nhận hàng | Return state machine + refund saga |
| BG-7 | Mở storefront ở quốc gia mới trong 1 sprint | Onboard tenant ≤ 1 ngày | Tenant config-driven, không hardcode |
| BG-8 | Chi phí hạ tầng/đơn hàng giảm dần theo scale | Cost per 1k orders | Spot node cho worker, right-sizing, log sampling |

### 1.3. Actors

| Actor | Mô tả | Quyền chính |
|---|---|---|
| Customer | Người mua cuối | Browse, cart, checkout, xem đơn, tạo return |
| Seller (Marketplace) | Đối tác bán hàng | Quản lý sản phẩm của mình, xem đơn, cập nhật tồn |
| Ops / Warehouse Staff | Nhân viên kho | Pick/pack/ship, nhận hàng trả, điều chỉnh tồn |
| CS Agent | Chăm sóc khách hàng | Xem đơn, huỷ đơn, refund thủ công (có approval) |
| Admin | Quản trị tenant | Cấu hình tenant, role, promotion, feature flag |
| System / Machine | Service nội bộ, job | Gọi internal API bằng client-credentials |

### 1.4. Business Rules quan trọng (nguồn của mọi bug thú vị)

- **BR-01**: Tồn kho được **reserve** khi checkout khởi tạo, giữ trong **15 phút**. Hết hạn → tự động release.
- **BR-02**: `available = on_hand - reserved - damaged`. Không bao giờ cho phép `available < 0`.
- **BR-03**: Một đơn hàng có thể được **split** thành nhiều shipment nếu hàng nằm ở nhiều kho.
- **BR-04**: Thanh toán thất bại → release reservation ngay lập tức, không chờ TTL.
- **BR-05**: Đơn đã `SHIPPED` không thể huỷ, chỉ có thể tạo Return.
- **BR-06**: Return chỉ hợp lệ trong 30 ngày kể từ ngày giao, và sản phẩm phải qua **inspection** trước khi refund.
- **BR-07**: Refund không được vượt quá số tiền đã capture (bao gồm cả các refund trước đó).
- **BR-08**: Giá tại thời điểm đặt hàng được **snapshot** vào order item — thay đổi giá sau đó không ảnh hưởng đơn cũ.
- **BR-09**: Promotion không được stack quá 1 voucher + 1 campaign; tổng discount ≤ 70% giá gốc.
- **BR-10**: Mỗi tenant chỉ nhìn thấy dữ liệu của chính mình — kể cả khi lập trình viên quên `WHERE tenant_id` (→ ép bằng RLS).

---

## 2. Functional Requirement

### 2.1. Bản đồ module (Bounded Context)

```mermaid
graph TB
    subgraph Edge
        GW[API Gateway / BFF]
    end
    subgraph Core Services
        ID[identity-service]
        CAT[catalog-service]
        INV[inventory-service]
        ORD[order-service<br/>Saga Orchestrator]
        PAY[payment-service]
        FUL[fulfillment-service]
    end
    subgraph Read Side
        SRCH[search-service<br/>OpenSearch]
    end
    subgraph Async
        NOTIF[notification-worker]
        RECON[reconciliation-worker]
    end
    K[(Kafka)]

    GW --> ID & CAT & INV & ORD & SRCH
    ORD -.gRPC.-> INV & PAY & CAT
    CAT --> K
    INV --> K
    ORD --> K
    PAY --> K
    FUL --> K
    K --> SRCH & NOTIF & RECON & ORD & FUL
```

### 2.2. Đặc tả chức năng theo module

#### M1 — Identity & Access (`identity-service`)
| ID | Chức năng | Chi tiết |
|---|---|---|
| F1.1 | Đăng ký / đăng nhập | Email + password (Argon2id), rate limit theo IP + account |
| F1.2 | OAuth2 social login | Google/Apple (Authorization Code + PKCE) |
| F1.3 | JWT access + refresh rotation | Access 15 phút, refresh 30 ngày, **reuse detection** → revoke cả family |
| F1.4 | RBAC + scope | Role → Permission (`order:read`, `inventory:adjust`) |
| F1.5 | Service-to-service auth | Client Credentials grant, hoặc mTLS qua service mesh |
| F1.6 | MFA (TOTP) cho Admin/Ops | Bắt buộc với role có quyền refund/adjust stock |
| F1.7 | Session/token revocation | Blacklist `jti` trên Redis đến khi hết TTL |
| F1.8 | Audit log | Ai làm gì, khi nào, IP, user-agent — append-only |

#### M2 — Catalog (`catalog-service`)
| ID | Chức năng | Chi tiết |
|---|---|---|
| F2.1 | Quản lý Product / Variant (SKU) | 1 product → N variant theo (size, color); attribute động bằng JSONB |
| F2.2 | Category cây phân cấp | Closure table hoặc `ltree`, hỗ trợ move subtree |
| F2.3 | Brand, Season, Collection | Metadata thời trang |
| F2.4 | i18n | `product_translations` theo locale (`vi-VN`, `en-SG`, `pt-BR`, `ru-RU`) |
| F2.5 | Publish theo tenant | Cùng 1 product, publish `zalora-vn` nhưng ẩn `iconic-au` |
| F2.6 | Media | Upload S3 qua presigned URL, resize async |
| F2.7 | Bulk import | CSV 100k dòng → xử lý bằng worker + progress tracking |
| F2.8 | Emit domain event | `ProductPublished`, `VariantUpdated` → Kafka (qua Outbox) |

#### M3 — Inventory (`inventory-service`) ⭐ *module lõi, khó nhất*
| ID | Chức năng | Chi tiết |
|---|---|---|
| F3.1 | Multi-warehouse stock | `stock_item(variant_id, warehouse_id, on_hand, reserved, damaged, version)` |
| F3.2 | ATP (Available To Promise) | Query tổng available theo tenant/region, có cache |
| F3.3 | **Reserve** | Atomic, idempotent theo `reservation_id`, TTL 15 phút |
| F3.4 | **Commit** (confirm) | Reserved → trừ `on_hand` khi hàng được pick |
| F3.5 | **Release** | Huỷ reservation (do payment fail / timeout / user cancel) |
| F3.6 | Stock ledger | Append-only `stock_movements` — mọi thay đổi đều có audit trail |
| F3.7 | Reconciliation job | So khớp `sum(movements)` vs `stock_item`, alert nếu lệch |
| F3.8 | Hot SKU mode | SKU vượt ngưỡng traffic → chuyển sang Redis token bucket + sharded counter |
| F3.9 | Low-stock event | `StockBelowThreshold` → notify merchandising |
| F3.10 | Bulk adjust | Kiểm kê kho, import file, có approval flow |

#### M4 — Cart & Pricing (nằm trong `catalog-service` hoặc tách nhỏ)
| ID | Chức năng | Chi tiết |
|---|---|---|
| F4.1 | Cart | Lưu Redis (TTL 30 ngày) + snapshot sang Postgres khi user login |
| F4.2 | Merge cart | Guest cart + user cart khi đăng nhập |
| F4.3 | Price resolution | Base price → tenant price → campaign → voucher → tax |
| F4.4 | Promotion engine | Rule-based: `BUY_X_GET_Y`, `PERCENT_OFF`, `FREE_SHIP`, có priority & exclusivity |
| F4.5 | Cart validation | Trước checkout: re-check giá, tồn, trạng thái publish |

#### M5 — Order (`order-service`) ⭐ *Saga Orchestrator*
| ID | Chức năng | Chi tiết |
|---|---|---|
| F5.1 | Checkout API (async) | Trả `202 Accepted` + `order_id` + polling/SSE trạng thái |
| F5.2 | Order state machine | `CREATED → RESERVED → PAID → ALLOCATED → SHIPPED → DELIVERED → COMPLETED`, nhánh `CANCELLED / FAILED / REFUNDED` |
| F5.3 | Saga orchestration | Reserve stock → Charge payment → Allocate → Confirm. Mỗi bước có compensation |
| F5.4 | Idempotent checkout | Header `Idempotency-Key`, cùng key → cùng response |
| F5.5 | Cancel order | Chỉ trước `SHIPPED`; kích hoạt compensation |
| F5.6 | Order query | Filter, sort, cursor pagination; view riêng cho CS |
| F5.7 | Invoice | Sinh PDF async → S3 → presigned URL |
| F5.8 | Timeline | Lịch sử trạng thái, hiển thị cho khách + CS |

#### M6 — Payment (`payment-service`)
| ID | Chức năng | Chi tiết |
|---|---|---|
| F6.1 | Mock PSP adapter | Interface `PaymentProvider`, 2 impl: `StripeMock`, `LocalWalletMock` (chứng minh Hexagonal) |
| F6.2 | Authorize / Capture / Void / Refund | State machine đầy đủ |
| F6.3 | Webhook handler | Verify signature (HMAC), idempotent theo `event_id`, lưu raw payload |
| F6.4 | Retry với backoff | Exponential + jitter, tối đa N lần → DLQ |
| F6.5 | Partial refund | Ràng buộc `sum(refunds) <= captured` |
| F6.6 | Payment reconciliation | Job đối soát daily với "PSP statement" (file CSV mock trên S3) |

#### M7 — Fulfillment & Returns (`fulfillment-service`)
| ID | Chức năng | Chi tiết |
|---|---|---|
| F7.1 | Allocation | Chọn kho tối ưu (gần nhất / ít split nhất / đủ hàng) — thuật toán có thể pluggable |
| F7.2 | Shipment lifecycle | `PENDING → PICKING → PACKED → HANDED_OVER → IN_TRANSIT → DELIVERED` |
| F7.3 | Carrier adapter | Mock 3PL, cập nhật tracking qua webhook |
| F7.4 | Return request (RMA) | Khách tạo yêu cầu, chọn lý do, upload ảnh |
| F7.5 | Inspection | Ops kiểm tra → `ACCEPTED` (restock) / `REJECTED` (trả lại khách) |
| F7.6 | Refund saga | Inspection accepted → trigger refund + restock atomically |
| F7.7 | Split shipment | Một order → nhiều shipment, trạng thái order tổng hợp từ các shipment |

#### M8 — Search (`search-service`)
| ID | Chức năng | Chi tiết |
|---|---|---|
| F8.1 | Index consumer | Consume Kafka → denormalize → bulk index OpenSearch |
| F8.2 | Full-text + typo tolerance | Multi-locale analyzer, synonym (áo thun = t-shirt) |
| F8.3 | Faceted filter | Brand, size, color, price range, discount |
| F8.4 | Sorting & relevance | `popularity`, `price`, `newest`, `relevance` |
| F8.5 | Availability-aware | Ẩn/hạ rank SKU hết hàng (nhận `StockChanged` event) |
| F8.6 | Reindex zero-downtime | Alias swap: `products_v1` → `products_v2` |

#### M9 — Platform / Cross-cutting
| ID | Chức năng |
|---|---|
| F9.1 | Notification worker (email/SMS mock, template theo locale) |
| F9.2 | Outbox relay (Debezium CDC hoặc polling publisher) |
| F9.3 | Dead Letter Queue + UI/CLI để replay message |
| F9.4 | Feature flag theo tenant (Unleash hoặc self-built) |
| F9.5 | Admin API: tenant onboarding, config, quota |
| F9.6 | Scheduled jobs: expire reservation, retry payment, reconciliation, report |

### 2.3. Out of Scope (nói rõ để thể hiện tư duy scoping)
- Frontend đầy đủ (chỉ làm 1 demo UI tối giản hoặc Postman collection + Swagger UI)
- Tích hợp PSP thật, carrier thật (dùng mock có contract giống thật)
- Recommendation ML, dynamic pricing
- Data warehouse / BI (chỉ emit event, có ADR nói hướng mở rộng)

---

## 3. Non-Functional Requirements

### 3.1. Performance

| Metric | Target | Cách đo | Ghi chú |
|---|---|---|---|
| `GET /v1/products` (cached) | p50 < 25ms, p99 < 80ms | k6 + Grafana histogram | 2.000 RPS |
| `GET /v1/search` | p99 < 100ms | k6 | 1.000 RPS |
| `POST /v1/checkout` (async ack) | p99 < 300ms | k6 | 500 RPS |
| Saga end-to-end (order → PAID) | p95 < 3s | Trace span duration | Đo bằng Tempo |
| Kafka consumer lag | < 5s ở peak | `kafka_consumergroup_lag` | Alert nếu > 30s |
| Inventory reserve (uncontended) | p99 < 20ms | Micro-benchmark + integration test | |
| Inventory reserve (hot SKU, 10k concurrent) | p99 < 50ms, **oversell = 0** | k6 scenario riêng | Đây là "signature test" của project |
| Cold start container | < 3s | k8s readiness | Go binary distroless |

**Budget phân bổ latency cho checkout (p99 = 300ms):**
```
Gateway + auth        : 15ms
Cart validate (Redis) : 10ms
Price calc            : 20ms
Reserve stock (gRPC)  : 40ms
Persist order + outbox: 30ms
Network/serialization : 25ms
Buffer (an toàn)      : 160ms
```

### 3.2. Scalability

| Chiều | Chiến lược |
|---|---|
| **Stateless scale-out** | Mọi service không giữ state cục bộ → HPA theo CPU + RPS + custom metric |
| **Async scale** | KEDA scale consumer theo **Kafka lag**, không theo CPU (đúng bản chất workload) |
| **DB read scale** | Aurora reader endpoint cho read-heavy query; ứng dụng tự route read/write |
| **DB write scale** | Partition `orders`, `stock_movements`, `outbox_events` theo tháng; roadmap shard theo `tenant_id` |
| **Hot partition** | Kafka key = `order_id` (đảm bảo ordering) chứ không phải `tenant_id` (tránh skew) |
| **Hot SKU** | Sharded counter: chia stock 1000 thành 10 bucket × 100, request hash vào bucket, fallback sang bucket khác khi cạn |
| **Cache** | Multi-layer: in-process (LRU, TTL 5s) → Redis (TTL 60s) → DB. Chống thundering herd bằng singleflight |
| **Rate limit** | Token bucket theo tenant/user/IP, phân tầng |
| **Cell-based (roadmap)** | Mỗi region = 1 cell độc lập → blast radius nhỏ. Viết vào ADR |

**Bài toán scale để nói trong interview:** *"Khi RPS tăng 20x trong 5 phút, HPA phản ứng chậm ~60s. Giải pháp: (1) pre-warm bằng scheduled scaling trước campaign, (2) queue-based load leveling — checkout trả 202 ngay, xử lý nặng đẩy sang worker, (3) shed load có chọn lọc: ưu tiên user đã có cart, trả 429 cho bot."*

### 3.3. Security
Xem chi tiết ở **Mục 9**. Yêu cầu tối thiểu:
- TLS mọi nơi (edge + mTLS nội bộ)
- Không có secret trong code/image → AWS Secrets Manager + External Secrets Operator
- Encryption at rest: KMS cho RDS/S3/EBS; column-level cho PII (email, phone, address)
- OWASP Top 10 được checklist và có test tương ứng
- Container chạy non-root, read-only rootfs, drop all capabilities
- SBOM + image scanning trong CI, block khi có CVE HIGH/CRITICAL
- PII masking trong log; log không bao giờ chứa token/PAN

### 3.4. Reliability & Availability

| Yêu cầu | Giá trị |
|---|---|
| SLO API đọc | 99.95% availability, error budget 21 phút/tháng |
| SLO checkout | 99.9% |
| RPO | ≤ 5 phút (Aurora PITR + binlog) |
| RTO | ≤ 30 phút (runbook + IaC apply lại) |
| Graceful shutdown | `SIGTERM` → drain connection → finish in-flight → exit (preStop hook 10s) |
| Health check | `/healthz` (liveness, không phụ thuộc dependency), `/readyz` (readiness, có check DB/Kafka) |
| Resilience pattern | Timeout + retry (budget) + circuit breaker + bulkhead + fallback |
| Zero-downtime deploy | Rolling + `maxSurge=25%`, PDB, canary bằng Argo Rollouts |
| Chaos | Toxiproxy (latency/packet loss), kill pod ngẫu nhiên, DB failover drill |

### 3.5. Logging

- **Structured JSON** (`slog` của Go), 1 dòng = 1 event
- Trường bắt buộc: `timestamp`, `level`, `service`, `version`, `env`, `trace_id`, `span_id`, `tenant_id`, `user_id`(hash), `request_id`, `msg`
- Correlation: `trace_id` bơm từ gateway, propagate qua HTTP header **và** Kafka header
- Level: `ERROR` (cần người xử lý) / `WARN` (bất thường tự phục hồi) / `INFO` (business event) / `DEBUG` (tắt ở prod)
- **Sampling**: log INFO của endpoint high-traffic sample 10%; ERROR luôn 100% → tiết kiệm 60–70% chi phí log
- Retention: hot 7 ngày (Loki), archive S3 90 ngày (Glacier IR), audit log 1 năm
- **Không log**: password, token, số thẻ, địa chỉ đầy đủ (dùng masking middleware)

### 3.6. Monitoring & Observability
Xem chi tiết ở **Mục 13**. Nguyên tắc:
- **RED** cho service (Rate, Errors, Duration), **USE** cho resource (Utilization, Saturation, Errors)
- **Golden signal + business metric**: `orders_created_total`, `oversell_detected_total` (phải luôn = 0), `saga_compensation_total`, `payment_success_ratio`
- Alert dựa trên **triệu chứng ảnh hưởng user**, không alert trên CPU cao đơn thuần
- Trace 100% cho checkout flow, sampling 1–5% cho traffic thường (tail-based: giữ 100% trace có lỗi hoặc chậm)

### 3.7. Testing
Xem chi tiết ở **Mục 10**. Mục tiêu coverage: domain ≥ 90%, application ≥ 80%, tổng ≥ 75% (và **hiển thị badge thật** từ Codecov).

### 3.8. CI/CD
- PR: lint → unit test → build → scan → contract test → comment coverage diff. **Thời gian < 8 phút** (DevEx là yêu cầu trong JD).
- Trunk-based, feature flag thay vì long-lived branch
- Deploy staging tự động, production cần approval + canary 10% → 50% → 100%
- Rollback tự động khi error rate canary > baseline + 1%

### 3.9. Cost Optimization
- Graviton (ARM) cho mọi node → tiết kiệm ~20% với cùng hiệu năng
- Spot instance cho async worker + CI runner (interruption chấp nhận được) → tiết kiệm ~70%
- Karpenter consolidation, scale node group dev về 0 ngoài giờ
- Aurora Serverless v2 cho dev/staging (min 0.5 ACU)
- S3 lifecycle: Standard → IA (30d) → Glacier IR (90d)
- Log/metric cardinality control (cấm label `user_id` trong Prometheus!)
- **Infracost** chạy trong PR → thấy delta chi phí trước khi merge
- **FinOps metric của chính app**: `cost_per_1k_orders` — đưa lên dashboard Grafana

### 3.10. Maintainability & DevEx
- `make dev` → toàn bộ hệ thống chạy local trong < 3 phút (docker compose)
- Seed data script sinh 100k product, 1M stock movement để test thật
- Code generation: OpenAPI → client/server stub; sqlc cho type-safe SQL; protobuf cho gRPC
- Pre-commit hook: format, lint, secret scan
- ADR bắt buộc cho mọi quyết định không thể đảo ngược dễ dàng

### 3.11. Compliance & Data Privacy
- GDPR-style: API `DELETE /v1/me` → anonymize PII, giữ dữ liệu tài chính (nghĩa vụ kế toán)
- Data residency: dữ liệu tenant EU/RU không rời region (thiết kế cell-based hỗ trợ điều này)
- Audit trail bất biến cho hành động nhạy cảm (refund, stock adjust, role change)

---

## 4. Architecture

### 4.1. Quyết định lớn: Microservices — nhưng chỉ 5–6 service

**Lựa chọn: Microservices "vừa đủ" (right-sized), mỗi service bên trong theo Hexagonal + DDD tactical patterns.**

#### Vì sao không phải Modular Monolith?
JD nói rõ *"maintain APIs in our microservices architecture (Kubernetes)"*. Portfolio phải chứng minh bạn xử lý được **distributed problems**: partial failure, eventual consistency, distributed tracing, contract evolution. Monolith **không tạo ra** những bài toán đó.

#### Vì sao không chia 12+ service?
Đây là điểm khiến bạn khác 90% ứng viên. Lập luận cần nói:

> *"Mỗi service thêm vào là thêm một network hop, một pipeline, một database, một trang on-call. Tôi chia service theo **ranh giới thay đổi và ranh giới scale**, không theo bảng dữ liệu. Inventory tách riêng vì nó có **write pattern hoàn toàn khác** (contention cao, cần lock) và **scale khác** (peak gấp 20x lúc flash sale). Cart không tách riêng vì nó thay đổi cùng nhịp với catalog và không có ràng buộc scale riêng — tách ra chỉ tạo chi phí vận hành mà không đem lại lợi ích."*

#### Tiêu chí tách service đã áp dụng
| Service | Lý do tồn tại độc lập |
|---|---|
| `identity` | Ranh giới bảo mật, compliance riêng, deploy hiếm, ai cũng phụ thuộc |
| `catalog` | Read-heavy 100:1, cache & CDN riêng, team merchandising sở hữu |
| `inventory` | Write contention cao, cần scale & tuning DB riêng, consistency nghiêm ngặt nhất |
| `order` | Orchestrator, state machine phức tạp, sở hữu saga |
| `payment` | Compliance (PCI-scope), tích hợp bên thứ 3, retry/webhook riêng |
| `fulfillment` | Workflow dài hạn (ngày), tích hợp 3PL, team vận hành sở hữu |
| `search` | Read model, scale theo hướng khác hẳn, có thể viết ngôn ngữ khác |

### 4.2. Kiến trúc tổng thể

```mermaid
flowchart TB
    C[Client / Storefront] --> CF[CloudFront + WAF]
    CF --> ALB[ALB / Ingress-NGINX]
    ALB --> GW[API Gateway BFF<br/>authN, rate limit, routing, aggregation]

    GW -->|REST| CAT[catalog-service]
    GW -->|REST| SRCH[search-service]
    GW -->|REST| ORD[order-service]
    GW -->|REST| ID[identity-service]

    ORD -->|gRPC sync| INV[inventory-service]
    ORD -->|gRPC sync| PAY[payment-service]
    ORD -->|gRPC sync| CAT

    CAT --> PGC[(Aurora: catalog)]
    INV --> PGI[(Aurora: inventory)]
    ORD --> PGO[(Aurora: order)]
    PAY --> PGP[(Aurora: payment)]
    FUL[fulfillment-service] --> PGF[(Aurora: fulfillment)]
    ID --> PGD[(Aurora: identity)]

    PGC & PGI & PGO & PGP & PGF -->|Debezium CDC on outbox| K((Kafka / MSK))

    K --> SRCH
    K --> FUL
    K --> ORD
    K --> NOTIF[notification-worker]
    K --> RECON[reconciliation-worker]
    K --> DLQ[(DLQ topic)]

    SRCH --> OS[(OpenSearch)]
    INV --> R[(Redis)]
    GW --> R
    CAT --> S3[(S3 + CloudFront)]
```

### 4.3. Style kiến trúc bên trong mỗi service: Hexagonal + Clean + DDD-lite

**Vì sao Hexagonal (Ports & Adapters)?**
- Domain logic (reservation rule, refund rule) **không biết** gì về Postgres, Kafka, HTTP → test được bằng unit test thuần, chạy trong mili-giây.
- Đổi Kafka → SQS, đổi Stripe → Adyen chỉ cần viết adapter mới. Đây chính là bằng chứng "architectural vision" mà JD yêu cầu.

**Vì sao DDD nhưng "lite"?**
Áp dụng **tactical DDD** (Entity, Value Object, Aggregate, Domain Event, Repository, Domain Service) ở nơi có business logic thật (`inventory`, `order`, `payment`, `fulfillment`). **Không** áp dụng cho service CRUD thuần (`catalog` phần admin) — over-engineering ở đó là điểm trừ, và biết được điều này là điểm cộng.

**Aggregate boundaries đã chọn:**
| Aggregate | Root | Invariant được bảo vệ |
|---|---|---|
| `StockItem` | `(variant_id, warehouse_id)` | `on_hand - reserved - damaged >= 0` |
| `Reservation` | `reservation_id` | Tổng qty, TTL, chỉ chuyển trạng thái hợp lệ |
| `Order` | `order_id` | State machine, tổng tiền = sum(items) + ship - discount |
| `Payment` | `payment_id` | `sum(refunds) <= captured`, transition hợp lệ |
| `ReturnRequest` | `return_id` | Trong 30 ngày, qty ≤ qty đã mua trừ đi đã trả |

> Quy tắc: **1 transaction = 1 aggregate**. Cần thay đổi nhiều aggregate → dùng domain event / saga. Đây là lý do gốc rễ khiến ta cần Saga.

### 4.4. Event-Driven: dùng ở đâu, không dùng ở đâu

| Tình huống | Sync (gRPC) hay Async (Kafka)? | Lý do |
|---|---|---|
| Order → Inventory reserve | **Sync** | Cần biết kết quả ngay để trả lời khách; không reserve được thì không có đơn |
| Order → Payment authorize | **Sync** (trong saga step) | Cần kết quả để quyết định bước tiếp |
| Order → Search index | **Async** | Không ảnh hưởng tính đúng đắn của đơn |
| Order PAID → Fulfillment | **Async** | Fulfillment có thể trễ vài giây, decoupling giúp order không sập theo |
| Order → Notification | **Async** | Không chặn luồng chính |
| Inventory change → Search | **Async** | Eventually consistent là chấp nhận được |

**Nguyên tắc:** *Sync khi cần tính đúng đắn tức thời và có SLA chặt; Async khi cần decoupling và chịu được eventual consistency.* Nói được câu này trong interview là ăn điểm.

### 4.5. Saga: Orchestration (không phải Choreography)

**Chọn Orchestration** vì luồng checkout có **compensation phức tạp và cần quan sát được**. Với choreography, khi đơn hàng "kẹt", không ai biết nó đang ở đâu.

```mermaid
sequenceDiagram
    participant C as Client
    participant O as order-service (Saga)
    participant I as inventory-service
    participant P as payment-service
    participant F as fulfillment-service
    participant K as Kafka

    C->>O: POST /v1/checkout (Idempotency-Key)
    O->>O: Tạo Order(CREATED) + SagaInstance (1 TX)
    O-->>C: 202 Accepted {order_id}

    O->>I: Reserve(reservation_id=order_id, items)
    alt Reserve OK
        I-->>O: RESERVED
        O->>O: Order → RESERVED
        O->>P: Authorize+Capture(idem_key=order_id)
        alt Payment OK
            P-->>O: CAPTURED
            O->>O: Order → PAID (+ outbox: OrderPaid)
            O->>I: Commit(reservation_id)
            O-->>K: OrderPaid
            K->>F: Allocate + create shipment
            F-->>K: ShipmentCreated
            K->>O: Order → ALLOCATED
        else Payment FAIL
            P-->>O: DECLINED
            O->>I: Release(reservation_id)  %% compensation
            O->>O: Order → PAYMENT_FAILED
        end
    else Out of stock
        I-->>O: INSUFFICIENT_STOCK
        O->>O: Order → FAILED(OUT_OF_STOCK)
    end
```

**Bảng compensation (đưa vào README, rất "senior"):**

| Step | Forward action | Compensation | Idempotent? | Retryable? |
|---|---|---|---|---|
| 1 | Reserve stock | Release reservation | ✅ theo `reservation_id` | ✅ |
| 2 | Authorize payment | Void authorization | ✅ theo `idempotency_key` | ✅ (network error) |
| 3 | Capture payment | Refund | ✅ theo `capture_id` | ⚠️ chỉ retry lỗi mạng |
| 4 | Commit stock | Manual restock + alert | ✅ | ✅ |
| 5 | Create shipment | Cancel shipment | ✅ | ✅ |

**Xử lý bước không thể compensate (pivot transaction):** sau khi `capture` thành công, saga chuyển sang **retry-forward-only** — không rollback nữa mà retry đến khi thành công hoặc đưa vào hàng đợi can thiệp thủ công + alert. Đây là kiến thức khiến interviewer gật đầu.

**Saga state được persist** (`saga_instances`, `saga_steps`) → service restart vẫn resume được; có job quét saga `STUCK > 5 phút` → alert.

### 4.6. Transactional Outbox + CDC

**Vấn đề:** không thể vừa `COMMIT` Postgres vừa `publish` Kafka atomically (dual write).

**Giải pháp:** ghi domain event vào bảng `outbox_events` **trong cùng transaction** với thay đổi business. Sau đó relay ra Kafka.

Hai lựa chọn relay, nêu trade-off:

| Cách | Ưu | Nhược | Khi nào chọn |
|---|---|---|---|
| **Polling publisher** (job đọc bảng mỗi 200ms) | Đơn giản, không thêm hạ tầng, dễ chạy local | Tăng tải DB, latency ~200ms | MVP, dev local |
| **Debezium CDC** (đọc WAL) | Latency < 50ms, không đụng DB workload, ordering đảm bảo | Thêm Kafka Connect, phức tạp vận hành | Production, và là điểm cộng portfolio |

→ **Làm cả hai**: interface `OutboxRelay`, chạy polling ở docker-compose, Debezium ở EKS. Ghi vào ADR-006.

**Consumer phải idempotent**: bảng `processed_events(event_id, consumer_group)` với `UNIQUE` constraint → at-least-once delivery + idempotent consumer = **effectively-once**.

### 4.7. Folder Structure

**Repo layout (monorepo — có lý do):**
```
threadline/
├── README.md
├── Makefile                       # make dev / test / lint / migrate / seed / load-test
├── docker-compose.yml
├── docs/
│   ├── adr/                       # ADR-001 .. ADR-012
│   ├── diagrams/                  # C4 (context, container, component), sequence
│   ├── api/                       # OpenAPI 3.1 spec + AsyncAPI cho Kafka topic
│   ├── runbooks/                  # oncall: high-lag, oversell-alert, db-failover
│   └── DEPLOYMENT.md
├── proto/                         # protobuf shared, sinh code bằng buf
├── pkg/                           # shared library (dùng chung, KHÔNG chứa business logic)
│   ├── httpx/                     # middleware: recover, requestid, cors, ratelimit
│   ├── observability/             # otel setup, slog, metrics helper
│   ├── errors/                    # error taxonomy + RFC7807 mapping
│   ├── outbox/                    # outbox writer + relay
│   ├── idempotency/
│   └── resilience/                # retry, circuit breaker, bulkhead
├── services/
│   ├── identity/
│   ├── catalog/
│   ├── inventory/
│   ├── order/
│   ├── payment/
│   ├── fulfillment/
│   ├── search/                    # (tuỳ chọn: Java/Spring Boot để chứng minh polyglot)
│   └── gateway/
├── deploy/
│   ├── helm/                      # 1 generic chart + values per service
│   ├── kustomize/overlays/{dev,staging,prod}
│   ├── argocd/
│   └── terraform/                 # VPC, EKS, Aurora, MSK, ElastiCache, OpenSearch, IAM
├── test/
│   ├── e2e/                       # kịch bản happy + failure
│   ├── load/                      # k6 scripts
│   └── contract/                  # Pact
└── .github/workflows/
```

**Bên trong 1 service (Hexagonal, ví dụ `inventory`):**
```
services/inventory/
├── cmd/
│   ├── api/main.go                # HTTP + gRPC server
│   └── worker/main.go             # kafka consumer + scheduled jobs
├── internal/
│   ├── domain/                    # ❤️ KHÔNG import gì ngoài stdlib
│   │   ├── stock/
│   │   │   ├── stock_item.go      # Aggregate root + invariants
│   │   │   ├── reservation.go
│   │   │   ├── movement.go        # Value object
│   │   │   ├── events.go          # StockReserved, StockReleased...
│   │   │   ├── errors.go          # ErrInsufficientStock...
│   │   │   └── stock_item_test.go # unit test thuần, không cần DB
│   │   └── warehouse/
│   ├── application/               # Use case / orchestration, định nghĩa PORTS
│   │   ├── reserve_stock.go
│   │   ├── release_stock.go
│   │   ├── commit_stock.go
│   │   ├── adjust_stock.go
│   │   └── ports.go               # interface: StockRepository, EventPublisher, Clock, Locker
│   ├── adapters/                  # ADAPTERS (driven side)
│   │   ├── postgres/              # implement StockRepository (sqlc generated)
│   │   ├── redis/                 # hot-sku counter, distributed lock
│   │   ├── kafka/                 # publisher + consumer
│   │   └── metrics/
│   ├── ports/                     # (driving side)
│   │   ├── http/                  # handler, DTO, validation, error mapping
│   │   ├── grpc/
│   │   └── consumer/
│   └── config/
├── migrations/                    # goose/atlas, đánh số tuần tự
├── Dockerfile
└── README.md                      # mỗi service có README riêng!
```

**Quy tắc dependency (ép bằng linter `depguard` / `go-arch-lint` trong CI):**
```
ports ──▶ application ──▶ domain
adapters ──▶ application (chỉ implement interface) ──▶ domain
domain ──▶ (không phụ thuộc ai)
```
> Có CI check kiến trúc là điểm rất mạnh — chứng minh bạn không chỉ "biết Clean Architecture" mà còn **enforce** được nó.

### 4.8. Bảng Trade-off tổng hợp (in ra và học thuộc)

| Quyết định | Chọn | Thay thế | Đánh đổi chấp nhận |
|---|---|---|---|
| Microservices | 6 service | Modular monolith | Phức tạp vận hành ↑, nhưng scale & ownership độc lập |
| DB per service | ✅ | Shared DB | Không JOIN cross-service → phải denormalize/CQRS |
| Saga orchestration | ✅ | Choreography | Orchestrator là single point of logic, nhưng debug/observe dễ hơn nhiều |
| Outbox + CDC | ✅ | Dual write / 2PC | Eventual consistency, thêm Debezium, nhưng không mất event |
| gRPC nội bộ | ✅ | REST nội bộ | Khó debug bằng curl hơn, nhưng nhanh hơn + contract chặt |
| Kafka | ✅ | SQS/SNS | Vận hành nặng hơn, nhưng có ordering + replay + log compaction |
| CQRS cho catalog | ✅ | Query thẳng Postgres | Đồng bộ trễ, code phức tạp hơn, nhưng search p99 < 100ms |
| Async checkout (202) | ✅ | Sync 201 | UX phức tạp hơn (polling/SSE), nhưng chịu tải peak tốt hơn nhiều |
| Go | ✅ | Java/Spring | Ecosystem enterprise mỏng hơn, nhưng image nhỏ + startup nhanh + concurrency tốt |

---

## 5. Database Design

### 5.1. Nguyên tắc
- **Database per service**, không cross-service JOIN. Cần dữ liệu của service khác → gọi API hoặc consume event và giữ **local read model** (chỉ field cần thiết).
- Local dev: 1 Postgres container, mỗi service 1 **schema riêng** + user riêng (không có quyền cross-schema) → mô phỏng đúng ranh giới mà vẫn nhẹ.
- Mọi bảng có `id UUIDv7` (sortable theo thời gian → index locality tốt hơn UUIDv4), `tenant_id`, `created_at`, `updated_at`.
- Tiền tệ: `amount_minor BIGINT` (đơn vị nhỏ nhất) + `currency CHAR(3)`. **Tuyệt đối không dùng FLOAT.**
- Thời gian: `TIMESTAMPTZ`, luôn lưu UTC.

### 5.2. ERD — Inventory (aggregate lõi)

```mermaid
erDiagram
    WAREHOUSES ||--o{ STOCK_ITEMS : holds
    STOCK_ITEMS ||--o{ STOCK_MOVEMENTS : logs
    RESERVATIONS ||--|{ RESERVATION_ITEMS : contains
    STOCK_ITEMS ||--o{ RESERVATION_ITEMS : reserves

    WAREHOUSES {
        uuid id PK
        text tenant_id
        text code UK
        text country_code
        numeric lat
        numeric lng
        text status
    }
    STOCK_ITEMS {
        uuid id PK
        text tenant_id
        text sku
        uuid warehouse_id FK
        int on_hand
        int reserved
        int damaged
        int version
        timestamptz updated_at
    }
    RESERVATIONS {
        uuid id PK
        text tenant_id
        uuid order_id UK
        text status
        timestamptz expires_at
        timestamptz created_at
    }
    RESERVATION_ITEMS {
        uuid id PK
        uuid reservation_id FK
        uuid stock_item_id FK
        text sku
        int quantity
    }
    STOCK_MOVEMENTS {
        uuid id PK
        text tenant_id
        uuid stock_item_id FK
        text movement_type
        int quantity_delta
        text reference_type
        uuid reference_id
        timestamptz occurred_at
    }
```

### 5.3. ERD — Order & Payment

```mermaid
erDiagram
    ORDERS ||--|{ ORDER_ITEMS : contains
    ORDERS ||--o{ ORDER_STATUS_HISTORY : tracks
    ORDERS ||--|| SAGA_INSTANCES : driven_by
    SAGA_INSTANCES ||--|{ SAGA_STEPS : has
    ORDERS ||--o{ PAYMENTS : paid_by
    PAYMENTS ||--o{ REFUNDS : refunded_by
    ORDERS ||--o{ SHIPMENTS : fulfilled_by
    SHIPMENTS ||--|{ SHIPMENT_ITEMS : contains
    ORDERS ||--o{ RETURN_REQUESTS : returned_by

    ORDERS {
        uuid id PK
        text tenant_id
        uuid customer_id
        text status
        bigint subtotal_minor
        bigint discount_minor
        bigint shipping_minor
        bigint tax_minor
        bigint total_minor
        char currency
        jsonb shipping_address
        text idempotency_key UK
        int version
        timestamptz placed_at
    }
    ORDER_ITEMS {
        uuid id PK
        uuid order_id FK
        text sku
        text product_name_snapshot
        jsonb variant_snapshot
        int quantity
        bigint unit_price_minor
        bigint line_total_minor
    }
    PAYMENTS {
        uuid id PK
        uuid order_id FK
        text provider
        text provider_ref UK
        text status
        bigint authorized_minor
        bigint captured_minor
        bigint refunded_minor
        char currency
    }
    SAGA_INSTANCES {
        uuid id PK
        uuid order_id UK
        text saga_type
        text current_step
        text status
        jsonb payload
        int attempt
        timestamptz next_retry_at
    }
```

### 5.4. DDL mẫu (những chỗ có "chất")

```sql
-- =========================================================
-- INVENTORY: bảng nóng nhất hệ thống
-- =========================================================
CREATE TABLE stock_items (
    id            UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id     TEXT      NOT NULL,
    sku           TEXT      NOT NULL,
    warehouse_id  UUID      NOT NULL REFERENCES warehouses(id),
    on_hand       INTEGER   NOT NULL DEFAULT 0,
    reserved      INTEGER   NOT NULL DEFAULT 0,
    damaged       INTEGER   NOT NULL DEFAULT 0,
    version       INTEGER   NOT NULL DEFAULT 0,      -- optimistic locking
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- ❤️ Invariant được DB bảo vệ, không chỉ ở tầng application
    CONSTRAINT chk_non_negative      CHECK (on_hand >= 0 AND reserved >= 0 AND damaged >= 0),
    CONSTRAINT chk_available_ge_zero CHECK (on_hand - reserved - damaged >= 0),
    CONSTRAINT uq_stock_item         UNIQUE (tenant_id, sku, warehouse_id)
);

-- Query chính: "tổng available của sku X trong tenant Y"
CREATE INDEX idx_stock_lookup ON stock_items (tenant_id, sku)
    INCLUDE (warehouse_id, on_hand, reserved, damaged);   -- covering index → index-only scan

-- Chỉ index những dòng thực sự cần cho job low-stock (partial index → nhỏ hơn 100x)
CREATE INDEX idx_stock_low ON stock_items (tenant_id, sku)
    WHERE (on_hand - reserved - damaged) < 10;

-- =========================================================
-- RESERVATION: idempotent theo order_id
-- =========================================================
CREATE TABLE reservations (
    id          UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id   TEXT NOT NULL,
    order_id    UUID NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('ACTIVE','COMMITTED','RELEASED','EXPIRED')),
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_reservation_order UNIQUE (tenant_id, order_id)  -- ❤️ chống double-reserve
);

-- Job expire chỉ quét reservation ACTIVE đã hết hạn
CREATE INDEX idx_reservation_expiry ON reservations (expires_at)
    WHERE status = 'ACTIVE';

-- =========================================================
-- STOCK LEDGER: append-only, partition theo tháng
-- =========================================================
CREATE TABLE stock_movements (
    id             UUID        NOT NULL DEFAULT uuidv7(),
    tenant_id      TEXT        NOT NULL,
    stock_item_id  UUID        NOT NULL,
    movement_type  TEXT        NOT NULL,  -- RECEIVE, RESERVE, RELEASE, COMMIT, ADJUST, RETURN, DAMAGE
    quantity_delta INTEGER     NOT NULL,
    reference_type TEXT,
    reference_id   UUID,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE TABLE stock_movements_2026_08 PARTITION OF stock_movements
    FOR VALUES FROM ('2026-08-01') TO ('2026-09-01');

-- Bảng append-only theo thời gian → BRIN nhỏ hơn B-tree ~100x, đủ tốt cho range scan
CREATE INDEX idx_movements_brin ON stock_movements USING BRIN (occurred_at);
CREATE INDEX idx_movements_item ON stock_movements (stock_item_id, occurred_at DESC);

-- =========================================================
-- OUTBOX
-- =========================================================
CREATE TABLE outbox_events (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    aggregate_type TEXT        NOT NULL,
    aggregate_id   UUID        NOT NULL,
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    headers        JSONB       NOT NULL DEFAULT '{}',   -- chứa traceparent để nối trace qua Kafka
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at   TIMESTAMPTZ
);
CREATE INDEX idx_outbox_unpublished ON outbox_events (created_at)
    WHERE published_at IS NULL;   -- partial index: bảng có 10M dòng, index chỉ vài nghìn

-- =========================================================
-- IDEMPOTENCY (dùng chung ở order/payment)
-- =========================================================
CREATE TABLE idempotency_keys (
    key             TEXT PRIMARY KEY,
    tenant_id       TEXT NOT NULL,
    request_hash    TEXT NOT NULL,       -- phát hiện cùng key nhưng khác payload → 422
    response_status INT,
    response_body   JSONB,
    state           TEXT NOT NULL CHECK (state IN ('IN_PROGRESS','COMPLETED')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);

-- =========================================================
-- ORDERS: partition theo tháng (dữ liệu chỉ tăng)
-- =========================================================
CREATE TABLE orders (
    id              UUID NOT NULL DEFAULT uuidv7(),
    tenant_id       TEXT NOT NULL,
    customer_id     UUID NOT NULL,
    status          TEXT NOT NULL,
    total_minor     BIGINT NOT NULL CHECK (total_minor >= 0),
    currency        CHAR(3) NOT NULL,
    version         INTEGER NOT NULL DEFAULT 0,
    placed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, placed_at)
) PARTITION BY RANGE (placed_at);

-- Query phổ biến nhất: "đơn của tôi, mới nhất trước"
CREATE INDEX idx_orders_customer ON orders (tenant_id, customer_id, placed_at DESC);
-- Dashboard Ops: chỉ quan tâm đơn đang xử lý (partial index cực nhỏ)
CREATE INDEX idx_orders_active ON orders (tenant_id, status, placed_at DESC)
    WHERE status IN ('CREATED','RESERVED','PAID','ALLOCATED');

-- =========================================================
-- ROW LEVEL SECURITY: chống lỗi "quên WHERE tenant_id"
-- =========================================================
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON orders
    USING (tenant_id = current_setting('app.tenant_id', true));
-- Application set `SET LOCAL app.tenant_id = '...'` ở đầu mỗi transaction
```

### 5.5. Bài toán Concurrency — trái tim của project

#### Cách 1 (nền tảng): Atomic conditional UPDATE — không cần lock tường minh
```sql
UPDATE stock_items
SET reserved   = reserved + $qty,
    version    = version + 1,
    updated_at = now()
WHERE tenant_id = $tenant
  AND sku = $sku
  AND warehouse_id = $wh
  AND (on_hand - reserved - damaged) >= $qty   -- ❤️ guard nằm trong chính câu UPDATE
RETURNING version;
```
- `0 rows affected` ⇒ không đủ hàng ⇒ trả `409 INSUFFICIENT_STOCK`.
- Postgres tự lấy **row-level exclusive lock**; các transaction khác chờ rồi **re-evaluate** điều kiện với dữ liệu mới (Read Committed) ⇒ **không thể oversell**.
- Ưu điểm: 1 round-trip, không cần `SELECT FOR UPDATE`, không cần retry loop.

**Câu hỏi bẫy interviewer sẽ hỏi:** *"Read Committed có gây lost update không?"* → Trả lời: Với `UPDATE ... WHERE`, Postgres re-check predicate sau khi lock được giải phóng (EvalPlanQual), nên **không** lost update. Vấn đề chỉ xảy ra khi ta `SELECT` rồi `UPDATE` ở hai câu lệnh riêng biệt — đó là lúc cần `SELECT ... FOR UPDATE` hoặc optimistic version.

#### Chống deadlock khi reserve nhiều SKU
```go
// Luôn khoá theo THỨ TỰ CỐ ĐỊNH (sort theo stock_item_id) trong mọi transaction
sort.Slice(items, func(i, j int) bool { return items[i].StockItemID < items[j].StockItemID })
```
> Deadlock kinh điển: TX A khoá SKU1→SKU2, TX B khoá SKU2→SKU1. Sắp xếp thứ tự khoá là cách rẻ nhất để loại bỏ hoàn toàn.

#### Cách 2: Hot SKU (flash sale) — Redis + sharded counter
Khi 1 SKU nhận > 500 RPS, row-lock Postgres trở thành điểm nghẽn (mọi TX xếp hàng trên 1 dòng).

```lua
-- reserve.lua — atomic trong Redis
local key      = KEYS[1]          -- stock:{tenant}:{sku}:shard:{n}
local qty      = tonumber(ARGV[1])
local idem     = KEYS[2]          -- idem:{reservation_id}
if redis.call('EXISTS', idem) == 1 then return 1 end   -- idempotent
local avail = tonumber(redis.call('GET', key) or '0')
if avail < qty then return -1 end
redis.call('DECRBY', key, qty)
redis.call('SETEX', idem, 900, '1')
return 1
```
- Stock 1000 chia thành **10 shard × 100**; request hash theo `user_id` vào shard → giảm contention 10x.
- Shard cạn → thử shard kế tiếp (max 2 lần) → tránh "false out-of-stock".
- Redis là **nguồn tốc độ**, Postgres vẫn là **nguồn sự thật**: mỗi thao tác đẩy vào Kafka để persist async; **reconciliation worker** so khớp mỗi 30s, lệch > 0 → metric `stock_drift_total` + alert.
- Trade-off phải nói rõ: *"Redis thêm một failure mode. Tôi chấp nhận vì chỉ bật cho SKU hot (chiếm <1% SKU nhưng 60% traffic), và có reconciliation + circuit breaker fallback về Postgres."*

### 5.6. Query Optimization — checklist thực chiến
1. **Bật `pg_stat_statements`**, tìm top 10 query theo `total_exec_time` (không phải theo `mean` — query nhanh nhưng gọi 1M lần mới là thủ phạm).
2. `EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT)` — soi `Seq Scan` trên bảng lớn, `rows` ước lượng lệch thực tế > 10x (⇒ cần `ANALYZE` hoặc tăng `default_statistics_target`).
3. **Covering index + `INCLUDE`** → Index Only Scan (xem `Heap Fetches: 0`).
4. **Partial index** cho cột có phân bố lệch (status, published, unpublished_at IS NULL).
5. **Diệt N+1**: batch query bằng `WHERE id = ANY($1::uuid[])` + dataloader pattern.
6. **Cursor pagination** thay `OFFSET` (OFFSET 100000 phải quét 100k dòng bỏ đi).
7. **Materialized view** cho báo cáo nặng (doanh thu theo ngày), `REFRESH CONCURRENTLY` ban đêm.
8. **JSONB**: index bằng `GIN (attributes jsonb_path_ops)` nếu query theo attribute; nếu chỉ 1 field hay dùng → `CREATE INDEX ON products ((attributes->>'color'))`.
9. **Connection pool**: PgBouncer transaction mode. Công thức khởi điểm `pool = (core × 2) + effective_spindle`; đo bằng `pg_stat_activity` chứ đừng đoán.
10. **Timeout mọi nơi**: `statement_timeout = 3s` (API), `idle_in_transaction_session_timeout = 10s` → transaction treo không giữ lock vô hạn.

**Bằng chứng để bỏ vào portfolio:** file `docs/performance/query-tuning.md` với format:
> *"Query `orders by customer` ban đầu 850ms (Seq Scan 2M rows). Thêm composite index `(tenant_id, customer_id, placed_at DESC)` → 4ms. `EXPLAIN` trước/sau đính kèm."*
> Không gì thuyết phục bằng số liệu trước/sau.

### 5.7. Transaction & Isolation

| Use case | Isolation | Lý do |
|---|---|---|
| Reserve stock | Read Committed + atomic UPDATE | Đủ an toàn, throughput cao nhất |
| Create order + outbox | Read Committed | 1 aggregate, ghi cùng TX |
| Refund (đọc tổng đã refund rồi ghi) | **Repeatable Read** + retry khi `40001` | Chống write skew: 2 refund song song vượt quá captured |
| Báo cáo tài chính cuối ngày | Repeatable Read (snapshot) | Nhất quán tại 1 thời điểm |
| Bulk import | Chunk 1000 dòng/TX | TX dài giữ lock & phình WAL |

**Quy tắc bất di bất dịch:** 🚫 **Không bao giờ gọi HTTP/gRPC bên trong một DB transaction.** Nếu remote call treo 30s, bạn giữ lock 30s → cascading failure toàn hệ thống. Đây là lỗi #1 của mid-level engineer.

### 5.8. Migration Strategy — Expand / Contract (zero downtime)

Đổi cột `orders.total` (INT) → `total_minor` (BIGINT):

| Phase | Hành động | Deploy |
|---|---|---|
| **1. Expand** | `ADD COLUMN total_minor BIGINT NULL` (không NOT NULL, không default → không rewrite bảng) | Migration only |
| **2. Dual write** | App ghi cả 2 cột, đọc từ cột cũ | Release N |
| **3. Backfill** | Job cập nhật theo batch 5k dòng, có sleep, chạy off-peak | Job |
| **4. Switch read** | App đọc `total_minor`, vẫn ghi cả 2 | Release N+1 |
| **5. Stop old write** | Chỉ ghi cột mới | Release N+2 |
| **6. Contract** | `DROP COLUMN total` | Migration only |

**Quy tắc migration:**
- Mọi migration **backward compatible** với version app đang chạy (vì rolling deploy → 2 version cùng tồn tại)
- Chạy migration ở **Kubernetes Job / initContainer với leader election**, không chạy trong app startup của N replica
- `CREATE INDEX CONCURRENTLY` (không khoá bảng); nhớ `lock_timeout = 3s` để migration fail nhanh thay vì khoá cả hệ thống
- Mọi migration có script rollback + được test trên bản snapshot dữ liệu production-size
- Công cụ: **Atlas** hoặc **goose**; lint migration nguy hiểm bằng **squawk** trong CI

---

## 6. API Design

### 6.1. Nguyên tắc
- REST + JSON cho public (dễ debug, cache tốt), **gRPC cho nội bộ** (nhanh, contract chặt, streaming)
- Contract-first: viết OpenAPI 3.1 trước → sinh server stub + client + mock
- URL đặt tên theo **danh từ số nhiều**, không có động từ (trừ action rõ ràng: `/orders/{id}:cancel`)
- Mọi response bọc trong envelope nhất quán
- **Không bao giờ** trả 200 kèm `{"error": ...}`

### 6.2. Danh sách endpoint chính

| Method | Endpoint | Mô tả | Auth |
|---|---|---|---|
| POST | `/v1/auth/register` | Đăng ký | Public |
| POST | `/v1/auth/login` | Lấy access + refresh token | Public |
| POST | `/v1/auth/refresh` | Xoay refresh token | Refresh token |
| POST | `/v1/auth/logout` | Revoke token family | Bearer |
| GET | `/v1/products` | List + filter + sort + cursor page | Public |
| GET | `/v1/products/{id}` | Chi tiết + variants + availability | Public |
| POST | `/v1/admin/products` | Tạo product | `catalog:write` |
| PATCH | `/v1/admin/products/{id}` | Cập nhật (hỗ trợ `If-Match` ETag) | `catalog:write` |
| POST | `/v1/admin/products:bulk-import` | Import CSV → trả `job_id` | `catalog:import` |
| GET | `/v1/search` | Full-text + facet | Public |
| GET | `/v1/cart` / PUT / DELETE | Giỏ hàng | Bearer / guest token |
| POST | `/v1/checkout` | **Tạo đơn (async)** | Bearer + `Idempotency-Key` |
| GET | `/v1/orders` | Đơn của tôi | Bearer |
| GET | `/v1/orders/{id}` | Chi tiết + timeline | Bearer |
| POST | `/v1/orders/{id}:cancel` | Huỷ đơn | Bearer |
| GET | `/v1/orders/{id}/events` | SSE stream trạng thái saga | Bearer |
| POST | `/v1/orders/{id}/returns` | Tạo yêu cầu trả hàng | Bearer |
| POST | `/v1/internal/inventory:reserve` | (gRPC) Reserve | mTLS / client-cred |
| POST | `/v1/webhooks/payments/{provider}` | Webhook PSP | HMAC signature |
| GET | `/v1/ops/orders` | Dashboard Ops | `order:read:all` |
| POST | `/v1/ops/stock-adjustments` | Điều chỉnh tồn | `inventory:adjust` + MFA |

### 6.3. Ví dụ chi tiết — Checkout

**Request**
```http
POST /v1/checkout HTTP/1.1
Authorization: Bearer eyJhbGciOiJSUzI1NiIs...
Idempotency-Key: 018f4a2b-9c3d-7e1f-8a2b-3c4d5e6f7a8b
X-Tenant-ID: zalora-vn
Content-Type: application/json
traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01

{
  "cart_id": "018f4a2b-1111-7000-8000-000000000001",
  "shipping_address": {
    "recipient": "Nguyen Van A",
    "phone": "+84901234567",
    "line1": "123 Le Loi",
    "ward": "Ben Nghe",
    "district": "District 1",
    "city": "Ho Chi Minh City",
    "country_code": "VN",
    "postal_code": "700000"
  },
  "payment_method": { "type": "CARD", "token": "tok_mock_visa_4242" },
  "voucher_code": "SUMMER20",
  "shipping_option_id": "express"
}
```

**Response 202**
```http
HTTP/1.1 202 Accepted
Location: /v1/orders/018f4a2b-2222-7000-8000-000000000002
X-Request-ID: req_01J8...
X-RateLimit-Remaining: 58

{
  "data": {
    "order_id": "018f4a2b-2222-7000-8000-000000000002",
    "order_number": "VN-2026-0000123",
    "status": "CREATED",
    "total": { "amount_minor": 79000000, "currency": "VND" },
    "status_url": "/v1/orders/018f4a2b-2222-7000-8000-000000000002",
    "events_url": "/v1/orders/018f4a2b-2222-7000-8000-000000000002/events"
  },
  "meta": { "request_id": "req_01J8...", "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736" }
}
```

**Response lỗi — RFC 7807 Problem Details**
```http
HTTP/1.1 409 Conflict
Content-Type: application/problem+json

{
  "type": "https://api.threadline.dev/problems/insufficient-stock",
  "title": "Insufficient stock",
  "status": 409,
  "detail": "Một số sản phẩm không còn đủ hàng.",
  "instance": "/v1/checkout",
  "code": "INVENTORY_INSUFFICIENT",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "errors": [
    { "sku": "TSHIRT-BLK-M", "requested": 3, "available": 1 }
  ]
}
```

**Response lỗi validation**
```json
{
  "type": "https://api.threadline.dev/problems/validation-error",
  "title": "Validation failed",
  "status": 422,
  "code": "VALIDATION_ERROR",
  "errors": [
    { "field": "shipping_address.phone", "code": "INVALID_FORMAT", "message": "Số điện thoại không hợp lệ" }
  ]
}
```

### 6.4. Error taxonomy (bảng này rất ghi điểm)

| HTTP | Code | Khi nào | Client nên làm gì |
|---|---|---|---|
| 400 | `MALFORMED_REQUEST` | JSON sai cú pháp | Sửa request |
| 401 | `UNAUTHENTICATED` | Thiếu/hết hạn token | Refresh token |
| 403 | `FORBIDDEN` | Thiếu permission | Không retry |
| 404 | `NOT_FOUND` | Resource không tồn tại | Không retry |
| 409 | `INVENTORY_INSUFFICIENT` | Hết hàng | Cập nhật cart |
| 409 | `IDEMPOTENCY_CONFLICT` | Cùng key, khác payload | Đổi key |
| 409 | `INVALID_STATE_TRANSITION` | Huỷ đơn đã ship | Không retry |
| 412 | `PRECONDITION_FAILED` | `If-Match` ETag lệch | Fetch lại rồi thử lại |
| 422 | `VALIDATION_ERROR` | Sai nghiệp vụ | Sửa dữ liệu |
| 429 | `RATE_LIMITED` | Vượt quota | Retry sau `Retry-After` |
| 500 | `INTERNAL_ERROR` | Bug | Retry backoff, báo `trace_id` |
| 503 | `DEPENDENCY_UNAVAILABLE` | Circuit breaker mở | Retry backoff |
| 504 | `UPSTREAM_TIMEOUT` | Downstream timeout | Retry backoff |

> **Nguyên tắc:** không bao giờ leak stack trace / SQL error ra client. Log chi tiết nội bộ, trả `trace_id` để support tra cứu.

### 6.5. Pagination — Cursor-based

```http
GET /v1/products?limit=24&cursor=eyJwbGFjZWRfYXQiOiIyMDI2LTA4LTEzVDA5OjAwOjAwWiIsImlkIjoiMDE4ZiJ9
```
```json
{
  "data": [ /* ... */ ],
  "page": {
    "limit": 24,
    "next_cursor": "eyJwbGFjZWRfYXQiOiIyMDI2LTA4LTEzVDA4OjU5OjAwWiIsImlkIjoiMDE4ZSJ9",
    "prev_cursor": null,
    "has_more": true
  }
}
```
Cursor = base64 của `{last_sort_value, last_id}` (keyset pagination):
```sql
WHERE (placed_at, id) < ($cursor_time, $cursor_id)
ORDER BY placed_at DESC, id DESC
LIMIT 25;   -- lấy limit+1 để biết has_more
```
**Vì sao không dùng offset?** `OFFSET 100000` bắt Postgres quét và vứt bỏ 100k dòng → O(n). Keyset là O(log n) và **ổn định** khi có dữ liệu mới chèn vào. Vẫn giữ offset cho Admin UI (cần nhảy trang), có giới hạn `offset <= 10000`.

### 6.6. Filtering & Sorting

```http
GET /v1/products
  ?filter[brand]=nike,adidas
  &filter[price][gte]=200000
  &filter[price][lte]=1000000
  &filter[size]=M,L
  &filter[in_stock]=true
  &sort=-discount_percent,price
  &fields=id,name,price,thumbnail
  &include=variants
```
- `-` prefix = DESC
- **Whitelist** field được filter/sort (map sang tên cột), **không bao giờ** ghép chuỗi SQL từ input
- Giới hạn: tối đa 3 field sort, tối đa 20 filter → tránh query gây DoS
- `fields` (sparse fieldset) giảm payload cho mobile

### 6.7. Versioning

- **URI versioning** `/v1/` — hiển thị rõ, dễ route ở gateway, dễ debug
- Thay đổi **additive** (thêm field optional) → không tăng version
- Breaking change → `/v2/`, chạy song song `/v1` tối thiểu **6 tháng**
- Deprecation dùng chuẩn HTTP:
```http
Deprecation: Sun, 01 Nov 2026 00:00:00 GMT
Sunset: Sun, 01 Feb 2027 00:00:00 GMT
Link: <https://docs.threadline.dev/migration/v2>; rel="deprecation"
```
- CI có bước **breaking-change detection** trên OpenAPI (`oasdiff`) và protobuf (`buf breaking`) → PR bị chặn nếu phá contract. **Đây là thứ rất ít portfolio có.**
- Kafka event cũng versioned: dùng **Schema Registry (Avro/Protobuf)** với compatibility mode `BACKWARD`

### 6.8. Các chuẩn khác
- **Idempotency**: bắt buộc `Idempotency-Key` cho mọi POST tạo tài nguyên/tiền. TTL 24h.
- **ETag / If-Match** cho PATCH → chống lost update ở tầng API.
- **Compression**: gzip/brotli cho response > 1KB.
- **`Cache-Control`**: `public, max-age=60, stale-while-revalidate=300` cho catalog; `no-store` cho dữ liệu cá nhân.
- **Content negotiation**: `Accept-Language` → chọn locale của tenant.
- **Long-running**: bulk import trả `202` + `job_id`, poll `GET /v1/jobs/{id}` (async request-reply pattern).

---

## 7. Authentication & Authorization

### 7.1. Tổng quan luồng

```mermaid
sequenceDiagram
    participant U as User
    participant GW as Gateway
    participant ID as identity-service
    participant R as Redis
    participant S as order-service

    U->>ID: POST /v1/auth/login
    ID->>ID: Verify Argon2id, check lockout
    ID->>R: Lưu refresh family {family_id, jti}
    ID-->>U: access(15m, JWT RS256) + refresh(30d, httpOnly cookie)

    U->>GW: GET /v1/orders (Bearer access)
    GW->>GW: Verify chữ ký bằng JWKS (cache 10m)
    GW->>R: Kiểm tra jti có trong blacklist?
    GW->>S: Forward + header X-User-ID, X-Tenant-ID, X-Scopes
    S->>S: Kiểm tra permission ở tầng application
    S-->>U: 200
```

### 7.2. Token design

**Access token — JWT RS256** (asymmetric: service chỉ cần public key, không giữ secret):
```json
{
  "iss": "https://auth.threadline.dev",
  "sub": "018f4a2b-user-id",
  "aud": ["threadline-api"],
  "exp": 1755079200,
  "iat": 1755078300,
  "jti": "018f4a2b-token-id",
  "tid": "zalora-vn",
  "roles": ["customer"],
  "scp": ["order:read:own", "cart:write", "return:create"],
  "ver": 2
}
```
- **Ngắn hạn (15 phút)** → cửa sổ tấn công nhỏ khi token rò rỉ
- **`ver`**: tăng lên khi user đổi mật khẩu / bị đổi role → mọi token cũ vô hiệu ngay
- Không nhét PII vào JWT (JWT chỉ encode, không encrypt!)
- Key rotation qua **JWKS endpoint**, `kid` trong header; xoay 90 ngày, giữ overlap 24h

**Refresh token — opaque, rotation + reuse detection:**
```
Login       → RT1 (family F1)
Dùng RT1    → cấp RT2, RT1 bị đánh dấu USED
Dùng lại RT1 → 🚨 PHÁT HIỆN TẤN CÔNG → revoke TOÀN BỘ family F1 → buộc đăng nhập lại + gửi email cảnh báo
```
Đây là cơ chế OAuth 2.0 BCP; nói được nó là dấu hiệu senior rõ ràng.

- Lưu **hash** của refresh token (SHA-256), không lưu plaintext
- Refresh token trong `HttpOnly; Secure; SameSite=Strict` cookie → JS không đọc được → chống XSS đánh cắp token
- Access token giữ trong memory (không localStorage)

### 7.3. Authorization: RBAC + scope + ownership

**3 tầng kiểm tra:**
1. **Gateway**: token hợp lệ? chưa revoke? (coarse-grained)
2. **Service middleware**: có scope yêu cầu không? (`order:read:all`)
3. **Domain layer**: có sở hữu resource không? (`order.customer_id == subject.id`) — **đây là tầng quan trọng nhất**, chống IDOR/BOLA (OWASP API #1)

```go
// Ví dụ policy trong application layer — KHÔNG đặt ở HTTP handler
func (uc *GetOrderUseCase) Execute(ctx context.Context, sub Subject, orderID uuid.UUID) (*Order, error) {
    o, err := uc.repo.FindByID(ctx, sub.TenantID, orderID)
    if err != nil { return nil, err }

    switch {
    case sub.Has("order:read:all"):                 // CS/Ops
        return o, nil
    case sub.Has("order:read:own") && o.CustomerID == sub.UserID:
        return o, nil
    default:
        // Trả 404 chứ không phải 403 → không leak sự tồn tại của resource
        return nil, ErrNotFound
    }
}
```

**Ma trận quyền:**

| Role | order:read:own | order:read:all | order:cancel | inventory:adjust | payment:refund | catalog:write | tenant:admin |
|---|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| customer | ✅ | | ✅(own) | | | | |
| seller | | ✅(của mình) | | ✅(SKU của mình) | | ✅(của mình) | |
| warehouse_staff | | ✅ | | ✅ | | | |
| cs_agent | | ✅ | ✅ | | ⚠️ cần approve | | |
| finance | | ✅ | | | ✅ | | |
| admin | | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |

**Nâng cao (điểm cộng lớn):** hành động rủi ro cao (`payment:refund > 10.000.000đ`, `inventory:adjust > 100 units`) yêu cầu **maker-checker** (người tạo ≠ người duyệt) + MFA + audit bất biến. Đây là tư duy của người từng làm hệ thống có tiền thật.

### 7.4. Service-to-Service
- **Ưu tiên mTLS** qua service mesh (Linkerd — nhẹ hơn Istio, dễ demo) → identity ở tầng network, không cần token
- Fallback: OAuth2 **Client Credentials**, mỗi service 1 client với scope tối thiểu
- **Không bao giờ** forward access token của user xuống downstream (confused deputy). Dùng **token exchange** (RFC 8693) nếu cần propagate identity.

### 7.5. Bảo vệ tài khoản
- Argon2id (`m=64MB, t=3, p=4`), pepper lưu ở Secrets Manager
- Rate limit đăng nhập: 5 lần/15 phút/account + 20 lần/15 phút/IP, exponential lockout
- Chống user enumeration: response và thời gian phản hồi giống nhau cho email tồn tại/không tồn tại (constant-time)
- Email verify + reset password: token 1 lần, TTL 30 phút, lưu hash
- MFA TOTP bắt buộc cho role nội bộ; recovery code dùng 1 lần
- Session của staff: idle timeout 30 phút, absolute 8 giờ

---

## 8. Performance Optimization

### 8.1. Bản đồ: dùng gì, ở đâu, vì sao

| Kỹ thuật | Áp dụng ở đâu | Vấn đề giải quyết | Trade-off |
|---|---|---|---|
| **Redis cache-aside** | Product detail, category tree, tenant config | DB đọc lặp lại | Stale data → TTL ngắn + invalidate bằng event |
| **In-process LRU** (TTL 5s) | Tenant config, feature flag, JWKS | Round-trip Redis 1ms × 3000 RPS | Không nhất quán giữa các pod trong 5s (chấp nhận được) |
| **Redis counter + Lua** | Hot SKU reservation, rate limit | Row-lock contention | Thêm failure mode → cần reconciliation |
| **Kafka queue** | Order → fulfillment, notification, indexing | Load leveling, decoupling | Eventual consistency |
| **Async job / worker** | Invoice PDF, bulk import, export report, resize ảnh | Request timeout, block user | Cần job status tracking + retry |
| **Background scheduler** | Expire reservation, reconciliation, retry saga | Dữ liệu treo | Cần leader election (chỉ 1 pod chạy) |
| **Connection pool** (PgBouncer) | Mọi service → Aurora | Postgres tốn ~10MB/connection, 500 pod × 20 conn = sập | Transaction mode không hỗ trợ prepared statement mặc định (dùng `protocol=simple` hoặc `statement_cache_mode`) |
| **Batch processing** | Bulk index OpenSearch (1000 doc/lần), bulk insert movement | N round-trip → 1 | Latency tăng, cần flush theo cả size lẫn time (`max(1000 docs, 500ms)`) |
| **Lazy loading + sparse fieldset** | Product list (không load variant/review) | Over-fetching | Client cần thêm request → cân bằng bằng `include=` |
| **singleflight** | Cache miss trên hot key | Thundering herd/cache stampede | Request phải chờ leader |
| **CDN (CloudFront)** | Ảnh, static, product API public | Latency toàn cầu + giảm origin load | Invalidation phức tạp → dùng versioned URL |
| **gRPC + protobuf nội bộ** | Order ↔ Inventory ↔ Payment | JSON parse chiếm 15–30% CPU ở tải cao | Khó debug, cần grpcurl |
| **Read replica routing** | Query báo cáo, order history | Giảm tải primary | Replication lag → read-your-write phải đi primary |

### 8.2. Cache Strategy chi tiết

**Cache key convention:**
```
tl:v1:{tenant}:product:{id}                → TTL 300s
tl:v1:{tenant}:product:{id}:availability   → TTL 10s   (dữ liệu biến động nhanh)
tl:v1:{tenant}:category:tree               → TTL 3600s
tl:v1:cart:{cart_id}                       → TTL 30d
tl:v1:rl:{scope}:{identity}:{window}       → TTL = window
```
Có `v1` trong key → đổi schema chỉ cần bump prefix, không cần flush.

**Invalidation:** cache-aside + **event-driven invalidation**. `catalog-service` publish `ProductUpdated` → mọi pod consume → xoá key local + Redis. Không dựa duy nhất vào TTL.

**Chống 3 bệnh kinh điển của cache:**
| Bệnh | Hiện tượng | Cách chữa |
|---|---|---|
| **Cache Penetration** | Query key không tồn tại liên tục (bot dò) → đấm thẳng DB | Cache negative result (TTL 30s) + Bloom filter |
| **Cache Breakdown** | 1 hot key hết hạn, 5000 request cùng miss | `singleflight` + logical expiry (refresh nền trước khi hết hạn) |
| **Cache Avalanche** | Hàng loạt key hết hạn cùng lúc | TTL + jitter ngẫu nhiên ±10% |

### 8.3. Application-level
- **Zero-allocation hot path**: `sync.Pool` cho buffer, tránh alloc trong loop; profile bằng `pprof`, tối ưu dựa trên flame graph **chứ không đoán**
- **Preallocate slice** khi biết size: `make([]T, 0, n)`
- **`sonic`/`jsoniter`** thay `encoding/json` nếu profile cho thấy JSON là bottleneck
- **`errgroup`** để gọi song song các dependency độc lập (fan-out): checkout gọi pricing + shipping-quote song song → tiết kiệm 40ms
- **Context deadline propagation**: request có deadline 3s → mọi downstream call kế thừa deadline còn lại, không để "zombie request" chạy tiếp khi client đã bỏ đi

### 8.4. Kịch bản benchmark bắt buộc phải có trong repo
```
test/load/
├── 01-catalog-read.js       # 2000 RPS, 5 phút, kiểm p99 < 80ms
├── 02-search.js             # 1000 RPS
├── 03-checkout-normal.js    # 300 RPS, 10 phút
├── 04-flashsale.js          # ⭐ 10k VU tranh 100 units trong 30s
├── 05-soak.js               # 200 RPS × 2 giờ → phát hiện memory leak, connection leak
└── 06-spike.js              # 50 RPS → 2000 RPS trong 10s → đo thời gian HPA phản ứng
```
**Kịch bản 04 là "signature" của portfolio.** Kết thúc test, assert:
```javascript
check(summary, {
  'không oversell': () => finalStock.on_hand >= 0,
  'tổng đơn thành công == 100': () => successOrders === 100,
  'p99 < 500ms': () => p99 < 500,
});
```
Chụp lại kết quả + dashboard Grafana → đưa vào README. **Đây là bằng chứng khiến CV của bạn khác biệt hoàn toàn.**

---

## 9. Security

### 9.1. OWASP Top 10 — checklist có thể kiểm chứng

| # | Rủi ro | Biện pháp trong Threadline | Test chứng minh |
|---|---|---|---|
| A01 | Broken Access Control | Ownership check ở domain layer; RLS ở Postgres; trả 404 thay 403 | E2E: user A đọc order của user B → 404 |
| A02 | Cryptographic Failures | TLS 1.3, Argon2id, KMS at-rest, column encryption cho PII | Test: DB dump không lộ email plaintext |
| A03 | Injection | sqlc/prepared statement 100%; whitelist cột sort/filter; validate input | Test: `?sort=id;DROP TABLE` → 400 |
| A04 | Insecure Design | Threat model (STRIDE) trong `docs/security/`; rate limit; maker-checker | Tài liệu + test |
| A05 | Security Misconfiguration | Distroless, non-root, read-only FS, no default cred, header bảo mật | `trivy` + `kube-score` trong CI |
| A06 | Vulnerable Components | Dependabot + `govulncheck` + Trivy, block CVE HIGH | CI fail có bằng chứng |
| A07 | Auth Failures | Lockout, MFA, refresh rotation + reuse detection, session timeout | Test reuse RT → cả family bị revoke |
| A08 | Software/Data Integrity | Image ký bằng `cosign`, SBOM, admission policy chỉ chạy image đã ký | Kyverno policy |
| A09 | Logging & Monitoring Failures | Audit log bất biến, alert trên auth failure spike | Alert rule + runbook |
| A10 | SSRF | Không fetch URL từ user; nếu cần → allowlist domain + chặn IP nội bộ (169.254.169.254!) | Test chặn metadata endpoint |

### 9.2. Chi tiết từng mối đe doạ

**SQL Injection**
```go
// ✅ sqlc sinh code parameterized, không bao giờ nối chuỗi
rows, err := q.ListOrders(ctx, ListOrdersParams{TenantID: t, CustomerID: c, Limit: l})

// Sort động — chỉ cách duy nhất được phép
var sortWhitelist = map[string]string{
    "price":      "price_minor",
    "created_at": "placed_at",
    "discount":   "discount_percent",
}
col, ok := sortWhitelist[userInput]
if !ok { return ErrInvalidSort }   // ❤️ whitelist, không phải blacklist/escape
```

**XSS** — API JSON thuần nên bề mặt tấn công nhỏ, nhưng vẫn:
- `Content-Type: application/json` + `X-Content-Type-Options: nosniff`
- Sanitize HTML trong product description (bleach/bluemonday) **khi ghi**, và escape khi hiển thị
- CSP header: `default-src 'self'; frame-ancestors 'none'`
- Không bao giờ trả HTML từ input người dùng

**CSRF**
- API dùng Bearer token trong header → **miễn nhiễm CSRF** (browser không tự gắn header)
- Nhưng refresh token nằm trong cookie → cần `SameSite=Strict` + double-submit token cho endpoint `/auth/refresh`
- Admin UI (nếu có session cookie): CSRF token bắt buộc

**CORS** — cấu hình chặt, không dùng wildcard:
```go
AllowedOrigins:   []string{"https://zalora.vn", "https://theiconic.com.au"},  // từ tenant config
AllowedMethods:   []string{"GET","POST","PATCH","DELETE","OPTIONS"},
AllowedHeaders:   []string{"Authorization","Content-Type","Idempotency-Key","X-Tenant-ID","traceparent"},
AllowCredentials: true,     // ⚠️ AllowCredentials=true + Origin="*" là lỗ hổng nghiêm trọng
MaxAge:           600,
```

**Security headers (ở gateway):**
```
Strict-Transport-Security: max-age=63072000; includeSubDomains; preload
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: geolocation=(), camera=(), microphone=()
```

**Secret Management**
- AWS Secrets Manager + **External Secrets Operator** → sync thành k8s Secret
- **IRSA** (IAM Roles for Service Accounts): pod lấy quyền AWS qua OIDC, **không có access key nào tồn tại**
- Auto-rotate DB credential 30 ngày (Secrets Manager rotation lambda)
- `gitleaks` chạy pre-commit + CI; secret lỡ commit → coi như đã lộ, phải rotate
- Sealed Secrets hoặc SOPS nếu cần commit config có mã hoá

**Encryption**
| Loại | Cách |
|---|---|
| In transit (edge) | TLS 1.3, ACM cert, HSTS preload |
| In transit (nội bộ) | mTLS qua Linkerd, cert tự xoay 24h |
| At rest | KMS CMK cho RDS/S3/EBS/MSK, key policy tách biệt theo môi trường |
| Column-level | Email/phone/address mã hoá bằng AES-256-GCM, khoá từ KMS; giữ `email_hash` (HMAC-SHA256) để vẫn query được |
| Password | Argon2id + salt riêng + pepper |
| Card data | **Không bao giờ chạm vào** — chỉ nhận token từ PSP (giảm PCI scope xuống SAQ-A) |

**Rate Limiting — nhiều tầng**
| Tầng | Ngưỡng | Công cụ |
|---|---|---|
| Edge | 2000 req/5min/IP | AWS WAF |
| Gateway (global) | 1000 req/min/user, 100/min/IP ẩn danh | Redis sliding window |
| Endpoint nhạy cảm | login 5/15min, checkout 10/min, refund 3/min | Token bucket theo scope |
| Backend protection | Concurrency limit + adaptive load shedding | Bulkhead pattern |

```http
HTTP/1.1 429 Too Many Requests
Retry-After: 42
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1755078400
```

**Chống lạm dụng nghiệp vụ (business logic abuse)** — phần rất ít ai làm, cực ấn tượng:
- Bot ôm hàng flash sale → giới hạn qty/user/SKU, device fingerprint, CAPTCHA có điều kiện
- Voucher farming → 1 voucher/user, kiểm tra theo device + payment method chứ không chỉ account
- Return abuse → theo dõi tỉ lệ trả hàng theo user, cờ đỏ khi bất thường
- Price scraping → rate limit theo pattern truy cập, không chỉ theo count

**Container & K8s security**
```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities: { drop: ["ALL"] }
  seccompProfile: { type: RuntimeDefault }
```
+ NetworkPolicy default-deny, chỉ mở đúng chiều cần thiết
+ Kyverno/OPA Gatekeeper chặn pod không đạt chuẩn
+ Resource limits bắt buộc (chống noisy neighbor)

---

## 10. Testing Strategy

### 10.1. Test Pyramid (với con số thật)

```
        /\        E2E (~25 test)          — chậm, giòn, chỉ luồng quan trọng nhất
       /  \       Contract (~30)          — Pact, chống breaking change giữa service
      /    \      Integration (~150)      — testcontainers: Postgres, Redis, Kafka thật
     /      \     Unit (~800)             — domain thuần, chạy < 5 giây toàn bộ
    /________\    + Load, Chaos, Security scan
```

### 10.2. Unit Test — nơi Hexagonal trả cổ tức
```go
func TestStockItem_Reserve(t *testing.T) {
    tests := []struct {
        name     string
        onHand   int
        reserved int
        request  int
        wantErr  error
    }{
        {"đủ hàng",            10, 0, 5,  nil},
        {"vừa đủ",             10, 5, 5,  nil},
        {"thiếu 1 đơn vị",     10, 6, 5,  stock.ErrInsufficientStock},
        {"qty âm",             10, 0, -1, stock.ErrInvalidQuantity},
        {"qty bằng 0",         10, 0, 0,  stock.ErrInvalidQuantity},
        {"tất cả đã reserved", 10, 10, 1, stock.ErrInsufficientStock},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            item := stock.NewStockItem("SKU-1", wh, tt.onHand, tt.reserved)
            err := item.Reserve(tt.request)
            require.ErrorIs(t, err, tt.wantErr)
            if err == nil {
                assert.Equal(t, tt.reserved+tt.request, item.Reserved())
                assert.Len(t, item.PendingEvents(), 1)   // domain event được sinh ra
            }
        })
    }
}
```
> Không có DB, không có mock phức tạp, chạy trong micro-giây. **Đây là lý do tồn tại của Clean Architecture** — và bạn nên nói đúng câu này khi được hỏi.

**Property-based testing** (điểm cộng): với mọi chuỗi thao tác `Reserve/Release/Commit` ngẫu nhiên, invariant `on_hand - reserved >= 0` **luôn** đúng. Dùng `rapid` hoặc `gopter`.

### 10.3. Integration Test — testcontainers
```go
func TestReserveStock_Concurrent_NoOversell(t *testing.T) {
    ctx := context.Background()
    pg := testcontainers.StartPostgres(t)          // Postgres THẬT, không mock
    repo := postgres.NewStockRepository(pg.Pool)
    uc := application.NewReserveStock(repo, ...)

    seedStock(t, pg, "SKU-HOT", 100)

    const goroutines = 500
    var success, failed atomic.Int64
    var wg sync.WaitGroup
    for i := 0; i < goroutines; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            err := uc.Execute(ctx, ReserveCmd{ReservationID: uuid.New(), SKU: "SKU-HOT", Qty: 1})
            if err == nil { success.Add(1) } else { failed.Add(1) }
        }(i)
    }
    wg.Wait()

    assert.EqualValues(t, 100, success.Load())   // ❤️ đúng 100, không hơn
    assert.EqualValues(t, 400, failed.Load())
    assert.EqualValues(t, 100, readReserved(t, pg, "SKU-HOT"))
}
```
Test này chính là bằng chứng bạn hiểu concurrency. **Ghim nó trong README.**

Các integration test khác:
- Outbox: commit TX → event xuất hiện trong bảng → relay đẩy lên Kafka → consumer nhận
- Idempotency: gọi checkout 2 lần cùng key → chỉ 1 order được tạo
- Saga compensation: giả lập payment fail → reservation được release (verify state trong DB)
- Migration: apply toàn bộ migration lên DB rỗng + lên snapshot có dữ liệu

### 10.4. Contract Test (Pact)
- `order-service` (consumer) định nghĩa kỳ vọng với `inventory-service` (provider)
- Pact file publish lên Pact Broker; provider verify trong CI
- **`can-i-deploy`** gate: không cho deploy nếu contract chưa được verify
> Giải quyết bài toán thật của microservices: *"team A đổi API, team B sập lúc 2h sáng"*.

### 10.5. E2E Test
Chạy trên docker-compose đầy đủ, kịch bản:
1. Register → login → browse → add to cart → checkout → payment success → order PAID → shipment created → delivered → complete
2. Checkout khi hết hàng → 409, reservation không tạo
3. Payment declined → order FAILED, stock được trả lại (verify qua API)
4. Cancel trước ship → refund + restock
5. Return flow: request → inspection accepted → refund + restock
6. Idempotent checkout: gửi 5 request cùng key song song → đúng 1 order
7. Token refresh reuse → cả family bị revoke

### 10.6. Mock/Stub strategy
| Đối tượng | Cách xử lý | Vì sao |
|---|---|---|
| Postgres, Redis, Kafka | **Testcontainers (thật)** | Mock DB tạo cảm giác an toàn giả; bug SQL không bị bắt |
| PSP (Stripe...) | **WireMock/mock server** có contract | Không gọi bên thứ 3 trong CI |
| Time (`Clock` port) | Fake clock | Test TTL reservation không cần `sleep(15 phút)` |
| Random/UUID | Injectable generator | Test deterministic |
| Service nội bộ (unit test) | Interface mock (`mockery`) | Nhanh, cô lập |

### 10.7. Mục tiêu Coverage (thực tế, không sáo rỗng)

| Layer | Target | Ghi chú |
|---|---|---|
| `domain/` | **≥ 90%** | Đây là nơi có logic, phải kín |
| `application/` | ≥ 80% | Use case + error path |
| `adapters/` | ≥ 60% | Cover bằng integration test |
| `ports/http` | ≥ 70% | Validation + error mapping |
| **Tổng repo** | **≥ 75%** | Badge Codecov thật trong README |

> Nói trong interview: *"Tôi không đặt mục tiêu 100% vì nó khuyến khích viết test vô nghĩa cho getter. Tôi đo **mutation score** cho domain package — nó cho biết test có thực sự bắt được bug hay không, thay vì chỉ chạy qua dòng code."*

**Bổ sung:**
- **Mutation testing**: `go-mutesting` cho `domain/`, target ≥ 70% killed
- **Fuzz test**: parser cursor pagination, webhook signature verify
- **Flaky test policy**: test flaky bị quarantine + tạo issue trong 24h, không được `t.Skip` im lặng
- CI chạy `-race` bắt buộc → phát hiện data race

### 10.8. Chaos Testing
| Thí nghiệm | Giả thuyết | Cách làm |
|---|---|---|
| Kill 1 pod order-service giữa saga | Saga resume sau restart, không mất đơn | `kubectl delete pod` trong lúc load test |
| Inventory latency +2s (Toxiproxy) | Circuit breaker mở, checkout trả 503 có kiểm soát, không cascade | Toxiproxy |
| Kafka down 60s | Outbox tích luỹ, tự publish lại khi Kafka trở lại, không mất event | Stop broker |
| Aurora failover | App reconnect < 30s, không mất transaction đã commit | RDS failover API |
| Redis down | Fallback về Postgres, latency tăng nhưng vẫn phục vụ | Stop Redis |

---

## 11. DevOps

### 11.1. Dockerfile (multi-stage, distroless)
```dockerfile
# ---- builder ----
FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download   # cache layer
COPY . .
ARG SERVICE VERSION COMMIT
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/app ./services/${SERVICE}/cmd/api

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/app /app
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/app"]
```
Kết quả: image **~15MB**, không shell, không package manager → bề mặt tấn công gần bằng 0. Nói được con số này rất ấn tượng.

### 11.2. docker-compose (DevEx — JD nhắc "developer experience")
```yaml
# Mục tiêu: `make dev` → toàn hệ thống chạy < 3 phút
services:
  postgres:      # 1 instance, nhiều schema
  redis:
  kafka:         # KRaft mode, không cần Zookeeper
  schema-registry:
  opensearch:
  otel-collector:
  prometheus: / grafana: / loki: / tempo:
  jaeger:        # (tuỳ chọn, hoặc dùng Tempo)
  localstack:    # giả lập S3, SQS, Secrets Manager → dev không tốn tiền AWS
  mock-psp:      # WireMock
  # + 6 service của Threadline với hot-reload (air)
```
Kèm `make seed` sinh 100k product / 50 warehouse / 1M stock movement → test với dữ liệu thật chứ không phải 10 dòng.

### 11.3. GitHub Actions

**`pr.yml` — mục tiêu < 8 phút**
```yaml
jobs:
  detect-changes:      # dorny/paths-filter → chỉ chạy CI cho service bị đổi (monorepo)
  lint:                # golangci-lint, gofumpt, buf lint, hadolint, sqlfluff
  arch-check:          # go-arch-lint: domain không được import adapters
  unit-test:           # matrix theo service, -race -cover
  integration-test:    # testcontainers (cache Docker layer)
  contract-test:       # pact publish + can-i-deploy
  build:               # docker buildx, multi-arch (amd64 + arm64/Graviton)
  security:            # trivy fs + image, govulncheck, gitleaks, checkov (terraform)
  api-compat:          # oasdiff breaking + buf breaking → chặn breaking change
  infracost:           # comment delta chi phí AWS lên PR
  coverage-report:     # comment coverage diff, fail nếu giảm > 1%
```

**`main.yml` — deploy**
```yaml
build-push:
  - docker buildx build --platform linux/amd64,linux/arm64
  - push ECR, tag = git SHA (KHÔNG dùng :latest ở production)
  - cosign sign + generate SBOM (syft) + attest
gitops:
  - cập nhật image tag trong repo deploy/ → ArgoCD tự sync staging
smoke-test:
  - chạy E2E rút gọn trên staging
promote-prod:
  - environment protection: cần approval
  - Argo Rollouts canary: 10% (5m) → 50% (5m) → 100%
  - analysis template: error rate & p99 của canary vs baseline → tự rollback nếu xấu
```

### 11.4. Kubernetes
```yaml
# Điểm nhấn trong manifest
spec:
  replicas: 3
  strategy: { rollingUpdate: { maxSurge: 25%, maxUnavailable: 0 } }
  topologySpreadConstraints:      # trải pod qua nhiều AZ
    - maxSkew: 1
      topologyKey: topology.kubernetes.io/zone
      whenUnsatisfiable: DoNotSchedule
  containers:
    - resources:
        requests: { cpu: 100m, memory: 128Mi }
        limits:   { memory: 256Mi }      # ❤️ KHÔNG set CPU limit → tránh CFS throttling
      livenessProbe:  { httpGet: { path: /healthz } }   # không check dependency!
      readinessProbe: { httpGet: { path: /readyz  } }   # có check DB/Kafka
      startupProbe:   { failureThreshold: 30, periodSeconds: 2 }
      lifecycle:
        preStop: { exec: { command: ["sleep", "10"] } }  # chờ endpoint được gỡ khỏi LB
```
Kèm theo: **PodDisruptionBudget** (`minAvailable: 2`), **HPA** (CPU 70% + custom metric RPS), **KEDA** cho worker (scale theo Kafka lag), **NetworkPolicy** default-deny, **ServiceAccount + IRSA**.

> **Chi tiết ăn điểm:** giải thích vì sao không set CPU limit — CFS throttling gây p99 latency spike dù CPU trung bình thấp. Rất ít người biết điều này.

### 11.5. Nginx / Reverse Proxy
- **Production**: AWS ALB → Ingress-NGINX Controller (rate limit, mTLS passthrough, header injection)
- **Local**: Nginx container làm reverse proxy để mô phỏng gần giống production
```nginx
location /v1/ {
    proxy_pass http://gateway:8080;
    proxy_http_version 1.1;
    proxy_set_header X-Request-ID $request_id;    # sinh request id nếu client chưa có
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_connect_timeout 2s;
    proxy_read_timeout 10s;
    proxy_next_upstream error timeout http_502 http_503;
    limit_req zone=api burst=20 nodelay;
    gzip on; gzip_types application/json;
}
```

### 11.6. IaC — Terraform
```
deploy/terraform/
├── modules/{network,eks,aurora,msk,elasticache,opensearch,observability,iam}
└── envs/{dev,staging,prod}/     # backend S3 + DynamoDB lock, workspace tách biệt
```
- `terraform plan` chạy trong PR, comment kết quả; `apply` cần approval
- `checkov` + `tfsec` quét policy; `infracost` ước tính chi phí
- Mọi resource có tag: `Environment`, `Service`, `Owner`, `CostCenter`

---

## 12. Cloud (AWS)

### 12.1. Bản đồ dịch vụ

| Nhu cầu | Dịch vụ AWS | Vì sao chọn (và giải pháp thay thế đã cân nhắc) |
|---|---|---|
| Container orchestration | **EKS** (managed node group + Karpenter) | JD yêu cầu Kubernetes. *ECS Fargate rẻ và đơn giản hơn nhưng không thể hiện được kỹ năng k8s — với portfolio nhắm JD này, EKS là bắt buộc.* |
| Compute node | **EC2 Graviton (m7g/c7g) + Spot cho worker** | ~20% rẻ hơn với cùng hiệu năng; Spot tiết kiệm ~70% cho workload chịu gián đoạn |
| Database | **Aurora PostgreSQL** (1 writer + 1 reader) | Storage tự scale, failover < 30s, PITR, cloning nhanh cho staging. *RDS thường rẻ hơn — dùng cho dev.* |
| Dev/staging DB | **Aurora Serverless v2** (min 0.5 ACU) | Scale về gần 0 ngoài giờ → tiết kiệm rất nhiều |
| Cache | **ElastiCache for Redis** (cluster mode, 2 shard) | Managed, multi-AZ failover |
| Message bus | **Amazon MSK** (hoặc MSK Serverless) | Kafka managed. *Kinesis rẻ hơn nhưng không có ecosystem Debezium/Connect; SQS đơn giản nhưng thiếu ordering theo key + replay.* |
| CDC | **MSK Connect + Debezium** | Đọc WAL của Aurora, không đụng workload chính |
| Search | **OpenSearch Service** | Managed Elasticsearch fork |
| Object storage | **S3** (+ lifecycle → IA → Glacier IR) | Media, invoice, log archive, PSP statement |
| CDN | **CloudFront** | Cache ảnh & catalog API ở edge, giảm latency cho SEA/LATAM/ANZ |
| WAF & DDoS | **AWS WAF + Shield Standard** | Rate-based rule, managed rule set (SQLi/XSS), bot control |
| DNS | **Route 53** (latency-based routing) | Multi-region roadmap |
| Registry | **ECR** (scan on push, lifecycle policy) | Giữ 10 image gần nhất |
| Secret | **Secrets Manager** + **External Secrets Operator** | Auto-rotation; Parameter Store cho config không nhạy cảm (rẻ hơn) |
| Encryption key | **KMS** (CMK riêng theo môi trường) | Key policy tách biệt, audit qua CloudTrail |
| Identity | **IAM + IRSA (OIDC)** | Pod lấy quyền tạm thời — không có static credential nào |
| Async đơn giản | **SQS + SNS** | DLQ, fanout cho notification (rẻ hơn Kafka cho use case đơn giản) |
| Scheduled job | **EventBridge Scheduler** → k8s Job | Cron tập trung, có retry |
| Serverless nhỏ | **Lambda** | Xử lý ảnh (S3 trigger), rotate secret, cleanup |
| Metrics | **Amazon Managed Prometheus (AMP)** | Không phải tự vận hành Prometheus HA |
| Dashboard | **Amazon Managed Grafana (AMG)** | Tích hợp sẵn AMP/CloudWatch/X-Ray |
| Log | **CloudWatch Logs** (short) + **S3** (archive) hoặc Loki self-host | Cân bằng chi phí |
| Trace | **AWS X-Ray** hoặc **Tempo self-host** | X-Ray managed; Tempo rẻ hơn khi volume lớn |
| CI/CD | **GitHub Actions** + OIDC → IAM role | Không cần lưu AWS key trong GitHub |
| Cost | **Cost Explorer + Budgets + Compute Optimizer** | Alert khi vượt ngân sách |

### 12.2. Sơ đồ network
```
Route53 → CloudFront (WAF) → ALB (public subnet, 3 AZ)
                                 ↓
                   EKS worker nodes (private subnet, 3 AZ)
                                 ↓
        ┌────────────┬───────────┴─────────┬─────────────┐
    Aurora        ElastiCache            MSK         OpenSearch
  (private,      (private)            (private)      (private)
   isolated)
                                 ↓
                 VPC Endpoints (S3, ECR, Secrets Manager, KMS)
                   → không đi qua NAT Gateway → tiết kiệm ~$100/tháng
```
> Chi tiết ăn điểm: **NAT Gateway data processing cost là thủ phạm chi phí ẩn phổ biến nhất.** Dùng VPC Endpoint cho S3/ECR/DynamoDB (gateway endpoint miễn phí) là một trong những tối ưu chi phí dễ nhất và ít người biết.

### 12.3. ⚠️ Chiến lược chi phí cho Portfolio (rất quan trọng — thực tế)

Chạy full stack trên AWS 24/7 tốn **~$600–900/tháng**. Với portfolio cá nhân, đừng làm vậy:

| Môi trường | Cách chạy | Chi phí |
|---|---|---|
| **Dev hằng ngày** | `kind`/`k3d` local + LocalStack (S3/SQS/Secrets) + docker-compose | **$0** |
| **Chứng minh IaC** | Viết Terraform đầy đủ, chạy `terraform plan` trong CI, **không apply** | **$0** |
| **Demo thật** | `terraform apply` → quay video demo + chụp Grafana → `terraform destroy` trong 3–6 giờ | **~$5–15/lần** |
| **Luôn bật (nếu muốn)** | 1 EC2 t4g.medium Spot chạy k3s + Postgres/Redis/Kafka nhẹ | **~$8/tháng** |

> Nói được điều này trong interview là điểm cộng lớn: *"Tôi thiết kế cho production trên EKS, nhưng tối ưu chi phí bằng cách phát triển trên k3d + LocalStack và chỉ apply hạ tầng thật khi demo. Terraform giữ nguyên, chỉ khác backend và kích thước instance."* → Đây chính là tư duy **cost-conscious engineer** mà mục "Cost Optimization" trong JD ngầm yêu cầu.

---

## 13. Monitoring & Observability

### 13.1. Kiến trúc
```mermaid
flowchart LR
    A[Service<br/>OTel SDK] -->|OTLP gRPC| C[OTel Collector<br/>DaemonSet]
    C -->|metrics| P[(Prometheus / AMP)]
    C -->|logs| L[(Loki)]
    C -->|traces, tail-sampling| T[(Tempo / Jaeger)]
    P & L & T --> G[Grafana]
    P --> AM[Alertmanager] --> S[Slack / PagerDuty]
```
**Một SDK (OpenTelemetry) cho cả 3 tín hiệu** → không lock-in backend, đổi Jaeger sang Tempo chỉ là đổi config collector.

### 13.2. Metrics

**RED cho mọi service (auto qua middleware):**
```
http_requests_total{service,method,route,status,tenant}
http_request_duration_seconds_bucket{...}     # histogram, không phải summary (để tính p99 toàn cục)
grpc_server_handled_total{...}
```
**USE cho tài nguyên:** CPU, memory, goroutines, GC pause, connection pool (`in_use`, `idle`, `wait_count`, `wait_duration`).

**Business metrics — thứ khiến dashboard của bạn khác mọi người:**
```promql
orders_created_total{tenant,status}
order_value_minor_sum{tenant,currency}
checkout_conversion_ratio
inventory_reservations_total{result="success|insufficient"}
oversell_detected_total                  # 🚨 PHẢI LUÔN = 0, alert ngay khi > 0
stock_drift_total{sku}                   # lệch giữa Redis và Postgres
saga_duration_seconds{saga_type,outcome}
saga_compensations_total{step,reason}
saga_stuck_total                         # saga treo > 5 phút
payment_attempts_total{provider,result}
kafka_consumer_lag{topic,group}
outbox_unpublished_age_seconds           # event cũ nhất chưa publish
cost_per_1k_orders_usd                   # FinOps metric
```

> ⚠️ **Cardinality**: KHÔNG bao giờ đặt `user_id`, `order_id`, `sku` làm label Prometheus. 1M SKU × 5 label = nổ TSDB. Dùng exemplar để nối metric → trace thay vì tăng cardinality.

### 13.3. Tracing
- **Instrument tự động**: HTTP, gRPC, `database/sql`, Redis, Kafka producer/consumer
- **Trace xuyên Kafka**: nhét `traceparent` vào Kafka header (và vào cột `headers` của outbox) → **trace liên tục từ HTTP request → DB → outbox → Kafka → consumer → service khác**. Đây là thứ khó và rất ít portfolio làm được.
- **Span attributes** hữu ích: `tenant.id`, `order.id`, `saga.step`, `stock.sku`, `db.statement` (đã sanitize)
- **Tail-based sampling** ở collector: giữ 100% trace có error hoặc latency > p99, sample 1% trace bình thường → giảm ~95% chi phí lưu trữ mà không mất trace quan trọng

### 13.4. Logging
- Loki với label **thấp cardinality**: `{service, env, level, tenant}` — nội dung chi tiết nằm trong log line, query bằng LogQL
- Grafana: từ log → click `trace_id` → nhảy sang Tempo → xem span → click exemplar → xem metric. **Correlation 3 chiều.** Quay GIF cảnh này bỏ vào README, cực kỳ ấn tượng.

### 13.5. Dashboards (đưa JSON vào repo `deploy/grafana/dashboards/`)
1. **Service Health** — RED theo service, error budget burn rate
2. **Checkout Funnel** — cart → checkout → reserved → paid → shipped, conversion từng bước
3. **Inventory** — reservation rate, insufficient rate, hot SKU, drift, tuổi reservation
4. **Saga** — thời lượng, tỉ lệ compensation, saga treo, phân bố theo step
5. **Kafka** — lag theo consumer group, throughput, DLQ depth
6. **Database** — QPS, slow query, connection pool, replication lag, cache hit ratio
7. **Cost** — chi phí theo service, cost per 1k orders

### 13.6. SLO & Alert

| SLI | SLO | Cách đo |
|---|---|---|
| Availability API đọc | 99.95% | `1 - (5xx / total)` trên 30 ngày |
| Latency checkout | 99% request < 300ms | histogram quantile |
| Order success rate | ≥ 99.5% | `orders PAID / orders CREATED` (loại trừ out-of-stock hợp lệ) |
| Freshness search index | 95% < 30s | so `indexed_at - event_time` |

**Alert dựa trên triệu chứng, dùng multi-window burn rate** (Google SRE):
```yaml
- alert: CheckoutErrorBudgetBurnFast
  expr: |
    (1 - (sum(rate(http_requests_total{route="/v1/checkout",status!~"5.."}[5m]))
        / sum(rate(http_requests_total{route="/v1/checkout"}[5m])))) > 14.4 * 0.001
    and
    (1 - (sum(rate(http_requests_total{route="/v1/checkout",status!~"5.."}[1h]))
        / sum(rate(http_requests_total{route="/v1/checkout"}[1h])))) > 14.4 * 0.001
  for: 2m
  labels: { severity: page }
  annotations:
    summary: "Checkout đang đốt error budget nhanh gấp 14x"
    runbook_url: "https://github.com/<you>/threadline/blob/main/docs/runbooks/checkout-errors.md"

- alert: OversellDetected
  expr: increase(oversell_detected_total[5m]) > 0
  labels: { severity: page }          # 🚨 nghiêm trọng nhất — vi phạm invariant lõi

- alert: KafkaConsumerLagHigh
  expr: kafka_consumergroup_lag > 10000
  for: 5m

- alert: OutboxStalled
  expr: outbox_unpublished_age_seconds > 60
  for: 2m

- alert: SagaStuck
  expr: increase(saga_stuck_total[10m]) > 0
```
> **Nguyên tắc alert:** mỗi alert `severity: page` phải (1) đại diện cho tác động lên user, (2) có runbook, (3) hành động được ngay lúc 3h sáng. Alert không thoả 3 điều này → hạ xuống ticket. Nói câu này = tư duy SRE thật.

### 13.7. Runbooks (`docs/runbooks/`)
Mỗi runbook theo template: **Triệu chứng → Tác động → Chẩn đoán (query cụ thể) → Giảm thiểu → Khắc phục gốc rễ → Escalation**.
- `high-kafka-lag.md`
- `oversell-detected.md`
- `saga-stuck.md`
- `db-connection-exhausted.md`
- `aurora-failover.md`
- `payment-provider-outage.md`

---

## 14. Documentation

### 14.1. README.md — cấu trúc chuẩn portfolio
```markdown
# Threadline — Distributed Commerce Backend

[badges: CI | coverage | Go Report | license | image size]

> Nền tảng backend e-commerce fashion multi-tenant, event-driven trên Kubernetes.
> Xử lý 500 orders/s với 0 oversell trong flash sale.

## 🎬 Demo
[GIF 30s: checkout → trace trong Grafana → dashboard cập nhật realtime]

## 🏆 Điểm nổi bật (Highlights)
- **0 oversell** với 10.000 request đồng thời tranh 100 sản phẩm — [xem load test report](docs/performance/flashsale-report.md)
- **Saga orchestration** với compensation đầy đủ, resume được sau khi service restart
- **Transactional Outbox + Debezium CDC** → không mất event, effectively-once
- **Trace liên tục xuyên Kafka** — từ HTTP request đến consumer cuối cùng
- **p99 checkout 180ms** ở 500 RPS ([k6 report](docs/performance/))
- 12 ADR ghi lại mọi trade-off kiến trúc

## 🚀 Quick Start (3 phút)
    git clone ... && cd threadline
    make dev          # docker compose up toàn bộ hệ thống
    make seed         # 100k products, 50 warehouses
    make demo         # chạy kịch bản checkout end-to-end
    open http://localhost:3000    # Grafana (admin/admin)

## 🏗 Kiến trúc
[C4 diagram] + link tới docs/adr/

## 📊 Kết quả benchmark
| Endpoint | RPS | p50 | p99 | Error |
|---|---|---|---|---|
| GET /v1/products | 2,000 | 12ms | 74ms | 0% |
| POST /v1/checkout | 500 | 89ms | 180ms | 0.02% |
| Flash sale (10k VU) | — | — | 412ms | 0 oversell ✅ |

## 🧪 Testing / 🔐 Security / 📈 Observability / ☁️ Deployment
...

## 🤔 Nếu làm lại, tôi sẽ...
[phần này gây ấn tượng cực mạnh — thể hiện sự trưởng thành kỹ thuật]
```

### 14.2. ADR — 12 quyết định cần ghi lại
Template (MADR): `Context → Decision Drivers → Options Considered → Decision → Consequences (tốt & xấu) → Status`

| # | Tiêu đề |
|---|---|
| ADR-001 | Microservices thay vì Modular Monolith — và vì sao chỉ 6 service |
| ADR-002 | Chọn Go làm ngôn ngữ chính |
| ADR-003 | Database per service, cấm cross-service JOIN |
| ADR-004 | Saga Orchestration thay vì Choreography |
| ADR-005 | Chọn Kafka thay vì SQS/SNS |
| ADR-006 | Transactional Outbox + Debezium CDC thay vì dual write |
| ADR-007 | Chiến lược chống oversell: atomic SQL UPDATE + Redis sharded counter cho hot SKU |
| ADR-008 | Cursor pagination thay vì offset |
| ADR-009 | Async checkout (202 Accepted) thay vì sync |
| ADR-010 | gRPC cho giao tiếp nội bộ, REST cho public API |
| ADR-011 | CQRS với OpenSearch cho catalog read model |
| ADR-012 | Multi-tenancy: shared database + RLS thay vì database-per-tenant |

> ADR là **thứ khiến hiring manager tin bạn ở level senior**. Code ai cũng viết được; ghi lại *vì sao* và *đã từ chối phương án nào* mới là dấu hiệu của người thiết kế hệ thống.

### 14.3. API Documentation
- **OpenAPI 3.1** viết tay (contract-first), publish qua Redoc/Scalar tại `/docs`
- **AsyncAPI 2.6** cho Kafka topic — mô tả event schema, key, partitioning, ordering guarantee. *Rất ít portfolio có AsyncAPI.*
- **Postman/Bruno collection** trong repo, có sẵn environment
- Mỗi endpoint: mô tả, ví dụ request/response, tất cả error code, rate limit, scope cần có

### 14.4. Sequence Diagrams cần có (Mermaid trong markdown)
1. Checkout happy path (đã có ở mục 4.5)
2. Checkout — payment failed → compensation
3. Checkout — hết hàng
4. Refresh token rotation + reuse detection
5. Outbox → CDC → Kafka → consumer (kèm propagate trace context)
6. Return & Refund flow
7. Hot SKU reservation qua Redis + reconciliation
8. Zero-downtime reindex OpenSearch (alias swap)

### 14.5. Các tài liệu khác
| File | Nội dung |
|---|---|
| `docs/DEPLOYMENT.md` | Từ zero → chạy trên EKS: prerequisites, terraform apply, ArgoCD bootstrap, DNS/cert, smoke test, rollback |
| `docs/DEVELOPMENT.md` | Setup local, coding convention, cách thêm 1 endpoint, cách thêm migration |
| `docs/ARCHITECTURE.md` | C4 Level 1–3, bounded context map, data flow |
| `docs/security/THREAT_MODEL.md` | STRIDE cho luồng checkout & payment |
| `docs/performance/` | Báo cáo k6, tuning query trước/sau, flame graph |
| `docs/runbooks/` | Sổ tay on-call |
| `CONTRIBUTING.md` | PR template, commit convention (Conventional Commits) |
| `CHANGELOG.md` | Sinh tự động từ commit |

---

## 15. Lộ trình thực thi (12 tuần, ~15h/tuần)

| Tuần | Mục tiêu | Deliverable "chứng minh được" |
|---|---|---|
| 1 | Nền móng | Monorepo, Makefile, docker-compose, CI skeleton, ADR-001/002, C4 diagram |
| 2 | Identity | Register/login, JWT RS256, refresh rotation + reuse detection, RBAC, unit test |
| 3 | Catalog | Product/variant/category, i18n, OpenAPI, cursor pagination, filter/sort |
| 4–5 | **Inventory** ⭐ | Reserve/release/commit, atomic UPDATE, ledger, TTL job, **test 500 goroutine không oversell** |
| 6 | Order + Saga ⭐ | State machine, saga orchestrator, compensation, idempotency, gRPC tới inventory |
| 7 | Payment | Mock PSP (Hexagonal 2 adapter), authorize/capture/refund, webhook idempotent, DLQ |
| 8 | Outbox + Kafka | Outbox table, polling relay, Debezium, consumer idempotent, AsyncAPI |
| 9 | Fulfillment + Returns | Allocation, shipment lifecycle, RMA, refund saga, restock |
| 10 | Search + CQRS | Kafka → OpenSearch, facet, alias swap reindex, availability-aware |
| 11 | **Observability + Performance** ⭐ | OTel end-to-end, 7 dashboard, SLO/alert, k6 đủ 6 kịch bản, **báo cáo benchmark** |
| 12 | Cloud + Docs | Terraform, Helm, ArgoCD, canary, 12 ADR, README hoàn chỉnh, video demo 3 phút |

**Nếu chỉ có 6 tuần** — cắt theo thứ tự này (giữ lại phần in đậm):
✅ Giữ: **Identity (tối giản), Catalog (CRUD), Inventory, Order+Saga, Payment (mock), Outbox+Kafka, Observability, docker-compose, CI, README+ADR**
❌ Cắt: Fulfillment/Returns, Search/CQRS, Terraform/EKS (thay bằng k3d + `terraform plan`), polyglot service, promotion engine

---

## 16. Cách "bán" project này khi phỏng vấn

### 16.1. Elevator pitch 45 giây
> *"Threadline là backend e-commerce fashion multi-tenant tôi xây để giải quyết bài toán tôi thấy khó nhất trong ngành: đảm bảo không oversell khi flash sale, trong khi hệ thống là phân tán. Tôi dùng 6 microservice trên Kubernetes, Saga orchestration cho luồng đặt hàng với compensation đầy đủ, và Transactional Outbox với Debezium CDC để không mất event. Điểm tôi tự hào nhất là bộ test chứng minh 10.000 request đồng thời tranh 100 sản phẩm thì có đúng 100 đơn thành công, và tôi có k6 report cùng Grafana dashboard để chứng minh, không chỉ nói suông."*

### 16.2. Câu hỏi họ sẽ hỏi → bạn đã có sẵn câu trả lời

| Câu hỏi | Bạn trả lời từ phần nào |
|---|---|
| "Làm sao chống oversell?" | 5.5 — atomic UPDATE + guard trong WHERE, giải thích EvalPlanQual |
| "Nếu payment thành công nhưng update DB fail?" | 4.5 — pivot transaction, retry-forward-only, DLQ + can thiệp thủ công |
| "Vì sao không dùng 2PC?" | Coupling, blocking, không scale; Saga + eventual consistency là đánh đổi đúng |
| "Consumer nhận trùng message thì sao?" | 4.6 — bảng `processed_events` + at-least-once = effectively-once |
| "Deadlock xử lý thế nào?" | 5.5 — sort thứ tự khoá + `lock_timeout` + retry với backoff |
| "Migration mà không downtime?" | 5.8 — expand/contract 6 bước |
| "Vì sao 6 service, không phải 12?" | 4.1 — tách theo ranh giới thay đổi & scale, không theo bảng |
| "Cache stale thì sao?" | 8.2 — event-driven invalidation + TTL ngắn cho dữ liệu biến động |
| "Latency tăng đột ngột, bạn debug thế nào?" | 13 — dashboard RED → tìm trace chậm → span breakdown → pprof |
| "Test coverage bao nhiêu?" | 10.7 — nói về mutation score thay vì chỉ line coverage |
| "Nếu làm lại bạn sẽ đổi gì?" | Có sẵn trong README — chuẩn bị 3 điều cụ thể |

### 16.3. Cách viết vào CV (dùng công thức: hành động + kỹ thuật + **số liệu**)
```
Threadline — Distributed Commerce Platform (Personal Project) | github.com/<you>/threadline
• Thiết kế và xây dựng nền tảng e-commerce multi-tenant với 6 microservice (Go, PostgreSQL,
  Kafka, Redis, OpenSearch) trên Kubernetes/AWS, phục vụ 500 orders/s ở p99 180ms.
• Triển khai Saga orchestration + Transactional Outbox (Debezium CDC) đảm bảo tính nhất quán
  phân tán; xác minh 0 mất mát event qua chaos test dừng Kafka 60 giây.
• Giải quyết bài toán oversell bằng atomic conditional UPDATE + Redis sharded counter cho hot
  SKU; chứng minh 0 oversell với 10.000 request đồng thời (k6).
• Xây dựng observability đầy đủ với OpenTelemetry (trace liên tục xuyên Kafka), Prometheus,
  Grafana, Loki, Tempo; định nghĩa SLO 99.9% và alert theo error-budget burn rate.
• CI/CD với GitHub Actions (< 8 phút), Trivy/govulncheck scan, canary deploy bằng Argo Rollouts;
  test coverage 78% với testcontainers và contract testing (Pact).
```

### 16.4. 5 điều tuyệt đối đừng bỏ qua
1. **README phải có số liệu thật.** Không có benchmark = project chỉ là "CRUD có nhiều folder".
2. **Commit history phải sạch.** Conventional Commits, PR nhỏ, có mô tả. Hiring manager *sẽ* xem.
3. **Mỗi service có README riêng.** Người đọc không cần hiểu cả hệ thống mới đọc được 1 service.
4. **Quay video demo 3 phút.** Đưa link lên đầu README + vào CV. Tỉ lệ được xem cao hơn code rất nhiều.
5. **Viết 1 bài blog** về bài toán oversell hoặc saga. Đăng LinkedIn/Dev.to, tag GFG. Đây là cách "chen ngang" hàng đợi tuyển dụng hiệu quả nhất.

---

## 17. Kiểm tra cuối: Project có thực sự vượt JD không?

| Yêu cầu JD | Threadline đáp ứng | Mức độ |
|---|---|---|
| 4+ năm high-traffic distributed systems | Saga, outbox, CDC, idempotency, chaos test, load test có số liệu | ✅ Vượt |
| Data structures & algorithms | Sharded counter, keyset pagination, allocation optimization, closure table | ✅ Đạt+ |
| Java/Python/Golang/PHP | Go (chính) + 1 service ngôn ngữ khác | ✅ Vượt |
| Testing framework | Unit + integration + contract + E2E + load + chaos + mutation | ✅ Vượt xa |
| Designing & building APIs | OpenAPI 3.1 + AsyncAPI + versioning + breaking-change CI gate | ✅ Vượt xa |
| Microservices + Kubernetes | 6 service, Helm, ArgoCD, HPA/KEDA, canary, NetworkPolicy | ✅ Vượt |
| Clean, well-tested code | Hexagonal + arch-lint enforce trong CI, coverage 78% | ✅ Vượt |
| Architectural vision & strategy | 12 ADR, trade-off analysis, C4, threat model | ✅ Vượt xa |
| Site reliability & performance | SLO, error budget, runbook, observability 3 tín hiệu, benchmark | ✅ Vượt xa |
| Developer experience | `make dev` < 3 phút, CI < 8 phút, codegen, seed data | ✅ Vượt |

**Kết luận:** Nếu bạn hoàn thành Threadline ở mức chất lượng mô tả trong tài liệu này, vị trí Software Engineer II tại GFG sẽ không còn là mục tiêu — nó sẽ là mức sàn. Hồ sơ của bạn sẽ cạnh tranh được ở tầm **Senior Engineer**.
