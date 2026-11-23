# MV2-012 后端实现与验证交接

日期：2026-11-23。此记录由实现 agent 编写，不代替独立验收，不修改 Issue 验收项。

## 完成范围

- 活动、奖池的分页查询、配置、运行操作及真实奖励来源查询；完整接口契约见 `MV2-012-api-contract.md`。
- 管理浏览器会话认证，事务内重新检查当前管理员角色及启用状态；幂等重放同样检查；并发旧版本覆盖拒绝。
- 按站点金额显示字符串输入输出。服务端保存换算快照，配置修订号防止同币种汇率变化后旧表单误存；成功幂等重放不重新解释金额。
- 四类活动复用新版业务规则，奖池供给/追加/补库/延期复用新版事务规则。没有引入活动赠金预算、赠金激活或人工奖励审批。
- 互斥组索引投影，与规则 Spec 同事务维护。MySQL 默认不区分大小写排序规则下，组名统计和成员查询仍与 Go 字符串身份一致。
- 来源查询按真实入账记录展示，实际支付订单号/付费卡密销售来源/签到日/邀请好友/抽奖奖项；组合奖每份赠金分别展示实际到账和到期时间，不暴露卡密或内部换算值。

## 数据库范围

所有操作只在独立 `silvermoon-marketing-v2` 验证栈进行，未接触线上服务。

| 引擎 | 实测版本 | 管理 HTTP | 付费兑换码 | 迁移 | 计费回归 |
| --- | --- | --- | --- | --- | --- |
| SQLite | 3.50.4 | PASS | PASS | PASS | PASS |
| MySQL | 8.4.13 | PASS | PASS | PASS | PASS |
| PostgreSQL | 17.11 | PASS | PASS | PASS | PASS |

不依赖新增的特定版本数据库功能。未额外运行 MySQL 5.7.8 / PostgreSQL 9.6；本记录不宣称实测过这些版本。

新增 `marketing_v2_activities.exclusive_group` 索引投影；启动以 100 行批次幂等回填新版自身 Spec。没有旧营销数据迁移或导入。正式 `custom/main` 基线为 `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`。

## 实际命令及证据

1. `.dev/run go test -c -o /artifacts/marketing-v2-012-controller.test ./controller`
2. `python3 .dev/marketing-v2-requirements/validation/admin_http_matrix.py > build/dev/marketing-v2-012-admin-matrix-r3.log 2>&1`

上述最新运行退出 0，无 SKIP。控制器二进制 SHA256：`b3935f0f666ab534c8aadf46a68257469173ec91fdb72de7382c60d673ff84f8`。每库日志 `build/dev/marketing-v2-012-admin-http-{sqlite,mysql,postgres}.log` 和 `build/dev/marketing-v2-012-paid-cards.log` 首行保存同一哈希。覆盖 GroupA/groupa 分离、组分页、真实订单号、实际每份奖励有效期、非一比一换算、极小金额、换算修订冲突、幂等/篡改/旧版本/撤权、运行规则锁定、供给不足、补库追加、延期及四类活动请求。

3. `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all > build/dev/marketing-v2-012-migration-r1.log 2>&1`

退出 0；三库全新初始化、正式基线建库升级、重复启动及既有数据验证通过。子日志在 `build/dev/marketing-v2-validation/`，批次 `20261123T092034.991462Z`。此后没有 schema 修改；最新 HTTP 测试还在三库分别模拟缺少投影列的新版结构，迁移/回填两次并验证权威 Spec 保留、索引建立。

4. `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all > build/dev/marketing-v2-012-accounting-r1.log 2>&1`

退出 0，无 FAIL/SKIP。三库模型金额/持久化/奖池/活动/签到/维护/付费兑换/易支付及服务层 `TestMarketingBillingSessionLifecycle` 均通过，包含赠金与付费金额结算、返还、缺失用量、异步任务及实时语音边界。该轮在最后管理查询调整前编译；最终管理查询与来源由第 2 项新二进制复验，不把旧二进制结果作为后续修改的验证。

5. `.dev/run go test ./controller ./router -run '^Test(MarketingAdminHTTP|AccessToken.*)$' -count=1 -v`

通过，日志 `build/dev/marketing-v2-admin-http-r3.log`。覆盖路由声明和个人访问令牌路由策略。最后调整未更改路由或令牌策略。

## 安全指导与验证边界

