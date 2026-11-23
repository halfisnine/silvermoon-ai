# MV2-008 在线支付字段核对

2026-11-23，独立只读代理 `mv2004_path_audit` 核对官方文档及当前 SDK。用户随后明确本站仅易支付和小店卡密，全部人民币支付，N07 已关闭。本文保留为历史研究，不是本期实现范围、阻塞项或验收依据；不据此开发外币换汇。

| 渠道 | 实付依据 | 不能混用的字段 |
| --- | --- | --- |
| Stripe | 已验签 Session 的 `amount_total/currency`；存在 Adaptive Pricing 时另外读取 `presentment_details.presentment_amount/presentment_currency` 作为用户原币实付 | integration currency 不一定是用户实际付款币种；`TopUp.Money` 当前是到账计价单位，非实际现金 |
| Creem | 已验签 `checkout.completed` 的 `object.order.amount_paid/currency`，同时核对一次性订单和 `order.status=paid` | `amount/sub_total` 与本地产品 `Price` 可能是折前价；不代替 `amount_paid` |
| Waffo | 已验签成功通知的 `finalDealAmount` 配 `userCurrency`；同币种缺省可用 `orderCurrency` | `orderAmount` 是商户订单金额，用户服务费、换币、税费可能导致实付不同 |
| Waffo Pancake | SDK 验签后的原始通知补读 `data.chargedAmount/currency` | 旧 `amount` 在实收缺失时回退标价；`taxAmount` 已废弃，不得将其再次加到含税实付上 |

Stripe minor unit 按支付币种和 Stripe 规则处理，JPY 等不统一除 100，ISK/UGX 有兼容特例。当前 stripe-go/v81.4.0 的 typed session 未覆盖 `presentment_details`，可使用项目 JSON wrapper 读取已验签事件原文，不为此直接升级依赖。

Creem 官方示例明确含税 `amount_paid`：商品 1000、税 210、实付 1210；折扣案例保留原 `amount/sub_total`，实付从 `amount_paid` 读取。官方还存在 `fx_amount/fx_currency/fx_rate`，但本轮证据未明确所有 webhook 场景和单位，不把这些字段擅自当用户原币实付。原代码 test mode 且无 secret 时允许跳过验签，不能将此路径标为可信真实收款。

Waffo 官方将 `finalDealAmount` 定义为用户最终实际支付金额，金额为十进制展示字符串；`userCurrency` 仅跨币种时必填。当前旧 SDK 的商户税结构不完整，因此不能自己从订单金额推算实付。

Pancake 官方自 2026-09-21 起明确支付事件 `amount` 优先等于实际渠道 `chargedAmount`，但后者缺失时会退回标价。严格实付来源不能把这种回退标价当已核实现金。当前 SDK v0.3.1 没有 `chargedAmount` 字段，可验签后补读原文；字段缺失与零金额不同，后续应保存真实缺失原因并等待可信核对，具体入账策略须符合已确认规则。

来源应保留订单配置、实际原币金额/币种、人民币营销基数及换算依据、钱包到账换算。验签成功仍需与本地订单、提供方、环境绑定，人工补单和回调共用稳定入账来源。缺失实付、折扣、零支付、重复通知不能都回退到商品标价。

## 官方证据

- [Stripe Adaptive Pricing](https://docs.stripe.com/payments/currencies/localize-prices/adaptive-pricing.md?payment-ui=stripe-hosted)、[Currencies](https://docs.stripe.com/currencies.md)
- [Creem Webhooks](https://docs.creem.io/code/webhooks.md)、[Create checkout](https://docs.creem.io/api-reference/endpoint/create-checkout.md)
- [Waffo Order inquiry](https://waffo.com/docs/api-reference/%E6%94%B6%E5%8D%95%E5%95%86%E6%88%B7api%E6%8E%A5%E5%8F%A3/order-inquiry.md)、[Coupons](https://waffo.com/docs/en/developer-docs/use-cases/coupons.md)
- [Pancake Webhooks](https://docs.waffo.ai/api-reference/webhooks.md)

本地原文在 `build/dev/mv2-008-contract-research/`。关键位置：`stripe-adaptive-hosted.txt` 62–83，`creem-webhooks.txt` 228–232/372，`creem-checkout.txt` 585–628，`waffo-inquiry.txt` 256–271/330–345，`waffo-coupons.txt` 122，`pancake-webhooks.txt` 172/178/238。以上行号属于本次下载的原文快照，不是在线页面永久锚点。
