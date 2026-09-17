# 验证记录（add-per-channel-recharge-bonus）

状态：**已实现**，自动验证通过；手动联调（7.7）未执行，需运行环境。

## 已实现

渠道级赠送倍率落在 `payment_provider_instances.limits` 的 `bonusMultiplier` 字段（无数据库迁移）。余额充值下单时，到账倍率按「选中渠道实例 → 全局 → 1.0」解析，渠道覆盖全局且不相乘。

关键点：到账金额的计算从「下单前按用户请求的 `PaymentType` 用全局倍率算」改为「**负载均衡选定实例之后**用该实例的倍率算」。同一 `PaymentType` 下可存在多个实例（如支付宝直连 + 易支付聚合），只有选定后才可能知道该走哪个渠道的倍率。

## 自动验证

### 后端

- `go build ./...`：通过。
- `go test -tags=unit ./internal/payment/ ./internal/handler/`：全部通过。
- `go test -tags=unit ./internal/service/`：通过，除 `TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort`（已在 `git stash` 后的干净 main 上复现，属既有失败）。
- `gofmt -l internal/payment/ internal/service/`：无输出。
- `go vet -tags=unit ./internal/payment/ ./internal/service/`：无输出。
- 本机未安装 `golangci-lint`，该步骤未执行。

新增测试：

| 文件 | 覆盖 |
|---|---|
| `internal/payment/load_balancer_test.go` | 倍率与限额读同一个键（Stripe `stripe` 键、`legacyVisibleMethodAlias` 别名回退）；未配置时保持 `nil` 而非 0/1；倍率不参与限额过滤 |
| `internal/service/payment_amounts_bonus_test.go` | 解析优先级 11 个用例（渠道覆盖全局、显式 1.0 覆盖、`nil`/0/负数/NaN/Inf 回落全局、双非法归 1.0）；渠道与全局不相乘（显式断言结果不是 2.4） |
| `internal/service/payment_config_bonus_validation_test.go` | `limits` JSON 校验 15 个用例（`null`/缺失视为未配置、0/负数/字符串/布尔/对象被拒、数组与畸形 JSON 报 `VALIDATION_ERROR`）；`ChannelLimits` 反序列化；显式 1.0 保持非 nil |
| `internal/service/payment_order_bonus_test.go` | **端到端**驱动真实 `CreateOrder`（EasyPay 实例 + `httptest` 上游桩 + 内存 ent）并断言落库订单 |

端到端用例断言的订单字段：

- 渠道 1.2 / 全局 1.0 → `amount=120`、`pay_amount=100`
- 渠道覆盖全局：1.2 对 2.0 得 120 而非 240
- 渠道未配置 → 回落全局 1.5 → `amount=150`
- 两者皆无 → `amount=100`
- 渠道 `singleMax=100` 且实付 100 时，到账 120 仍允许下单
- 非法渠道值（0）→ 回落全局
- 显式渠道 1.0 → 覆盖全局，`amount=100`

未单独覆盖（已由既有测试承担）：订阅订单金额恒等于套餐价；回调金额校验仍比对 `pay_amount`；发放使用订单 `amount`。本次改动未触及这些路径，且 `amount`/`pay_amount` 的语义与写入位置保持不变。

### 前端

- `vitest run`：2172 通过，2 失败（`ChannelMonitorView.grok.spec.ts`、`GroupsView.codexManifest.spec.ts`），两者均已在 `git stash` 后的干净 main 上复现，与本次改动无关。
- `vue-tsc --noEmit`：通过。
- 改动文件的 ESLint：通过。

新增用例：

- `src/views/user/__tests__/PaymentView.spec.ts`（+6）：选中渠道倍率 1.2 覆盖全局 2.0 并显示 1.20 倍率与 120（不断言 240）；无渠道倍率回落全局 1.5；渠道显式 1.0 隐藏赠送行；非法渠道值（0）回落全局；`bonus_multiplier_varied` 显示估算提示；倍率一致时不显示提示。
- `src/components/payment/__tests__/PaymentProviderDialog.spec.ts`（+5）：填写 1.2 序列化进 `limits`；显式 1 被保留；留空不写入且保留同级限额；0 与负数不被序列化；已存值回显。

## 验证边界

- **7.7 手动联调未执行**：需要在运行环境配置两个同 `PaymentType` 的渠道实例并分别充值。自动测试用 `httptest` 上游桩覆盖了金额与落库路径，但未验证真实网关回调后的余额发放。
- **同类型多渠道的展示误差**：用户端 checkout 的 `bonus_multiplier` 是跨实例的聚合值，多渠道倍率不一致时标记 `bonus_multiplier_varied=true` 并在页面提示「最终到账金额以实际支付渠道为准」。最终值以订单 `amount` 为准。这是既有 `pcAggregateMethodLimits` UNION 聚合语义的代价，未在本次消除。
- **`InstanceSelection` 构造点**：全局搜索确认仅 `DefaultLoadBalancer.buildSelection` 一处构造，已填入倍率；`visibleMethodLoadBalancer` 只是转发到内层，不自建选择结果。
- **未设倍率上限**：仅要求 `> 0` 且有限。误填 100 会送爆，建议后续在管理端加 `max` 提示。
- **`openspec validate` 未执行**：本机未安装 openspec CLI。
