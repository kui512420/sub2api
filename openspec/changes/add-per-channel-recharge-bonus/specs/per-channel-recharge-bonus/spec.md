## Purpose

规定余额充值赠送倍率的渠道级配置模型，以及到账金额的计算与优先级，使同一 `PaymentType` 下不同渠道实例可以提供不同的赠送比例，同时保证未配置渠道倍率时行为与改动前完全一致。

## ADDED Requirements

### Requirement: 渠道实例的赠送倍率必须是可选且可校验的

系统 SHALL 在 `payment_provider_instances.limits` 的每个支付类型条目中支持可选字段 `bonusMultiplier`。该字段 MUST 为正的有限数；系统 MUST 拒绝 `0`、负数、`NaN`、`Inf` 以及非数值类型的值。

#### Scenario: 保存合法的渠道倍率
- **WHEN** 管理员为渠道实例的 `alipay` 条目保存 `{"alipay": {"bonusMultiplier": 1.2}}`
- **THEN** 系统 MUST 保存成功
- **THEN** 重新读取该实例时 `limits` MUST 仍包含 `alipay.bonusMultiplier = 1.2`

#### Scenario: 保存非法倍率
- **WHEN** 管理员提交 `bonusMultiplier` 为 `0`、`-1`、`"abc"` 或 `null`
- **THEN** 系统 MUST 返回 400 且 MUST NOT 落库

#### Scenario: 未配置倍率的渠道
- **WHEN** 渠道实例的 `limits` 不含 `bonusMultiplier`，或 `limits` 为空字符串
- **THEN** 读取结果 MUST 表示「未配置」，系统 MUST NOT 将其视为 `1.0` 以外的默认值

### Requirement: 到账倍率必须按「渠道覆盖全局」的优先级解析

系统 MUST 按以下顺序确定余额充值的到账倍率：选中渠道实例该支付类型的 `bonusMultiplier`（配置合法时）> 全局 `BALANCE_RECHARGE_MULTIPLIER` > `1.0`。渠道倍率与全局倍率 MUST NOT 相乘。

#### Scenario: 渠道已配置倍率
- **WHEN** 渠道实例 `bonusMultiplier = 1.2`，全局 `BALANCE_RECHARGE_MULTIPLIER = 2.0`
- **THEN** 本次余额充值的到账倍率 MUST 为 `1.2`
- **THEN** 系统 MUST NOT 使用 `2.4`

#### Scenario: 渠道未配置倍率
- **WHEN** 渠道实例未配置 `bonusMultiplier`，全局 `BALANCE_RECHARGE_MULTIPLIER = 1.5`
- **THEN** 本次余额充值的到账倍率 MUST 为 `1.5`

#### Scenario: 两者都未配置
- **WHEN** 渠道实例未配置 `bonusMultiplier`，全局 `BALANCE_RECHARGE_MULTIPLIER` 缺失或非法
- **THEN** 本次余额充值的到账倍率 MUST 为 `1.0`
- **THEN** 到账金额 MUST 等于用户实付金额

#### Scenario: 同一支付类型下不同渠道提供不同倍率
- **WHEN** 支付类型 `alipay` 下存在渠道 A（`bonusMultiplier = 1.1`）与渠道 B（未配置），全局倍率为 `1.0`
- **THEN** 路由到渠道 A 的订单到账金额 MUST 为用户实付金额的 `1.1` 倍
- **THEN** 路由到渠道 B 的订单到账金额 MUST 等于用户实付金额

### Requirement: 到账金额必须使用最终选中渠道的倍率

系统 MUST 在负载均衡选定渠道实例**之后**用该实例的倍率计算到账金额。系统 MUST NOT 用用户请求的 `PaymentType` 对应的任意实例倍率、或用下单前不确定的倍率写入订单。

#### Scenario: 下单流程中实例选择先于金额确定
- **WHEN** 用户发起一笔余额充值
- **THEN** 渠道实例的选择 MUST 基于用户实付金额完成
- **THEN** 订单的 `amount` MUST 由选中实例解析出的倍率与该实付金额计算得出

#### Scenario: 选中实例与请求类型不一致时仍使用选中实例
- **WHEN** 用户请求 `payment_type = alipay`，负载均衡在 `alipay` 组内选中了易支付实例
- **THEN** 到账金额 MUST 使用该易支付实例的 `bonusMultiplier`，MUST NOT 使用支付宝直连实例的

### Requirement: 订单金额字段语义必须保持稳定

`payment_orders.amount` MUST 继续表示到账金额（含赠送），`payment_orders.pay_amount` MUST 继续表示网关实收金额。系统 MUST 在支付回调时校验网关实收金额与 `pay_amount` 一致，MUST NOT 因赠送倍率改变该校验目标。

#### Scenario: 支付回调金额校验
- **WHEN** 用户实付 100 且渠道到账倍率为 `1.2`，订单 `amount = 120`、`pay_amount = 100`
- **THEN** 网关回调返回实收 100 时系统 MUST 接受
- **THEN** 网关回调返回实收 120 时系统 MUST 拒绝并报金额不一致

#### Scenario: 余额发放使用到账金额
- **WHEN** 订单完成发放
- **THEN** 发放给用户的余额 MUST 等于订单的 `amount`（含赠送部分）

### Requirement: 下单前置校验必须继续基于用户实付金额

系统 MUST 继续使用用户实付金额（而非含赠送的到账金额）执行最小/最大单笔金额校验、全局日限额累计、渠道日限额校验与「用户请求金额落在所选渠道限额区间内」的校验。

#### Scenario: 渠道单笔上限基于实付金额
- **WHEN** 渠道 `singleMax = 100`，用户请求充值 100 且该渠道到账倍率为 `1.2`
- **THEN** 系统 MUST 允许该下单（实付 100 未超上限）
- **THEN** 系统 MUST NOT 因为到账金额 120 超过上限而拒绝

#### Scenario: 充值金额超出全局区间
- **WHEN** 全局 `max_amount = 50`，用户请求充值 100
- **THEN** 系统 MUST 拒绝并返回金额超范围错误

### Requirement: 订阅订单与其它赠送来源不受渠道倍率影响

系统 MUST NOT 对 `order_type = subscription` 的订单应用渠道赠送倍率。注册优惠码赠送与邀请返佣的计算 MUST NOT 使用渠道赠送倍率。

#### Scenario: 订阅订单
- **WHEN** 用户购买订阅套餐且该渠道配置了 `bonusMultiplier = 1.2`
- **THEN** 订单金额 MUST 等于套餐价格，MUST NOT 被倍率放大

#### Scenario: 邀请返佣
- **WHEN** 一笔含赠送的余额充值订单完成并触发邀请返佣
- **THEN** 返佣金额 MUST 按既有规则基于该订单的既有计算口径得出，MUST NOT 因渠道倍率产生新的计算分支
