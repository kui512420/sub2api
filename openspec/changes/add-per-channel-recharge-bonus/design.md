## Context

余额充值的到账金额当前在下单流程**前半段**就已确定：

```go
// backend/internal/service/payment_order.go  CreateOrder
orderAmount := req.Amount
limitAmount := req.Amount
if plan != nil {
    orderAmount = plan.Price
    limitAmount = plan.Price
} else if req.OrderType == payment.OrderTypeBalance {
    orderAmount = calculateCreditedBalance(req.Amount, cfg.BalanceRechargeMultiplier) // ← 全局倍率
}
feeRate := cfg.RechargeFeeRate
...
sel, err := s.selectCreateOrderInstance(ctx, req, cfg, payAmount)      // ← 实例在这里才选定
```

而 `pay_amount` 由 `calculateCreateOrderPayAmountForOrderType(limitAmount, ...)` 基于用户实付金额算出，随后负载均衡用 `payAmount` 选实例并校验渠道限额。

也就是说：**金额计算在前，实例选定在后**。渠道倍率依赖选中实例，因此必须把到账金额的计算挪到实例选定之后。

## Goals / Non-Goals

**Goals**

- 管理员能为每个渠道实例、每个支付类型独立设置赠送倍率。
- 未配置渠道倍率的渠道，行为与改动前逐字节一致（全局倍率仍生效）。
- 到账金额以**最终选中实例**的倍率为准，包括同类型多渠道并存（直连 + 聚合）的场景。
- 不新增数据库表或列，不需要数据迁移。

**Non-Goals**

- 不做订阅订单赠送。
- 不做分时段/分用户等级的倍率（未来可在此模型上做调度层扩展）。
- 不做「固定金额赠送」（如「满 100 送 10」）。本次只做倍率。若后续需要，`ChannelLimits` 已是可扩展的 JSON 结构。
- 不做按渠道区分手续费率（`RechargeFeeRate` 仍是全局）。

## Decisions

### D1：存储放在 `limits` JSON 而非新增字段

`payment_provider_instances.limits` 已是 `text` 存 JSON，结构为 `payment.InstanceLimits = map[string]payment.ChannelLimits`，读取路径有两条且都已存在：

- `payment/load_balancer.go` 的 `getInstanceChannelLimits`（含 Stripe 用 `stripe` 键、`legacyVisibleMethodAlias` 别名回退）
- `service/payment_config_limits.go` 的 `pcInstanceTypeLimits`（用户端限额聚合）

把 `bonusMultiplier` 加进 `ChannelLimits` 后，两条路径**自动**获得该字段，无需新增查询。

**取舍**：语义上 `ChannelLimits` 叫「限额」，塞倍率进去名不副实。但换独立字段需要 ent schema 变更 + 迁移 + 仓储/投影/缓存快照多处同步，成本远高于收益。选择在结构体注释里显式说明其承载「渠道级参数」而非仅限额。

**替代方案（已否决）**：新增 `bonus_multiplier` 列。否决理由：迁移成本高，且 `limits` 与 `bonusMultiplier` 的生命周期完全一致（都是渠道实例级配置，都被同一个对话框编辑）。

### D2：`InstanceSelection` 必须携带倍率

`selectCreateOrderInstance` 返回 `*payment.InstanceSelection`，其结构为 `{InstanceID, ProviderKey, Config, SupportedTypes, PaymentMode}`。业务层在选定之后**不再持有实例对象**，无法重新读取 `limits`。

因此需要让选择结果携带倍率。两种做法：

- **(A) 在 `InstanceSelection` 增加字段** `BonusMultiplier *float64`，由负载均衡在构造候选时从 `getInstanceChannelLimits` 一并取出。指针类型用于区分「未配置」与「配置为 1.0」。
- **(B) 业务层用 `sel.InstanceID` 回查数据库**。否决：多一次查询，且 `InstanceID` 是字符串，回查需要类型转换与错误处理，徒增失败路径。

采用 (A)。

### D3：选定顺序调整

