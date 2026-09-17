## Purpose

规定渠道级赠送倍率在管理端渠道配置界面与用户端支付页的呈现与交互，使管理员可配置、用户在下单前能准确看到到账金额。

## ADDED Requirements

### Requirement: 渠道配置对话框必须提供赠送倍率输入

管理端渠道实例配置对话框（`PaymentProviderDialog.vue`）的限额折叠区 SHALL 为每个受支持的支付类型提供「赠送倍率」输入项，与单笔下限、单笔上限、日限额并列。

#### Scenario: 展示与留空
- **WHEN** 管理员打开渠道配置对话框且在限额区看到某支付类型
- **THEN** 该支付类型下 MUST 显示赠送倍率输入框
- **THEN** 留空时该字段 MUST NOT 被写入 `limits` JSON

#### Scenario: 保存倍率
- **WHEN** 管理员在 `alipay` 的赠送倍率填入 `1.2` 并保存
- **THEN** 提交的 `limits` MUST 为 `{"alipay":{"bonusMultiplier":1.2}}` 或包含该键
- **THEN** 重新打开该渠道配置时 MUST 回显 `1.2`

#### Scenario: 非法输入被拒绝
- **WHEN** 管理员填入 `0` 或负数
- **THEN** 表单 MUST 阻止提交或后端 MUST 拒绝保存，并给出可见错误提示

### Requirement: 用户端到账金额必须按所选渠道展示

用户端支付页 SHALL 在用户选定支付渠道后，按该渠道解析出的倍率展示预计到账金额；未选定渠道时 MUST 使用全局倍率或 `1.0`。

#### Scenario: 选择配置了倍率的渠道
- **WHEN** 用户选择渠道 A（`bonusMultiplier = 1.2`）并输入 100
- **THEN** 页面 MUST 显示预计到账 120

#### Scenario: 切换到未配置倍率的渠道
- **WHEN** 用户在已选择渠道 A 后切换到未配置倍率的渠道 B，全局倍率为 `1.0`
- **THEN** 页面 MUST 将预计到账更新为 100

#### Scenario: 未配置倍率时展示与实付一致
- **WHEN** 所选渠道与全局均未配置倍率
- **THEN** 页面 MUST 显示预计到账等于用户输入金额
