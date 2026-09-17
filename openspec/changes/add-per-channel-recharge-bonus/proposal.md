## Why

当前余额充值的赠送能力只有一个全局开关 `BALANCE_RECHARGE_MULTIPLIER`（存于 `settings` 表，键名 `BALANCE_RECHARGE_MULTIPLIER`，由 `payment_config_service.go` 读取），对**所有支付渠道**生效同一个倍率。运营上这不够用：

- 不同渠道的通道成本不同。支付宝/微信直连费率低，Epusdt（USDT-TRC20）到账快但需要额外补贴，易支付类聚合通道费率更高。运营希望「费率高的渠道多送一点」或「做活动的渠道单独加送」，而不是全站一起调。
- 全局倍率一旦为了某个渠道活动调高，其余渠道会连带赠送，成本不可控。
- 现有实现里渠道级配置（`payment_provider_instances.limits`，JSON）已经承载了渠道级的单笔上下限与日限额，赠送倍率属于同一层级的渠道级参数，缺少它导致渠道配置能力不完整。

## What Changes

- 在支付渠道实例的 `limits` JSON 中，为每个渠道类型新增可选字段 `bonusMultiplier`（赠送倍率，`> 0` 的浮点数）。该字段与该渠道已有的 `singleMin` / `singleMax` / `dailyLimit` 并列，**不新增数据库表或列**。
- 余额充值下单时，实际到账金额的计算倍率按以下优先级决定：**选中渠道实例的 `bonusMultiplier`（若已配置且合法）→ 全局 `BALANCE_RECHARGE_MULTIPLIER` → 默认 `1.0`**。渠道配置与全局配置是**覆盖**关系，不相乘。
- 倍率的生效位置从「下单前按用户请求的 `PaymentType` 用全局倍率算」改为「**在负载均衡选定渠道实例之后**，用该实例的倍率算」。原因是同一个 `PaymentType`（如 `alipay`）可能对应多个渠道实例（支付宝直连 + 易支付聚合），下单前无法确定最终走哪个实例的费率与赠送。因此需要把到账金额的最终确定推迟到实例选定之后。
- 订单创建路径的顺序调整为：先按「用户实付金额」完成实例选择与金额/币种校验，再用**选中实例**的倍率计算到账金额并写入订单。
- 管理端渠道实例配置对话框（`PaymentProviderDialog.vue` 的「单笔限额」折叠区）新增赠送倍率输入框，与三个限额字段同框展示；占位提示说明留空即使用全局倍率。
- 用户端支付页（`PaymentView.vue`）对**选定渠道后**的到账金额展示使用该渠道的倍率。
- `GET /admin/payment/providers` 与 `GET /api/payment/checkout` 的响应新增/透传渠道级倍率信息，供前端展示。
- 不改变任何现有字段语义以外的东西：`payment_orders.amount` 继续表示**到账金额（含赠送）**，`pay_amount` 继续表示网关实收金额；不改变回调金额校验、不改变退款比例计算逻辑。
- 订阅订单、注册优惠码赠送（`promo_code.bonus_amount`）、邀请返佣（`applyAffiliateRebateForOrder`）均不受本变更影响。

## Capabilities

### New Capabilities

- `per-channel-recharge-bonus`：定义渠道级赠送倍率的配置模型与校验规则、到账金额的计算与优先级、倍率在「先选实例后算金额」流程中的位置，以及跨入口（余额充值下单、用户端 checkout、管理端渠道配置）的一致性要求。

### Modified Capabilities

无。本仓库 `openspec/specs/` 为空，尚无既有已发布能力需要修改；现有全局 `BALANCE_RECHARGE_MULTIPLIER` 行为在本变更中作为兼容基线保留（未配置渠道倍率时行为与改动前完全一致）。

## Impact

- **数据库**：无迁移。`payment_provider_instances.limits` 为 `text`（JSON）字段，仅扩展其 JSON 键集合。旧数据（不含 `bonusMultiplier`）读取时自然回落全局倍率。
- **后端**：
  - `backend/internal/payment/load_balancer.go`：`ChannelLimits` 结构体新增字段；`InstanceSelection` 需要携带选中实例的 `ChannelLimits`（或 `bonusMultiplier`），因为选定之后业务层不再持有实例对象。
  - `backend/internal/service/payment_amounts.go`：赠送倍率的归一化与到账金额计算，新增「按实例倍率解析」的入口。
  - `backend/internal/service/payment_order.go`：`CreateOrder` 调整顺序；`calculateCreditedBalance` 的入参由全局倍率改为「实例倍率 → 全局倍率」的解析结果。
  - `backend/internal/service/payment_config_providers.go`：渠道实例创建/更新时校验 `bonusMultiplier`（`> 0`、有限值），拒绝非法 JSON。
  - `backend/internal/handler/payment_handler.go`：checkout 响应携带渠道级倍率。
- **管理端 API**：`POST/PUT /admin/payment/providers` 的 `limits` JSON 语义扩展；不做字段级 schema 变更（仍是字符串）。
- **前端**：`PaymentProviderDialog.vue`（新增输入与序列化/反序列化、i18n）、`PaymentView.vue`（到账金额按所选渠道计算）、`types/payment.ts`。
- **风险点**：现有单元测试与前端测试断言「到账金额 = 输入金额 × 全局倍率」，且 `PaymentView.spec.ts` 已有基于全局倍率的用例，改动后需要同步调整并新增渠道覆盖用例。