```go
// 调整后
orderAmount := req.Amount          // 平衡：先用实付占位
limitAmount := req.Amount
if plan != nil {
    orderAmount = plan.Price
    limitAmount = plan.Price
}
// 余额充值不再在此处乘倍率
feeRate := cfg.RechargeFeeRate
...  // 币种解析
payAmountStr, payAmount, err := calculateCreateOrderPayAmountForOrderType(limitAmount, ...)
sel, err := s.selectCreateOrderInstance(ctx, req, cfg, payAmount)   // 基于实付金额选择（不变）
if err := s.validateSelectedCreateOrderInstance(ctx, req, sel); err != nil { ... }
// ↓ 新增：实例确定后计算到账金额
if req.OrderType == payment.OrderTypeBalance && plan == nil {
    orderAmount = calculateCreditedBalance(req.Amount, resolveChannelBonusMultiplier(sel, req.PaymentType, cfg))
}
```

`sel` 可能在 `selectCreateOrderInstance` 内部被替换为「OAuth 预检」等分支的临时选择，因此计算必须放在 `validateSelectedCreateOrderInstance` 之后、`createOrderInTx` 之前，确保用的是最终 `sel`。

### D4：倍率解析是纯函数，便于单测

```go
// payment_amounts.go
func resolveChannelBonusMultiplier(cl *payment.ChannelLimits, global float64) float64 {
    if cl.BonusMultiplier != nil && isValidBonusMultiplier(*cl.BonusMultiplier) {
        return *cl.BonusMultiplier
    }
    return normalizeBalanceRechargeMultiplier(global)
}
```

`isValidBonusMultiplier` 复用现有 `normalizeBalanceRechargeMultiplier` 的判定口径（排除 `NaN`/`Inf`/`<= 0`），保持全局与渠道两条路径的非法值处理一致。

### D5：Stripe 与别名渠道的键解析复用现有逻辑

`bonusMultiplier` 的读取键必须与限额一致，否则会出现「限额按 `stripe` 键、倍率按 `card` 键」的错位。做法：**不新增键解析逻辑**，直接在 `getInstanceChannelLimits` 返回的 `ChannelLimits` 上加字段，复用其既有的 `stripe` 特判与 `legacyVisibleMethodAlias` 回退。

### D6：订单金额计算只对余额充值生效

`plan == nil && req.OrderType == payment.OrderTypeBalance` 是唯一应用点。订阅路径的 `orderAmount = plan.Price` 保持不变。

## Risks / Trade-offs

- **R1｜业务层拿不到实例的 `ChannelLimits`**：缓解手段见 D2，`InstanceSelection` 增字段。需要同步检查所有构造 `InstanceSelection` 的位置，避免漏填导致倍率静默失效。
- **R2｜同类型多渠道并存仍是「先选后算」**：用户端展示的预计到账金额只能是「按请求类型的聚合展示」，可能与最终选中的实例不完全一致（例如 `alipay` 下同时存在 1.1 倍和 1.0 倍的渠道，用户看到的是其中之一的估算）。这与现有 `pcAggregateMethodLimits` 的 UNION 聚合语义一致，属于既有设计代价，不在本变更内消除。**下单成功后以后端订单 `amount` 为准**。
- **R3｜测试断言冲突**：现有 `payment_amounts_test.go`、`PaymentView.spec.ts` 断言到账金额 = 输入 × 全局倍率。改动后这些断言在「渠道无配置」时仍成立，但需要新增渠道配置用例，并检查是否有测试隐含假设「金额在选实例前就已确定」。
- **R4｜`amount` 语义被误用**：`validatePaymentRedeemCode` 校验发放码面额等于 `o.Amount`，`doBalance` 用 `o.Amount` 建码。改动只影响 `amount` 的**生成**，不改变这两处读取，语义保持一致。
- **R5｜退款比例**：`calculateGatewayRefundAmount(orderAmount, payAmount, refundAmount, currency)` 按 `refundAmount/orderAmount × payAmount` 折算。赠送后 `amount ≠ payAmount` 的比例更大，但公式本身与现有全局倍率场景完全同构，无需改动。

## Migration Plan

无数据库迁移。发布顺序无要求（新旧前端/后端组合下，未识别 `bonusMultiplier` 的一侧行为等同「未配置」）。回滚只需回退二进制与前端资源，已落库的 `bonusMultiplier` 键会被旧版本忽略。

## Open Questions

- 是否需要「倍率上限」保护（例如拒绝 `> 10`）以防止误配置造成赠送失控？暂定不设硬上限，仅要求 `> 0` 且有限。若要设，建议在管理端表单加 `max` 提示而非后端强拦。
