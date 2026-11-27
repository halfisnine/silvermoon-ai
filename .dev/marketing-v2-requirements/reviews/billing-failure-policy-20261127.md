# 请求失败与资金分账修订（2026年11月27日）

基线：最新 custom/main ace6ac81；普通调用行为对照营销前 ca5af4c3。用户已批准修复，独立 sub agent failure_policy_review（gpt-6.1-sol，只读）已评审以下调整，同意作为本轮验收依据。保留原 Issue 冻结正文及历史结果；本文明确替代其中不兼容的扩大解释，不伪称旧测试仍适用。

## 本轮验收

- [x] F01 普通请求按既有失败策略收尾，503 quota_exhausted、其他上游错误、网络失败不因发送记录自动进入 review。限时赠金、永久赠金、付费及令牌占用原路释放，重复返还不多退，过期赠金不复活。已结算消费、违规费用、已受理任务与多轮Realtime不误退。
- [x] F02 原重试策略决定新尝试；每个尝试保留持久唯一身份，同一尝试重放不重复发送，崩溃恢复不自动重发上游。调整预扣与重试成功只结算应收金额。
- [x] F03 普通调用使用原计价流程计算的金额，包括原流程明确采用的估算或缺失用量回退；记录估算依据，不冒充上游实测，不把预占值直接当实际费用。无效定价、异步未知终态和Realtime未知轮仍保留原账。
- [x] F04 review不自动封锁账号。未知费用自身不阻止其他独立请求，但占用继续影响可用余额。已确认ActualQuota大于SettledQuota的债务仍阻止新增消费，即使CostKnown为false；自身异常Realtime连接仍停止继续产费。来源未核实、账号/令牌权限和账本一致性检查保留。
- [x] F05 初始来源未核实、可用金额不足、已有未付消费与其他账务异常分别提示，支持简中/繁中/英文。reserved/active/settled/review保持；settled只表示账务已完成，不伪称调用成功。
- [x] F06 旧review仅通过现有带证据、版本和防重保护的维护入口逐笔核对；提供操作说明，不批量清零，不直接修改生产数据。清退仍等待未结事项，旧赠金/机会规则不变。
- [x] F07 实际SQLite/MySQL/PostgreSQL回归及两个真实网关共享PG/同Redis逻辑库，验证失败后其他实例调用、重试/返还幂等、正常消费与特殊协议保护。最后独立复核完整F01–F07，修复后复验并本地commit。

## 对原验收的明确修订

MV2-003 A03保留实际不足/已确认债务限制；A05对普通失败返还及原版计价回退增加例外。MV2-004 A01/A04继续遵循原计价；A02不以“发送可能发生”阻止普通失败后的既有重试；A05不将普通计价回退统一置review。MV2-005 A04/A06保留本连接停止、真实债务及迟到结算，取消仅因另一请求未知就停止独立请求的要求。MV2-015 A01/A02/A04的受影响行为按本附录重验。其他冻结标准不变。

产品取舍：普通失败按站点原收费契约处理，不以供应商可能收费为由长期占用用户资金。此规则不证明供应商绝未执行；保留失败尝试事实。未知已受理任务/Realtime、进程崩溃与真实债务仍按各自证据处理。

## 已存在的待核对请求

本次升级不批量改账，也不将所有 review 自动认定为免费。旧记录可能包含真正的异步任务、Realtime 用量或已确认欠费。

1. 先在后台账务查询中按用户和请求编号找出待核对记录，关联原错误日志、调用协议和实际消费。核实原请求已经结束，不会与旧进程迟到结算竞争。
2. 对本次这类普通503失败，按站点既有失败返还契约，确认没有另行结算的用量、已受理任务或违规费用；证据记原请求编号、错误时间、错误码及采用的失败返还规则。无需声称供应商一定没有执行。
3. 使用 [既有本机维护入口](../validation/maintenance-runbook.md) 的只读预览，核对 funding_id、当前 revision 和原占用来源。执行 reconcile 时 version 使用下一版本，final_amount 使用显示币种金额字符串（确认零收费时填 "0"），填写 original_request_id、terminal_evidence、source_evidence 及管理员/授权/停写依据。先不带 --execute 查看结果，再由有权运维逐笔执行。
4. 同一操作必须复用 reference、version 和相同证据。成功后检查请求 settled、原来源占用释放及新增调用可用；不得直接改用户余额、删账本或按HTTP状态批量退款。已到期赠金释放占用后仍受原有效期限制。
5. 来源尚未核验的账户仍须单独完成来源核验；已确认欠费须核实真实费用后处理。退款清退仍需未结事项全部处理完毕。

本轮仅修改代码和验证夹具，没有执行生产账户核对、退款或部署。

## 实现边界

