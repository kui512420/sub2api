## 1. 数据模型与读取路径

- [x] 1.1 `backend/internal/payment/load_balancer.go`：`ChannelLimits` 新增 `BonusMultiplier *float64 \`json:"bonusMultiplier,omitempty"\``，注释说明该结构承载渠道级参数（限额 + 赠送倍率）而不仅限额
- [x] 1.2 `backend/internal/payment/types.go`：`InstanceSelection` 新增 `BonusMultiplier *float64`（区分「未配置」与「=1.0」）
- [x] 1.3 `load_balancer.go` 构造 `InstanceSelection` 的各处填入 `BonusMultiplier`（取自 `getInstanceChannelLimits(inst, paymentType).BonusMultiplier`）；确认 Stripe 走 `stripe` 键、别名渠道走 `legacyVisibleMethodAlias` 回退，与限额同源
- [x] 1.4 全局搜索所有 `InstanceSelection{` 构造点，确认无遗漏导致倍率静默失效

## 2. 倍率解析与金额计算

- [x] 2.1 `backend/internal/service/payment_amounts.go`：新增 `isValidBonusMultiplier(v float64) bool`（排除 `NaN`/`Inf`/`<= 0`）
- [x] 2.2 同文件新增 `resolveChannelBonusMultiplier(bonus *float64, global float64) float64`：渠道合法值优先，否则回落 `normalizeBalanceRechargeMultiplier(global)`
- [x] 2.3 同文件新增 `resolveSelectionBonusMultiplier(sel *payment.InstanceSelection, fallbackGlobal float64) float64`：`sel == nil` 或 `sel.BonusMultiplier == nil` 时回落全局
- [x] 2.4 保持 `calculateCreditedBalance(paymentAmount, multiplier)` 签名不变，仅改变调用处的倍率来源

## 3. 下单流程调整

- [x] 3.1 `backend/internal/service/payment_order.go` `CreateOrder`：移除「余额充值在选实例前用全局倍率计算 `orderAmount`」的分支，`orderAmount` 暂用实付金额占位
- [x] 3.2 在 `validateSelectedCreateOrderInstance` 之后、`createOrderInTx` 之前插入：`if plan == nil && req.OrderType == payment.OrderTypeBalance { orderAmount = calculateCreditedBalance(req.Amount, resolveSelectionBonusMultiplier(sel, cfg.BalanceRechargeMultiplier)) }`
- [x] 3.3 确认 `limitAmount` 全程保持为用户实付金额（用于日限额累计、渠道限额校验、`pay_amount` 计算），未被倍率污染
- [x] 3.4 确认订阅路径 `orderAmount = plan.Price` 不受影响
- [x] 3.5 检查 `createOrderInTx` 与 `invokeProvider` 的参数传递，确认 `orderAmount`（到账）与 `payAmount`（实收）各自去向正确，未发生互换

## 4. 管理端校验

- [x] 4.1 `backend/internal/service/payment_config_providers.go`：创建/更新渠道实例时校验 `limits` JSON 中每个条目的 `bonusMultiplier`，非法值返回 400（错误码建议 `INVALID_BONUS_MULTIPLIER`）
- [x] 4.2 校验失败时 MUST NOT 落库；`bonusMultiplier` 缺失或 `null` MUST 视为未配置而非错误
- [x] 4.3 检查 `limits` 是否还有其他写入路径（管理端 API 之外）

## 5. 接口透传

- [x] 5.1 `backend/internal/handler/payment_handler.go`：checkout 响应让前端能按渠道取到倍率（`MethodLimits` 或 `checkoutInfoResponse` 增加渠道级 `bonus_multiplier`，保留现有全局字段以兼容）
- [x] 5.2 确认管理端 `GET /admin/payment/providers` 原样返回 `limits` 字符串，无需改动

## 6. 前端

- [x] 6.1 `frontend/src/components/payment/PaymentProviderDialog.vue`：限额折叠区每个支付类型新增「赠送倍率」输入框；`limits` 响应式结构与 `getLimitVal`/`setLimitVal`/`hasAnyLimit`/`serializeLimits` 支持 `bonusMultiplier`（注意现有序列化会丢弃 `<= 0`，倍率需按 `> 0` 保留）
- [x] 6.2 打开已有渠道配置时解析并回显 `bonusMultiplier`
- [x] 6.3 `frontend/src/views/user/PaymentView.vue`：`creditedAmount` 改为按所选渠道倍率计算（渠道倍率缺失时回落全局）；切换渠道时更新展示
- [x] 6.4 `frontend/src/types/payment.ts` 与 `frontend/src/api/admin/payment.ts`：补充类型
- [x] 6.5 中英文 i18n：赠送倍率标签、留空提示（使用全局倍率）、非法值提示
- [x] 6.6 管理端输入框填写 `0` 或负数时阻止提交或给出提示

## 7. 验证

- [x] 7.1 后端：`go build ./...` 通过；`go test -tags=unit ./internal/service/ ./internal/payment/ ./internal/handler/` 通过（仅剩 2 个改动前就存在的失败）
- [x] 7.2 后端：`gofmt -l` 无输出、`go vet -tags=unit` 无输出（本机未安装 golangci-lint，未执行）
- [x] 7.3 新增单元测试：渠道倍率解析优先级（渠道 > 全局 > 1.0）、渠道与全局不相乘、渠道 `nil` 回落全局、非法渠道值回落全局、渠道 JSON 键解析（Stripe 键 / 别名键）、倍率不参与限额过滤
- [x] 7.4 新增端到端测试（真实 `CreateOrder` + 落库）：到账金额按选中实例计算、渠道覆盖全局、回落全局、两者皆无、渠道上限基于实付金额、非法值回落、显式 1.0 覆盖
- [x] 7.5 前端：`vitest run` 通过（仅剩 2 个改动前就存在的失败）；`vue-tsc --noEmit` 通过；ESLint 通过
- [x] 7.6 前端新增用例：支付页按渠道倍率展示到账、切换渠道回落全局、显式 1.0 隐藏赠送行、估算提示、对话框序列化/回显/留空/非法值
- [ ] 7.7 手动验证：为两个渠道分别配置 1.0 与 1.2，用户分别经两个渠道充值相同金额，订单 `amount` 与到账余额符合预期（需运行环境，未执行）
