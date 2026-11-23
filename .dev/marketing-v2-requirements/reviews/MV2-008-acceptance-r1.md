# MV2-008 整项独立验收 r1

日期：2026-11-23。审查者：`/root/mv2004_path_audit`。结论：**A01–A05 全部 PASS，本 Issue 适用共同约束 PASS，可标记开发完成并提交。** 本文不代表后续 Issue、整体营销 UI、生产支付接入或上线验收完成。

## 验收对象与冻结校验

- 基础提交：`429fe909ed98ab52f9a35107459781a595a614c4`，加本轮未提交候选变更。
- 清单：`build/dev/mv2-008-review-manifest-r1.json`，SHA-256 `b6c711c3e308b94781d4a8abd8a10ced1b988c30dc9aa6a080ae99d519634d84`。
- 独立核对 43 个文件、38 个产物/日志 hash 均一致。15 个 Issue 验收正文与清单一致；008 逐字等于经本审查者批准的人民币范围修订，其他 Issue 与 HEAD 原文一致。共同约束与 HEAD 逐字一致，hash 为 `cd8c0a154988ce59c5976a99c4a196cf15412ff1d1f0a5bd288739dac400638d`。
- 修订历史 `issues/acceptance-revision-MV2-008-20261123.json` 保存原/新验收正文和对应摘要，原 `acceptance.sha256.json` 未覆盖。Issue 摘要范围为标题后换行之后至开发记录之前；共同约束摘要包含标题后紧跟的换行，不包含标题。
- 本次只新增本验收报告与独立检查产物；未改生产实现、测试、验收项或 Issue 完成状态。

## 逐项结论

| 验收项 | 结果 | 独立核对依据 |
| --- | --- | --- |
| A01 | PASS | 易支付冻结人民币整数分、商户和到账换算；验签成功后核对金额/商户/来源订单，在同一数据库事务写订单状态、用户本金、MarketingPayment、ledger、活动事实。卡密与补单接同一记账核心；补单和回调共用 topup:<ID> 来源。四个非支持通道创建/通知入口拒绝，旧内部充值 helper 拒绝且事务回滚，人工补单拒绝其他 provider；三条外币订阅下单也在外部请求前拒绝。完整配置及启用计划不绕过。 |
| A02 | PASS | 卡密仅真实付费，分别登记售价、到账金额、渠道、批次、可选凭据；换算来自服务端配置并冻结，客户端不能指定换算或操作者。无实付资料的码拒绝，不猜售价。已用码不可改价、重新启用或覆写来源；软删除保留追溯。卡密/在线充值竞争共用用户锁、支付顺序及首充事实，累计规则依据实付。 |
| A03 | PASS | 核对实付与面值不同的活动门槛、首次不达标不重置首充、单笔及累计即时奖励；赠金在独立资产表，不重新产生付费事件。坏签名、错金额、错商户、非成功状态不入账/发奖。来源锁后才采样首次入账时间，卡密到期和活动结束边界均有确定性真实数据库回归；重放不重新取时。 |
| A04 | PASS | 保留真实易支付 SDK 验签及原请求解析边界测试；数据库行锁、条件更新、来源唯一性和共同事务决定正确性，不依赖进程内订单锁或 Redis 临时锁。SQLite/MySQL/PostgreSQL 均有真实重叠事务，双真实网关共用 PostgreSQL/Redis 验证重复回调、卡密争用、在线/卡密首充以及回调/补单竞争。 |
| A05 | PASS | 人工补单要求当前数据库管理员权限、明确人民币实付与凭据；与回调同源去重。营销账户旧余额编辑被拒绝，不能把管理员赠送伪装为实付。卡密写接口有现行认证/服务端角色复核，用户兑换身份取认证上下文；匿名、普通用户、Cookie-only、伪造身份、跨网关撤权后重放均被拒绝且数据不变。 |

## 代码层关键核对

检查 `model/marketing_topup.go`、`model/marketing_redemption.go`、`model/redemption.go`、`model/topup.go`、相关 controller 与服务金额转换，并追踪已验收的 `CreditMarketingPayment` 到活动捕获/处理调用链。

易支付内部基础计价单位的 `CurrencyPerUnit=1` 是原 Amount 单位定义，不是把人民币或 quota 写死为一元。实际人民币依据发送给易支付的两位小数金额及已验签通知核对；到账依据下单时配置的 QuotaPerUnit。卡密依据当次站点显示配置转换，实付人民币另存。实际双网关通过配置接口将 QuotaPerUnit 从 50000 改为 70000，旧订单仍到账 600000，新订单到账 840000，同为人民币 1200 分。没有为本期引入外币换算产品。