- 请求账务状态仍使用 reserved / active / settled / review，普通失败零费用收尾进入 settled，表示账务处理完成，不代表上游调用成功；原错误日志继续保留调用失败。无需新增用户可见状态。
- CostKnown 表示按本站原计价契约已确定应收金额，不等同于用量全部由上游实测。usage_estimated、usage_confirmed、stream_outcome、calculated_quota 和定价快照仍保存供溯源。
- 新增的是内部发送尝试 failed 状态，区别于可证明执行前拒绝的 rejected；只有原重试策略允许时才能创建另一个唯一尝试。崩溃恢复不重发原尝试。
- 普通失败策略不扩大到异步任务：Task / Midjourney 仍保留发送屏障，包括2xx回执损坏且尚未保存任务ID的情况；未知任务只保留自己的占用。Realtime按轮确认消费、自身异常停止、赠金不足接续付费、真实债务限制均保留。
- 成功空预热仍为零费用；失败空预热释放占用。预热的矛盾或非法用量不会被强行当作零或固定按次收费，仍可待核对。已有输出或可靠正用量使用正常计价。
- 未新增表、字段、索引、中间件或数据库迁移；资金事务仍通过共享业务库的账号锁与持久唯一键实现并发和防重。Redis不成为扣费正确性的唯一依据。

## 本轮验证证据

仅独立 `silvermoon-marketing-v2` 测试栈，无生产数据。SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11；Go 1.26.10 固定工具镜像。未新增数据库专有语法或依赖版本特性。

| 检查 | 实际命令/范围 | 结果及证据 |
| --- | --- | --- |
| 修复前复现 | service 的 ordinary_failure_refund | 原实现失败，保留占用并拦截下一次调用；`build/dev/billing-failure-refund/before.log` |
| 三库模型 | `.dev/run go test -c -o /artifacts/billing-failure-refund/model.test ./model`，隔离容器设置 MV2_MYSQL_DSN、MV2_POSTGRES_DSN、MV2_REQUIRE_DATABASES=1，执行 `-test.run=^TestMarketingPersistence$ -test.v -test.count=1` | 三库通过、无跳过；`matrix-model.log` |
| 三库服务/定价 | 同样编译 service.test，设置上述 MV2 环境及 TEST_FIXED_MYSQL_DSN、TEST_FIXED_POSTGRES_DSN，执行 `-test.run=^Test(MarketingBillingSessionLifecycle\|FixedPriceBillingDatabaseMatrix)$ -test.v -test.count=1` | 三库通过、无跳过；`matrix-service.log`。包括历史普通失败逐笔零费用核对、防重及错误请求ID拒绝 |
| Responses控制器 | `.dev/run go test ./controller -run 'TestResponses\|TestMarketingSingleWebSocketDatabase' -count=1 -timeout=180s` | 通过；`controller-focused.log` |
| 两网关失败路径 | `python3 .dev/marketing-v2-requirements/validation/gateway_process.py build`，随后 `failure` | 11组通过：503三种来源、400/500、网络失败、损坏/空响应、重试成功、过期赠金、异步损坏回执；每组后另一实例成功调用 |
| 两网关综合 | 同一runner的 `run` | 通过：共享账号并发、Realtime到期/自动回复/耗尽/未知保留/可信迟到结算、Redis连接故障、普通计价回退、文本/流式/图片/异步接管 |

三库日志均位于 `build/dev/billing-failure-refund/`，运行命令及专用库配置保存在该目录的 `matrix.py`；首次运行仅因测试库命名保护被拒绝，随后使用 marketing_v2_behavior 前缀的专用空库通过，没有放宽测试保护。

双网关完整证据：`build/dev/marketing-v2-validation/gateway-failure-20261127T122358.027474Z.log`、`gateway-functional-20261127T122409.558151Z.log`，共用 PostgreSQL 测试库与 Redis DB14。外部模型响应为本机可控夹具，网关/数据库/Redis均为真实进程；不宣称已经实测所有商业供应商。

实际网关二进制 SHA256：`fb1c5bde07dbc7761f5de716c9d6d762d522aa3d7729013baae5e7a4659712d7`。源码摘要在 `build/dev/billing-failure-refund/sources.json`。本轮未修改数据库结构，因此没有新增迁移。

最终补充检查：六包 `.dev/run go test ./model ./service ./controller ./i18n ./relay ./relay/channel/xunfei -count=1 -timeout=600s` 全通过（controller 193.913秒），同六包 `go vet` 通过，见 `packages-accepted.log`、`vet-accepted.log`。此前180秒的整包检查超时，不能作为通过证据。

独立复核补充发现预热terminal error自身携带非法usage时也需要校验。最终将当前raw usage校验合并到同一helper，新增 `invalid_usage_error_after_created` 表格用例；`.dev/run go test ./controller -run '^TestResponsesWSMarketingPrewarmKeepsExecutionEvidence$' -count=1 -timeout=90s` 通过（12.344秒），见 `prewarm-last.log`。该最后调整没有改变模型层或三库账务实现，最终版本重新构建并重跑双网关。

最终双网关重验均通过，最终网关SHA256为 `8e5d559271acb0626befd51de0e8adaaa33c519a6e229d600087aa0c5e573a8a`。以下证据替代前一轮二进制绑定：

- `build/dev/marketing-v2-validation/gateway-failure-20261127T123038.605377Z.log`：通过。
- `build/dev/marketing-v2-validation/gateway-functional-20261127T123050.470713Z.log`：通过。

## 最终独立验收

2026年11月27日，`failure_policy_review`（gpt-6.1-sol，只读独立审查）按完整业务逻辑复验F01–F07，结论全部通过、无剩余阻断。复核覆盖最后预热错误帧修复、23份源码摘要、三库无跳过结果、最终双网关二进制及日志、六包测试、vet、历史维护防重和原冻结Issue未被修改。验收通过后执行本地commit；本次不推送、不发布、不部署、不改生产账目。
