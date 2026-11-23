# MV2-008 开发记录

日期：2026-11-23。基于已验收提交 `429fe90`。状态：开发中，未整项验收、未提交。

## 早期开发记录（历史时间点，最终状态见末尾）

付费卡密只承担真实充值：创建时分别登记实付人民币、站点显示货币的到账金额、渠道、批次和可选凭据。服务端冻结当次到账换算，客户端不能传入换算率或操作者身份；更改站点换算不会改写既有卡密。原始实付缺失的普通卡密不推断售价，允许管理员为未使用卡密补充真实销售资料。

兑换在一个数据库事务中完成当前用户校验、卡密状态转换、付费来源、付费流水和活动事实。在线来源与卡密共用 `CreditMarketingPayment` 的首充顺序；当前在线支付控制器接入尚未完成，不能把内部统一函数的测试称为线上渠道验证。兑换后立即调用活动处理服务；奖励处理失败不回滚已完成收款，也不提示用户再次付款，保留真实待处理责任。

已使用卡密不可重新启用、改价或改写销售来源。同用户成功重试返回既有结果，不重复到账或写充值日志；不同用户不能复用。软删除保留历史销售事实与支付关联。管理员权限由当前数据库身份复核，不能靠过期角色声明授权。

卡密所有数据库读写使用局部日志保护，调试模式也只显示 SQL 占位符与脱敏错误码；不改变其他业务的调试开关。业务失败日志不输出完整卡密。包含本张或本批已知卡密的销售资料被拒绝，不宣称可自动识别管理员粘贴的任意其他凭证。

已有营销账本的账户拒绝旧通用编辑器直接增加、减少或覆盖余额，防止赠送伪装为付费金额或破坏在途占用。正式记录赠金、来源核对与退款操作属于 MV2-011，当前没有增加新的管理员产品操作。

新增 14 个接口提示均通过规定脚本补齐七种前端语言并执行 i18n 同步；没有修改 React 页面，卡密管理界面的最终金额表单由 MV2-012 接入。

## 已运行证据

- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：r1 与最终 r2 均退出 0。最终日志 `build/dev/mv2-008-accounting-r2.log`；真实 SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11，包含 `TestPaidRedemption`、既有活动/奖池/资金模型及 service 生命周期。无跳过；r2 包含调试日志脱敏回归。
- `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`：r1、最终 r2 退出 0，最新 `build/dev/mv2-008-migration-r2.log`。三库新建、正式 `custom/main` 基线 `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12` 升级、重复启动、历史索引及唯一性均保留。r2 明确保留历史 JSON 全部字段，只允许新增加的五个销售字段以零值补入对比，不能通过删掉旧结构字段绕过核对；额外检查不输出到 JSON 的卡密换算证据未被伪造。没有迁移旧营销数据。
- `python3 .dev/marketing-v2-requirements/validation/payment_process.py all`：真实两个网关进程、PostgreSQL 17.11、Redis 7.2.17 同一逻辑库 11，退出 0。最新已完成证据 `build/dev/marketing-v2-validation/payment-process-20261123T024925.698774Z.log`。六类卡密写权限、伪造身份/换算、双用户同码、同用户两码首充顺序、即时首次/单笔/累计奖励、付费与赠金隔离、旧卡密缺实付拒绝、已用码保护、软删除追溯均通过。真实 root 二次验证降权后，另一网关重放旧 JWT 被拒绝；Cookie-only 写请求与非法 Origin 被拒绝。该次双网关运行在随后 SQL 日志保护前，日志保护由 r2 三库回归验证。

真实 HTTP 夹具只为独立测试显式建立可信新账户来源，没有把这个测试准备步骤称为注册接入；注册仍由 MV2-009 实现。

## 失败记录及修复

`build/dev/mv2-008-controller-r1.log` 保留最初 controller 回归失败：原批删及用户金额审计测试使用已退役的旧 access token，当前认证正确返回 401。将测试改用真实登录会话 JWT 和当前带 `user:write` 的 PAT，保留原业务及审计断言，未放宽生产认证。

随后 `python3 .dev/marketing-v2-requirements/validation/payment_process.py controller` 退出 0：SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11 各自执行批删及 12 项金额/本金/用户管理顶层回归，独立日志库，无跳过。六份证据为 `build/dev/marketing-v2-validation/payment-controller-{sqlite,mysql,postgres}-{batch,management}-20261123T025448.080403Z.log`。旧编辑器保护明确断言 `ErrMarketingBalanceEdit`，不能用任意数据库错误冒充业务拒绝。

## 安全参考与范围

依据 OWASP Authentication、Session Management、Authorization、CSRF Cheat Sheets 与 ASVS 5.0.0 的相关服务端权限、会话撤销和跨站请求控制。证据目录 `build/dev/mv2-006-owasp/`、`build/dev/mv2-008-owasp/`。API 写入要求 Authorization；不能把未授权 OPTIONS 的通配 CORS 响应直接解释为 Cookie 已获写权限。实际 Cookie-only 请求和权限撤销后的请求已验证数据库不变。此处不是整个站点的 ASVS 合规声明。