早期独立检查发现锁等待前取时可能回溯入账资格，候选已修复：生产传入 clock，持有用户/账户/来源锁并排除重放后首次采样；显式 now 的模型测试契约保留。三库 source-row 读取屏障验证取时顺序、过期拒绝、活动结束无奖励及重放不取新时间。该证据证明锁后记账时间，不宣称测量数据库物理 commit 时刻。

已收款用户即使被停用也保留真实入账事实，不能把成功付款丢弃；用户主动卡密兑换要求有效用户。奖励执行在成功入账后立即尝试，失败保留数据库责任，不回滚收款或要求再次付款。没有引入旧营销的手动激活、人工审核奖励、指定模型/分组限制、赠送码或订阅营销。

## 验证证据

父任务最终证据已独立读取并与冻结清单核对：

1. `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：`build/dev/mv2-008-accounting-r4.log`，真实 SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11；新易支付/卡密、重叠事务、时间边界及既有资金/活动/service 回归通过，无 FAIL/SKIP。
2. `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`：`build/dev/mv2-008-migration-r3.log` 及清单中的各库候选日志。新建、从正式 custom/main `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12` 基线升级、重复启动通过。旧记录逐字段比较，只允许声明新增字段零值；旧唯一约束通过实际冲突验证，内部快照未伪造。日志保留此前运行并追加，已核对末尾本轮包含新增支付字段的成功结果。没有迁移旧营销数据，也不声称测试了 MySQL/PG 最低支持版本。
3. `python3 .dev/marketing-v2-requirements/validation/payment_process.py all`：`build/dev/marketing-v2-validation/payment-process-20261123T061428.874765Z.log`，真实两个网关 PID、PostgreSQL 17.11、Redis 7.2.17 同逻辑库 11。只模拟外部支付，不模拟账本。核对真实登录、支付下单、MD5 GET/POST 通知、即时奖励、付费码与在线首充/累计、补单竞争、凭据/权限/Origin/撤权、全配置未支持通道及订阅下单拒绝。
4. `python3 .dev/marketing-v2-requirements/validation/payment_process.py controller`：六份 `payment-controller-{sqlite,mysql,postgres}-{batch,management}-20261123T061503.822462Z.log`，每库 1 个批删组及 20 个管理顶层组通过，0 SKIP，独立日志库。测试二进制 SHA-256 `2b711e401cba445d2ca1c534c3c1c3117931e30fad68c8b4d88053e5e5681c4e`。
5. `.dev/run go test ./controller -count=1 -timeout=300s`：`build/dev/mv2-008-controller-regressions-r2.log`，退出 0，186.077s。此前失败由旧测试缺活动 schema 导致，修复只增加 fixture schema/cleanup，未弱化行为断言。

审查者独立执行：

- `.dev/run go test ./model -run '^TestPaidRedemption$/^sqlite$|^TestEpayMarketing$/^sqlite$|^TestRechargeEpay' -count=1 -timeout=240s -v`：退出 0，`build/dev/mv2-008-independent-model-r1.log`，无 FAIL/SKIP。
- 初次独立命令运行 model/service/controller 的宽过滤，`build/dev/mv2-008-independent-r1.log` 保留：model 与 controller 通过，但 TestEpayMarketing 的 MySQL/PG 因本地未注入 DSN 跳过，service 无匹配测试。该日志不作为三库通过证据；三库依据前述完整矩阵，随后独立模型命令精确限定 SQLite 消除误导。
- `git diff --check`：退出 0。
- 19 个新增接口文案在七种 locale 均存在；本期未增加 React 页面，不把后续管理/用户 UI 作为已经完成。

## 安全与范围限制

依据现有 OWASP Authentication、Session Management、Authorization、CSRF Cheat Sheets 与 ASVS 5.0.0 相关服务端授权要求检查。认证上下文决定用户、数据库当前角色复核敏感写入、撤权后拒绝旧 JWT，Cookie-only 无写权限；刷新接口验证 Origin/Referer。测试使用隔离 HTTP loopback 检查服务端行为，不能称为生产浏览器 TLS 或整站 ASVS 合规验收。SQL/业务诊断不输出活卡密，付款日志不输出完整签名参数；管理员填写凭据不自动证明外部现金真实，由人工核实负责。

注册可信新账户来源在本轮双网关夹具显式建立，正式注册接入仍属009。正式赠送/核对/退款清零与剩余订阅入口切换属011，卡密/活动管理界面属012，用户页面与消费来源展示属013/014，50–100并发全站容量验收属015。本报告不提前宣称这些后续范围完成，也不允许已有通过标准降级。
