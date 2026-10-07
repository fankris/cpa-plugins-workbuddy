# 界面术语约定

## 不随界面语言改变
- 渠道名称：**CN**、**Intl**，严格保留大小写。不使用地理名称或翻译词替代。
- 产品名与缩写：WorkBuddy、CodeBuddy、CPA、CPAMC、CPAMP、WB、CB。
- 协议及技术标识：API、SDK、HTTP、OAuth、JSON；模型 ID、API 字段名、配置键、URL/path 原样保留。
- 用户账号名称与上游模型名称：保留数据原值，不对用户内容做批量文本替换。

## 可以本地化
标题、动作、状态、错误解释、操作提示等自然语言。示例：
- 简体：仅 CN；Intl 账号业务；CN / Intl。
- 繁体：僅 CN；Intl 帳號業務；CN / Intl。
- English：CN only；Intl account services；CN / Intl。
- Русский：Только CN；Сервисы аккаунта Intl；CN / Intl。

固定名称由 i18n 的 fixedLabel 生成四语言一致值，并由自动测试约束。技术字段仍使用现有 cn / intl，兼容旧 global；这些协议值不改成界面大小写，不改变存储或路由。未知渠道显示“渠道未确认”，不臆测为 Intl。
