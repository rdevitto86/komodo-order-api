# TODO

> **Current Version:** V1

## V1 (Current)

> Status: DynamoDB wired. `CreateOrder`, `GetOrder`, `ListOrders`, `UpdateOrderStatus` implemented. Schema aligned to data-model.md (hash-only PK, named GSIs, COUNTER#ORDER sequence). Route prefix fixed to `/v1/`. Returns flow stubbed. Private state-transition routes registered but not yet implemented.
>
> **Identity model:** email is the universal key for both guests and registered users. Every order carries an email. At placement, if the email matches a registered account the order is automatically linked to that `USER#<id>` — no separate guest/user routes needed, and guest conversion is zero-work. Order submission and lookup are unified and handle both identity types.

### OpenAPI

- **[M]** Complete `openapi.yaml` — required by downstream consumer codegen (cart-api, payments-api, shipping-api cross-reference)

### Open Items

- **[H]** Implement `POST /v1/me/orders/{orderId}/cancel` — release stock holds via shop-inventory-api, trigger refund via payments-api; enforce cancellable state check (`pending` or `confirmed` only)
- **[H]** Wire return request persistence — implement `CreateReturn`, `GetReturn`, `ListReturns`, `UpdateReturnStatus` in DynamoDB; replace stub responses on `GET/POST /v1/orders/returns` and `GET/DELETE /v1/orders/returns/{returnId}`
- **[H]** Implement private return state transitions — wire `PUT /v1/returns/{returnId}/approve` (trigger refund via payments-api), `PUT /v1/returns/{returnId}/reject`, and `PUT /v1/returns/{returnId}/receive` (trigger restock via shop-inventory-api and loyalty reversal)
- **[M]** Implement order status state machine: `pending → confirmed → shipped → delivered → cancelled` — enforce transition rules in service layer; reject invalid transitions with 409
- **[M]** Publish `order.placed`, `order.cancelled`, and `order.fulfilled` events to events-api
- **[M]** Consult banned-customer registry on `CreateOrder` — check `komodo-banned-customers` table via forge SDK `bannedcustomers.IsBanned(ctx, email)` before persisting any order; return 403 on match
- **[M]** Wire inbound shipping to returns flow — call `shipping-api` `POST /v1/labels/inbound` on RMA approval; return label URL to customer; store `inbound_shipment_id` on the return record
- **[M]** Wire outbound shipping to order fulfillment — call `shipping-api` `POST /v1/labels/outbound` when order transitions to `shipped`; store tracking number and carrier on the order record
- **[L]** Add integration tests for order creation, guest lookup, account auto-link, cancellation flow, and return lifecycle
- **[M]** Wire `GET /health/ready` in `cmd/public/main.go` — checkers: `DynamoDBChecker("dynamodb", ddb, ordersTable)`, `RedisChecker("redis", cache)`, `HTTPChecker("cart-api", os.Getenv("CART_API_URL")+"/health")`, `HTTPChecker("inventory-api", os.Getenv("INVENTORY_API_URL")+"/health")`; blocked on forge SDK `api/handlers/health` release
- **[M]** Wire `GET /health/ready` in `cmd/private/main.go` — checkers: `DynamoDBChecker("dynamodb", ddb, ordersTable)` only; private entrypoint has no downstream HTTP clients; blocked on forge SDK `api/handlers/health` release
- **[M]** Scope `order.delayed` event — emit via `EventBusAdapter` when an order is flagged delayed via a new private endpoint (e.g. `PUT /v1/orders/{orderId}/delay`, body `{reason, causeRef?}`); does not change `OrderStatus` — an order can be `processing` and delayed simultaneously — so store `delayed bool` + `delayReason` on the order record for UI display, alongside the event for analytics/Athena correlation (events-api TODO). V2: auto-trigger from `inventory.out_of_stock` events once that pipeline lands.
- **[L]** Evaluate "order fulfillment in progress" before adding a new event type — likely redundant with the existing `processing` status (currently omitted from the state-machine item above); if `order.status_updated{status: "processing"}` already fires when fulfillment begins, no new event is needed. Only add a dedicated `order.fulfillment_started` if "fulfillment" must be distinguished from "payment processing" as separate sub-stages.

## Testing

- **[M]** **Implement CI test stack** — add `github.com/stretchr/testify` and `go.uber.org/mock` to `go.mod`; generate mocks from the repo, cart, and inventory adapter interfaces via `mockgen -source`; convert stub `*_test.go` files to real unit tests (table-driven, `t.Run` subtests) with `net/http/httptest` for handler layer; add `testutil.Component(t)` / `testutil.Integration(t)` tier decorators from the SDK (`github.com/rdevitto86/komodo-forge-sdk-go/testing/testutil`, `TEST_TIER`-gated; default tier is `unit`); add `testcontainers-go` for integration tests against DynamoDB (LocalStack); apply section banners. Security-critical paths (`PlaceOrderUnified`, ownership checks) require 100% coverage per standards. Reference auth-api as the canonical pattern once its retrofit is complete.

## Audit Findings — 2026-05-17

- **[H]** **`PlaceOrderUnified` silently degrades when adapters are nil.** `internal/service/orders.go:144-152, 187-191` skip cart-token validation and inventory hold confirmation when `s.cart` or `s.inventory` is nil. Today both adapters are always nil. The handler still calls `repo.CreateOrder` — meaning any caller can POST `/v1/orders` with an arbitrary `checkoutToken` + email and a fully-formed order is persisted with empty items, zero totals, no stock hold. Either fail-closed (refuse placement when required adapters are nil) or guard behind a single startup check that crashes the service if adapters aren't wired in non-dev environments. Tracked already as "wire cart-api / shop-inventory-api adapters" but the security framing is missing — promote.
- **[H]** **Guest order placement against another user's email.** `internal/service/orders.go:128-138` auto-links a guest order to `USER#<linkedID>` if the supplied email matches a registered account, with no email verification. Anyone who knows your email can place orders that appear in your `GET /me/orders` view once you log in. Email-as-universal-key is intentional, but the placement path needs one of: 1) require an OTP-bound short-lived token to assert email ownership when no JWT is present, 2) mark auto-linked guest orders as `pending_claim` until the owner authenticates and accepts them, 3) only allow guest→user auto-link when the email also matches the JWT subject on a separate verification call. Pick before broad UI integration.
- **[H]** **Email in `?email=` query string leaks to logs.** `GetOrderUnified` (`internal/handlers/orders.go:165`) accepts the guest's email as a query param. Query strings land in access logs, CDN logs, proxies, and browser history. Move to a request header (`X-Guest-Email`) or a one-time signed order-lookup token issued at placement.
- **[M]** **`repo.GetOrder` masks all errors as 404.** `internal/repo/orders.go:117` wraps every `GetItemAs` error as `models.ErrNotFound`. A DynamoDB throttle, IAM revocation, or network blip is reported to the caller as a missing order — kills retry signals and obscures incidents. Inspect the error: only map `ResourceNotFoundException` to `ErrNotFound`; surface infra errors distinctly.
- **[M]** **JWT-subject format inconsistency between `GetOrder` and `GetOrderUnified`.** `service.GetOrder:225` requires the order's `UserID` to equal `"USER#"+userID` exactly. `service.GetOrderUnified:245` accepts both raw uuid and `"USER#"+uuid`. The auth-api JWT subject contract isn't pinned: today OTP-verify resolves `creds.UserId` from user-api (already prefixed) and falls back to raw email on user-api error. Tighten the JWT subject contract in one place (auth-api always issues `"USER#<id>"`) and remove the dual-form acceptance, OR keep dual-form but apply it uniformly across both ownership checks.
- **[M]** **`COUNTER#ORDER` is a hot partition.** `repo.IncrementOrderSeq` is a global atomic increment on a single item — every order placement contends on the same DynamoDB partition. Fine up to ~100s of orders/sec, but the launch-day sale spike will hit it first. Move to a sharded counter (`COUNTER#ORDER#<shard0..N>`) or drop the sequence entirely and rely on `createdAt` + the random suffix in the display ID for ordering.
- **[L]** **`repo.contains` / `containsAt` reimplement `strings.Contains`.** `internal/repo/orders.go:250-261` rolls a hand-written substring matcher; replace with `strings.Contains`. (The fragile `ConditionalCheckFailedException` string match itself is already noted — tracked in the SDK typed-sentinel TODO.)
- **[L]** **`var EC` and `var DynDB` package-level globals.** Same antipattern as the other services; defer to the cross-SDK instance-based-design sweep.
- **[L]** **No release of inventory holds on cancellation.** `service.CancelOrder:303-304` has TODO comments for releasing holds via shop-inventory-api and refund via payments-api. Promoting visibility — cancelled orders today are stock-leaks until those adapters land.

## Audit findings — gaps from audit (follow-up)

> Note: the 2026-05-17 findings above reference `internal/service/orders.go` and `internal/handlers/orders.go`. Those packages were merged into `internal/api/` — the code moved, none of those items were fixed. Re-verified against `internal/api/orders.go` + `orders_handler.go`: all still valid. Path map: `service/orders.go`→`api/orders.go`, `handlers/orders.go`→`api/orders_handler.go`.

### Fix the package-init table-name capture (latent config bug)
**Problem:** `internal/repo/orders.go:21` does `var table = os.Getenv(config.DYNAMODB_ORDERS_TABLE)` at package-init time, which runs before `main.init()` fetches that key from Secrets Manager and `os.Setenv`s it (`cmd/public/main.go:43,73-75`). The SM-provided table name is therefore silently ignored — the service only works because the table name also happens to be set as a real container env var. If the two ever diverge, or the env var is dropped in favour of SM, every query targets the wrong/empty table.
**Action:** Resolve the table name at call time or inject it via a repo constructor wired in `main()` after bootstrap. (Same pattern exists in cart-api.)

### Convert colon-prefixed error strings to verb phrases
**Problem:** `internal/repo/orders.go` wraps errors with `FuncName:` prefixes throughout — `"repo.IncrementOrderSeq: %w"` (76), `"repo.CreateOrder: put: %w"` (104), `"repo.GetOrder: %w"` (118), `"repo.ListOrdersByUser: query: %w"` (165), `"repo.UpdateOrderStatus: update: %w"` (217), plus `"encodeCursor: ..."`/`"decodeCursor: ..."`. Hard-rule violation (principles §1). (13 occurrences.)
**Action:** Rewrite each as a verb phrase, e.g. `"failed to increment order sequence: %w"`, `"failed to write order: %w"`.

### Remove name-leading and multi-paragraph doc comments
**Problem:** Doc comments across `internal/api/orders.go`, `orders_handler.go`, and `internal/repo/orders.go` open with the identifier name (`// OrderService manages...`, `// PlaceOrderUnifiedHandler handles...`, `// IncrementOrderSeq atomically...`). Several are multi-paragraph blocks well past two sentences (`PlaceOrderUnified` orders.go:94-107, `buildDisplayID` 338-347, `orderRecord` repo/orders.go:30-38, `ListOrdersByUser` 127-132) — comments.md hard violations on both counts.
**Action:** Reword each to a single verb-leading sentence (a second only for a non-obvious contract); drop the multi-paragraph prose into `docs/` where it belongs.

### Replace hand-rolled contains with strings.Contains (already noted — promote)
**Problem:** `internal/repo/orders.go:250-261` `contains`/`containsAt` reimplement `strings.Contains` to match `ConditionalCheckFailedException`. Confirmed still present after the refactor.
**Action:** Replace with `strings.Contains`. (The fragile string-match itself stays until the SDK exposes a typed sentinel — tracked.)
