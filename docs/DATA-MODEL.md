# Order API — Data Model

## Table

**Name:** `komodo-orders`  
**Key schema:** `PK (S)` only — no sort key  
**Entity types share one table:** `ORDER`, `RETURN`, `EXCHANGE`, `COUNTER`

### GSIs

| Index | GSI PK | GSI SK | Access pattern |
|-------|--------|--------|----------------|
| `user-index` | `userId` | `createdAt` | All orders/returns for a user, newest first |
| `email-index` | `email` | `createdAt` | Guest order lookup by email |
| `type-status-index` | `type` | `status` | Ops queries — all processing orders, all pending returns, etc. |
| `order-index` | `orderId` | `createdAt` | All returns/exchanges for a given order |

---

## Item Types

### ORDER

| Attribute | Type | Notes |
|-----------|------|-------|
| `PK` | String | `displayId` — e.g. `KFS-2504-7XK3M9` |
| `type` | String | `"ORDER"` — GSI PK for `type-status-index` |
| `seq` | Number | Global sequential counter — for sort/ops only, never shown as an ID |
| `status` | String | `pending\|confirmed\|processing\|shipped\|delivered\|cancelled\|refunded\|returned\|exchanged` |
| `userId` | String | GSI key — registered user ID |
| `email` | String | GSI key — guest or registered |
| `returnCount` | Number | Incremented atomically when a return is created; drives `-R{n}` suffix |
| `exchangeCount` | Number | Incremented atomically when an exchange is created; drives `-X{n}` suffix |
| `address` | Map | Shipping address snapshot at time of purchase |
| `payment` | Map | `method`, `transactionId`, `amount`, `currency` |
| `totals` | Map | `subtotal`, `tax`, `shipping`, `discount`, `total` |
| `promotions` | List | Applied promotions — see shape below |
| `lineItems` | List | Embedded order items — see shape below |
| `isReturnable` | Boolean | Whether the order is eligible for a return |
| `isExchangeable` | Boolean | Whether the order is eligible for an exchange |
| `returnExchangeWindowDays` | Number | Days from delivery the customer has to initiate — `-1` for unlimited |
| `notes` | String | Optional — internal or customer-provided notes |
| `createdAt` | String | ISO 8601 |
| `updatedAt` | String | ISO 8601 |

**`lineItems[]` shape**

| Attribute | Required | Notes |
|-----------|----------|-------|
| `productId` | Yes | |
| `sku` | Yes | |
| `name` | Yes | Snapshot — not a live reference |
| `quantity` | Yes | |
| `unitPrice` | Yes | Price at time of purchase |
| `total` | Yes | `unitPrice × quantity` |
| `variantId` | No | |

**`promotions[]` shape**

| Attribute | Required | Notes |
|-----------|----------|-------|
| `promotionId` | Yes | |
| `code` | No | Customer-entered code, if applicable |
| `type` | Yes | `percentage\|fixed\|free_shipping` |
| `discount` | Yes | Amount discounted by this promotion |

---

### RETURN

| Attribute | Type | Notes |
|-----------|------|-------|
| `PK` | String | `{orderId}-R{n}` — e.g. `KFS-2504-7XK3M9-R1` |
| `type` | String | `"RETURN"` |
| `status` | String | `requested\|approved\|received\|processed\|rejected\|cancelled` |
| `orderId` | String | FK → parent order PK; GSI key for `order-index` |
| `userId` | String | GSI key |
| `email` | String | GSI key |
| `items` | List | Subset of parent order line items being returned |
| `notes` | String | Optional — customer-provided |
| `createdAt` | String | ISO 8601 |
| `updatedAt` | String | ISO 8601 |

**`items[]` shape**

| Attribute | Required | Notes |
|-----------|----------|-------|
| `sku` | Yes | |
| `quantity` | Yes | |
| `reason` | Yes | `defective\|wrong_item\|not_as_described\|changed_mind\|other` |
| `notes` | No | |

---

### EXCHANGE

| Attribute | Type | Notes |
|-----------|------|-------|
| `PK` | String | `{orderId}-X{n}` — e.g. `KFS-2504-7XK3M9-X1` |
| `type` | String | `"EXCHANGE"` |
| `status` | String | `pending\|approved\|processing\|shipped\|completed\|cancelled` |
| `orderId` | String | FK → parent order PK; GSI key for `order-index` |
| `userId` | String | GSI key |
| `email` | String | GSI key |
| `returnItems` | List | Items being returned (same shape as RETURN `items[]`) |
| `exchangeItems` | List | Replacement items — see shape below |
| `priceDelta` | Number | Positive = customer owes, negative = customer credited |
| `createdAt` | String | ISO 8601 |
| `updatedAt` | String | ISO 8601 |

**`exchangeItems[]` shape**

| Attribute | Required | Notes |
|-----------|----------|-------|
| `productId` | Yes | |
| `sku` | Yes | |
| `name` | Yes | |
| `quantity` | Yes | |
| `unitPrice` | Yes | Price at time of exchange |

---

### COUNTER

| Attribute | Type | Notes |
|-----------|------|-------|
| `PK` | String | `COUNTER#ORDER`, `COUNTER#RETURN`, or `COUNTER#EXCHANGE` |
| `seq` | Number | Current sequence value — incremented via `UpdateItem ADD seq 1` |

Increment pattern: call `UpdateItem ADD seq 1` with `ReturnValues: UPDATED_NEW` before writing the entity. The returned value is the entity's `seq`. For return/exchange suffix counts, increment `returnCount` / `exchangeCount` on the parent order item instead.

---

## displayId Convention

| Entity | Format | Example |
|--------|--------|---------|
| Order | `KFS-{YYMM}-{6-char random}` | `KFS-2504-7XK3M9` |
| Return | `{orderId}-R{n}` | `KFS-2504-7XK3M9-R1` |
| Exchange | `{orderId}-X{n}` | `KFS-2504-7XK3M9-X1` |

- `KFS` — brand prefix
- `YYMM` — year + month of order placement (UTC)
- 6-char random — alphanumeric, excluding confusable chars (`0`, `O`, `1`, `I`, `L`)
- `seq` is stored on the item for chronological sorting but is never surfaced as part of the customer-facing ID

---

## Order Status Lifecycle

```
pending → confirmed → processing → shipped → delivered
                                           ↘ cancelled
                              ← refunded ←
                              ← returned ←
                              ← exchanged ←
```

`returned` and `exchanged` are terminal states set on the order when a return or exchange is fully processed. Partial returns/exchanges do not change the order status — only a full resolution does.

Returns and exchanges have their own `status` fields with context-appropriate transitions (see RETURN and EXCHANGE item types above).
