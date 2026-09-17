# add-per-channel-recharge-bonus

为余额充值引入**按支付渠道实例独立配置的赠送倍率**，替代当前唯一且全局的 `BALANCE_RECHARGE_MULTIPLIER`，使不同渠道（如支付宝直连、微信直连、易支付、Epusdt）可以设置不同的到账赠送比例。

阅读顺序：`proposal.md` → `design.md` → `specs/*/spec.md` → `tasks.md` → `verification.md`。

## 范围边界

- 只作用于**余额充值**（`order_type = balance`）。订阅订单（`order_type = subscription`）的赠送语义不在本变更内。
- 渠道配置与全局配置是**覆盖关系**（渠道优先），不是相乘关系。
- 不新增数据库表或列，只扩展 `payment_provider_instances.limits` 的 JSON 结构。