阅读 OWASP Authentication Cheat Sheet、Session Management Cheat Sheet，以及 ASVS 5.0.0 V6 认证章节。本次沿用正式主线会话机制，不另建登录流程；不声称全站 ASVS 合规认证。

- 新管理路由要求真实 bearer dashboard session；只有 Refresh Cookie 的跨站写请求不能通过。
- PAT 路由策略禁止以个人访问令牌替代管理浏览器会话。
- 当前用户在业务数据库的角色/启用状态于事务内锁定复核，撤权和禁用后即使成功请求重放也拒绝。
- 校验失败可修正后重试；同键不同请求拒绝；成功重复操作不重复发奖或增加供给。
- 响应不返回内部数据库异常、可用兑换码、支付密钥或原始内部规则快照。

代码已交父 agent 和独立验收 agent 审查；本子任务未执行 commit、push、部署。

## 完整包首次失败的诊断

首次完整包执行记录保留为 `build/dev/mv2-012-packages-r1.log`。Responses WebSocket 的超时、两种渠道连接及断连记账测试在宿主高负载期间失败；同一时段普通 SQLite SELECT/UPDATE 耗时达到 1–2 秒，客户端与清理等待上限为 3 秒。controller 包最终触发整包 5 分钟上限，堆栈记录中的最后一个子测试仅运行了 1 秒，不能据此认定该子测试挂起。

再次完整阅读计费规范并检查相关 fixture 生命周期、流式超时及清理链路后，没有修改业务代码、测试断言或任何超时参数。

先用之前的同一控制器二进制独立运行三组失败测试：`-test.run=^TestResponses(InterruptedStreamHealth|WebSocketDialsNativeResponsesChannelTypes|WebSocketDisconnectPreservesUnknownDeliveredOutputOnce)$ -test.v -test.count=1 -test.timeout=5m`。全部通过，总约 10.5 秒；websocket_timeout 为 2.44 秒，sub2api 为 1.09 秒，advanced_custom_query_auth 为 1.08 秒，断连未知用量记账为 1.33 秒。证据 `build/dev/mv2-012-responses-isolated-r1.log`，失败与复验日志哈希记录 `build/dev/mv2-012-responses-failure-evidence.sha256`。

随后采用串行包执行，避免多个测试包与重型构建同时竞争资源：`.dev/run go test -p 1 ./controller ./service ./model ./middleware ./router ./internal/marketing -count=1 -timeout=5m`。controller 全包通过，耗时 213.431 秒。外层工具会话收到 SIGTERM 后，容器内测试仍正常继续，已重新附加容器日志与退出码监听；因此最终应以 `build/dev/mv2-012-packages-isolated-r2-container.log` 和 `build/dev/mv2-012-packages-isolated-r2-exit.txt` 为准，而非外层工具退出码。容器最终退出码 0；六包全部通过：controller 213.431 秒、service 10.550 秒、model 20.217 秒、middleware 5.245 秒、router 0.822 秒、internal/marketing 0.005 秒。

现有证据支持测试运行资源不足导致首次失败，没有发现需修改的 Responses 业务回归。此诊断不构成线上性能或容量结论。

## 最终真实浏览器 gateway

在隔离 SQLite、回环随机端口及随机新管理员环境中，通过真实 setup/login/options/活动管理/注册/付费卡密兑换/签到接口建立四类活动、一个奖池、可编辑草稿与六条实际赠金和机会奖励。站点显示为 CNY，换算值 3。未向数据库直接插入 fixture，后台维护任务正常运行，无真实支付渠道或模型上游。

最终前端构建完成后重新编译 gateway，仅重启自建测试容器，保留数据库、端口和账号。二进制 SHA256：`82341b3b8c1c3d31263d063ec714dc2009f1d7d3ce43c3195ca43a7482f986a0`；1854 文件的 dist manifest SHA256：`764a8431f6296d34275aae9980136eafd68372fd3ebe933adf058a1036d157e4`。实际 HTTP 返回的所有 index 入口 JS/CSS 已逐字节核对最终 dist；index 仅存在主程序原有的 analytics 注释替换。证据为 `build/dev/mv2-012-browser/final-gateway-evidence.json` 与 `dist-final-manifest.json`。浏览器最终业务验收由另一个 agent 独立记录，不能用 gateway 成功启动替代。私密连接与登录信息仅在 chmod 600 的忽略文件中保存，不纳入提交。