## 尚未完成，不能标记通过

用户已关闭 N07：本站只使用易支付和小店付费卡密，全部人民币支付，不扩展外币换算。独立代理已批准 A01 收口并保留其他通道防绕过检查，见 `MV2-008-scope-review.md`；原冻结历史保留。易支付与对应人工补单正在接统一入账，完整 A01–A05 尚未独立验收。此前外币渠道研究仅保留历史，不作为本期开发义务。

A01 经用户范围确认与独立评审修订，A02–A05 不变。不得因付费卡密子部分通过而将 MV2-008 标记完成或提交为已验收 Issue。

## 最终候选实现与范围（2026-11-23）

上述“在线尚未完成”和r2等记录是历史阶段，不表示当前状态。用户确认只收人民币：易支付、小店付费卡密；现站USD显示计价单位兑人民币为1:1，但不得写死换算或将1 quota视为1元。不扩展外币业务。A01修订已独立批准，其余标准原文保留。

现已完成易支付下单冻结、验签后金额/商户核对、对应人工补单与两种来源统一入账。实际人民币整数分、到账金额/换算快照、可信来源及活动事实同事务保存；人工补单与回调共用来源去重。付款失败不发奖，成功后的奖励执行失败保留责任，不提示重复付款。其余支付通道即使完整配置也不能下单或回调入账，内部旧直加余额入口明确拒绝。三条外币订阅下单路径同样提前拒绝；这不声称本站订阅整体下线已完成，后续切换Issue仍需处理剩余订阅入口。

独立早期检查发现回调/兑换事务在等待数据库锁前取时间可能使活动资格或卡密到期判断回溯。已在锁住用户、账户及原订单/卡密、完成重放判断后采样实际入账时间，统一写入账本/来源/事实；重放沿用原时间。三库测试通过数据库读取屏障验证，不用sleep。该时间是受锁保护的入账操作时间，不声称是数据库物理commit时间。

接口提示最终新增19条，七种语言齐全；未增加React页面。既有余额编辑器对营销账户拒绝无来源增减，正式赠送/核对操作由011/012完成。

## 最终验证与证据边界

- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：最终r4退出0，`build/dev/mv2-008-accounting-r4.log`。SQLite3.50.4/MySQL8.4.13/PostgreSQL17.11全部通过、无SKIP，含新的Epay、付费码、时间边界以及已有资金、活动和service回归。
- `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`：最终r3退出0，`build/dev/mv2-008-migration-r3.log`。真实三库新建及正式custom/main ca5af4c升级、重复启动，旧数据/索引/约束保持；明确逐字段保留旧JSON，仅允许声明的新字段零值，不隐藏历史字段丢失。随后时间/HTTP修复无schema变更。
- `python3 .dev/marketing-v2-requirements/validation/payment_process.py all`：最终退出0，`build/dev/marketing-v2-validation/payment-process-20261123T061428.874765Z.log`。两真实网关进程共用PG17.11与Redis7.2.17/DB11，真实HTTP登录、易支付Purchase、签名GET/POST回调、人工补单竞争、卡密/在线首充累计及换算变更快照通过。坏签名/金额/商户/非成功状态不入账，完整配置外部通道拒绝；额外两进程各测三条外币订阅下单拒绝。卡密权限、Cookie-only/Origin、撤权重放均保留。仅外部支付商被模拟。
- `python3 .dev/marketing-v2-requirements/validation/payment_process.py controller`：最终退出0。六份`payment-controller-{sqlite,mysql,postgres}-{batch,management}-20261123T061503.822462Z.log`，每库batch1组+management20组，独立日志库、无SKIP。
- `.dev/run go test ./controller -count=1 -timeout=300s`：最终r2退出0，`build/dev/mv2-008-controller-regressions-r2.log`，186.077s。此前`mv2-008-package-regressions-r1.log`的model全包通过，controller因六组旧夹具未建007活动表失败；保留失败日志，仅补测试schema/cleanup，六组定向r2与最终全包均通过，没有削弱业务断言。
- `git diff --check`：退出0。

源码、测试、文档、验证脚本、最终产物和日志绑定`build/dev/mv2-008-review-manifest-r1.json`。仍需独立A01–A05整项验收，未提前标记完成、未提交。没有推送、发布或部署。

## 独立整项验收完成

2026-11-23：独立代理 `/root/mv2004_path_audit` 完成A01–A05及适用共同约束整项复核，全部PASS，见 `MV2-008-acceptance-r1.md`。审查期间43文件/38产物未变，另独立执行精确SQLite入账/兑换回归通过；早期宽过滤的跳过/空匹配不作为三库证据。现在只更新Issue完成状态及索引，按流程提交本项后进入009。
