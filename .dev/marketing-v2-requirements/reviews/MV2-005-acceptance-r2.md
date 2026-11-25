# MV2-005 整项独立验收 r2

日期：2026-11-23。审查者：`mv2004_path_audit`（独立 sub agent）。结论：**A01–A06 全部通过，本轮没有剩余阻断，可以更新 MV2-005 完成状态并提交本 Issue。** 此结论仅适用于下列清单绑定版本，不等于全部营销系统、退款维护入口、正式部署或所有供应商已经验收。

## 绑定版本与审查范围

- 工作树 `/root/.codex/worktrees/marketing-v2/silvermoon-ai`，分支 `codex/marketing-v2`，HEAD `46940e2a05fa664f00bff55758995d1fec3d0918` 加本轮未提交实现。
- 清单 `build/dev/mv2-005-review-manifest-r2.json`，SHA-256 `5a13c368756c6896ab9429b526452e5d23ce9687f332a760a5657d21b7cfccf9`。开始、结束核对 **31 个文件及 43 个产物/日志全部匹配**。
- 15 个 Issue 冻结验收正文及 README 共同约束逐字与 HEAD 比较，16 项没有变化。没有改验收标准，也没有重写旧 acceptance 哈希文件。
- 重新核对全部 A01–A06，覆盖资金来源、累计价格、协议关联、关闭/排空、恢复、多实例、正式基线升级；不仅复查 r1 的两个失败点。对 r1 已审文件用哈希核对其是否变化，对变化文件重读实现及新测试；本轮完整重读 billing 规则。
- 审查者只新增本报告及忽略目录中的独立日志/核对结果；没有修改实现、原测试、状态，没有提交、推送或部署。

最终核对记录：`build/dev/mv2-005-independent-r2-final-verification.json`。

## 全项结果

| 验收项 | 结果 | 业务与实现证据 |
| --- | --- | --- |
| A01 协议与生命周期 | **通过** | 保留正式 custom/main 的 OpenAI-compatible Realtime 支持范围；公开官方资料证明 metadata 回显、输入 item/输出 response 区别、默认 VAD 及取消/失败仍可带 usage。持续 append/commit 不是逐片建轮。专项覆盖连续输入、迟到 ACK、新输入 B、默认与部分配置更新、主动 cancel 带实际费用、重复终态、关闭及已读帧排空。 |
| A02 每轮资格 | **通过** | 手动意图只凭服务端真实 metadata 回显获得原资格；created 可无 metadata，done-only 关联保持已观察响应为 canonical，原 ResponseOrder 不改变。自动/闲置回复以首次收到 created 时间判断，不借连接时间或旧音频资格。16 项 metadata 容量、原字段保留、安全串行复用和旧 response ID 优先回归仍通过。 |
| A03 累计费用及冲正 | **通过** | 真实 supplier usage 先持久，再计算冻结价格；累计真实 tokens 不因降价减少。额度 10 赠金＋总费 15 的回归保持付费承担 5；跨轮累计 19→12 按实际最近消费原来源返还，不能将赠金变成付费。service 阶梯价格 2350→240 时真实总 tokens=240。先前终态冗余占用释放、过期赠金冲正和未结旧轮不得使用新赠金的回归保留。 |
| A04 止损与并发 | **通过** | 新轮在预估为零时也重新检查真正可用的令牌和当前资格资金；没有把预估 0 变成最低扣费。冻结 FreeModel 才保留免费模型零钱包语义。已观察的不足自动轮先保存身份/顺序与事实，再返回 stop；真实传输关闭上游后排空已读文本/音频/终态。真实双网关验证赠金不超用、余额不为负、actual>settled 的债务保留，以及全新有效 SK 也不能绕过同用户 review。 |
| A05 夹具与实际双进程 | **通过** | 最终两实际网关共享 PostgreSQL/Redis DB14，外部供应商才被模拟。旧跨到期、VAD、闲置、手动/自动交错、并发、断连及恢复场景保留。新增五场景覆盖 created 阶段拒绝、文本/音频已读输出、矛盾零/可信非零终态和无 metadata 迟到恢复；读到 ping 后回 pong 的证据确保终态已进入真实读协程，再解除行锁触发关闭/排空。第三验证进程调用真实内部恢复入口，明确不是上游重新执行。 |
| A06 未知费用与旧轮恢复 | **通过（本 Issue 的资金/协议范围）** | 缺最终 usage、非空输出与零 usage 矛盾、音频输出与纯文本回执矛盾均不能伪装零费用。原响应身份随 Begin 同事务持久，开始事实后续处理前退出也可被另一连接找到；恢复仍用原身份/资格/冻结价格和原来源。真实回归 actual=125、settled=120 时保持 review，重复恢复不增加消费、轮数或重赋资格，其他实例新 SK 受阻。未知/未结状态保持为后续清退门禁所需的权威状态；清退命令及管理员授权属于 MV2-011，未在本报告中提前标为已实现。 |

## r1 阻断的关闭依据

**F1 已修复。** 供应商自动 response ID 的摘要和实际响应顺序由 `BeginMarketingRealtimeRound` 与轮次创建同事务保存；准入失败也保存。不会再出现先创建 observed 轮、进程退出后没有 ResponseKey/ResponseOrder 的窗口。后续 start fact 不重新分配顺序，重复真实身份不新建另一轮。独立保留的 r1 原 overlay 在当前代码重跑，原四个失败断言全部通过，日志 `build/dev/mv2-005-independent-rejected-start-r2.log`。

**F2 已修复。** observed Stop 不再早于本地输出跟踪初始化返回；关闭排空时非空文本/音频先保存 OutputObserved/AudioOutputObserved，再返回停止信号。矛盾零终态保持 unknown/review；可信非零终态保存真实未付费用。独立 r1 原 overlay 重跑，原三个失败断言全部通过，日志 `build/dev/mv2-005-independent-rejected-output-r2.log`。没有改这些探针断言来适应实现。

**真实网关追加发现的零预估漏洞已修复并复核。** 正常 WS 连接可预估为 0；这不能证明后续一轮免费。当前模型额外检查有限令牌剩余、其他连接占用后的付费/合资格赠金。只有实际已发送 commit 的手动意图可以把本连接仍符合当前资格的输入占用作为准入支持；自动回复不能借用未关联输入，也不能借别的连接/轮次。检查本身不扣款；合法零费用终态仍为零。FreeModel 取自服务器冻结价格快照，已对照 `relay/helper/price.go`：tiered 情况不是“估算恰好为零”就设 FreeModel。空钱包、空 token、他处占用、过期/未来赠金、永久赠金、付费可用、免费及无限令牌分支已在三库矩阵覆盖。

## 本轮独立实际执行

```sh
.dev/run go test ./service ./model ./relay ./relay/channel/openai -run 'TestMarketingRealtime|TestMarketingBillingSessionLifecycle/sqlite/realtime|TestMarketingPersistence/sqlite/realtime|TestManagedRealtime|TestResponsesWS' -count=1 -timeout=240s -v
# 退出 0，四包通过，无 FAIL/SKIP。
# build/dev/mv2-005-independent-r2.log

.dev/run go test -overlay=/workspace/build/dev/mv2-005-independent-rejected-start-overlay.json ./service -run '^TestMarketingBillingSessionLifecycle/sqlite/realtime_two_rounds$' -count=1 -timeout=120s -v
# 退出 0，原独立 F1 自然消费反例通过。
# build/dev/mv2-005-independent-rejected-start-r2.log

.dev/run go test -overlay=/workspace/build/dev/mv2-005-independent-rejected-output-overlay.json ./service -run '^TestMarketingBillingSessionLifecycle/sqlite/realtime_two_rounds$' -count=1 -timeout=120s -v
# 退出 0，原独立 F2 排空输出反例通过。
# build/dev/mv2-005-independent-rejected-output-r2.log

git diff --check
# 退出 0。
```

以上资金测试使用真实 SQLite 与生产 model/service，没有模拟资金仓库。传输单元测试中的 coordinator 替身仍仅是传输证据，不能替代下面的真实资金矩阵。独立日志 SHA-256 已记录在最终核对 JSON。

## 三库、实际网关及构建证据复核

下列证据由实现代理运行，本轮审查者核对其完整结果、脚本/夹具与绑定版本，**没有声称本轮重新运行三库或重建网关**：

| 命令/范围 | 证据与结果 |
| --- | --- |
| `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` | `mv2-005-zero-estimate-accounting-matrix-r3.log`，model/service 均真实 SQLite/MySQL/PostgreSQL，无失败或跳过；独立保存 model/service 明细。包含零预估、原子身份、自然耗尽、文本/音频矛盾终态及既有全部资金生命周期回归。 |
| `python3 .dev/marketing-v2-requirements/validation/gateway_process.py all` | `mv2-005-rejected-start-gateway-r2.log` / `...-r2-detail.log`。两个实际网关、共享 PG/Redis；新五场景及旧全部场景通过。核对了资金/令牌值、物理 socket 关闭、响应原身份/顺序/资格、全新 SK 跨实例阻断及第三进程重复恢复的实际断言。 |
| `.dev/run go test ./model ./service ./relay ./relay/channel/openai -count=1 -timeout=300s` | `mv2-005-r3-regressions.log` 中 service/relay/openai 通过；model 当次测试种子缺 fmt 编译失败，不能把该日志整体称为通过。之后仅修测试使用已有 strconv，`.dev/run go test ./model -count=1 -timeout=300s` 在 `mv2-005-r3-model-regressions.log` 通过。生产未再变。 |
| `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` | 沿用 `mv2-005-protocol-migration.log` 及绑定的六份 fresh/upgrade 验证；r2 没有改持久字段、表、索引或迁移定义。真实新库、custom/main 代表数据升级及重复启动验证仍对应本轮 schema。 |
| controller WS、transport race | `mv2-005-final-controller-ws.log`、`mv2-005-final-transport-race.log` 对应文件未变；本轮又重跑相关 relay/transport 专项。没有把 fake coordinator 的通过用作资金结论。 |

实际版本：SQLite **3.50.4**、MySQL **8.4.13**、PostgreSQL **17.11**、Redis **7.2.17 / DB14**。没有宣称全部最低版本均已运行。迁移基线为正式 `origin/custom/main` 的 `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`，不要求旧营销数据迁移；旧营销从未上线。

r1 失败、零预估网关失败、用户名长度与编译失败记录均保留，development 按时间注明真实原因和后续结果；没有删失败断言、覆盖失败日志或把未运行/跳过当作通过。

## 共同业务约束及交付边界

- 资金更新由数据库事务、固定锁顺序、持久轮次/来源分配/真实 debit/唯一事件承担，Redis 不替代资金一致性。赠金与付费仍独立，来源可解释，旧赠金返还不能变付费或恢复新轮过期资格。
- Realtime 用户消费保持一个逻辑连接记录；内部轮次/来源拆分不制造多条用户消费记录。来源前端正式展示属于 MV2-014，不在这里提前声称完成。
- 供应商真实用量与估算分开，价格失败仍保留事实；多扣返还原来源，未结不自动免账。止损只能在协议可执行边界进行，不承诺网络/供应商停止延迟绝对零费用。
- 没有新中间件、公开恢复接口、Cookie 写入或新认证流程。现有 SK 权限及数据库发送前归属/状态检查保持；迟到真实费用可以结算，不代表撤权后可新发调用。未来管理员恢复/清退的权限、审计、CSRF（若走浏览器）必须在 MV2-011 入口开发时验收，不能只因内部函数存在就视为已覆盖。
- 不引入旧营销的待激活、人工审核发奖、赠金模型/分组限制或免费兑换码。没有将“系统未上线”错误扩展为删除 custom/main 现有基础记录的升级保护。
- 官方协议依据及本地原始资料仍见 `MV2-005-protocol.md`；本轮未增加供应商白名单，未要求用户指定供应商或提供文档。官方协议+受控供应商夹具验证的是网关契约实现，不泛化为每家第三方服务都遵循规范。没有可靠归属或真实终态时保持待核对，不伪造免费，也不新增独立转写计费产品。

本轮没有剩余需修复的 MV2-005 阻断。主代理可以按约定更新完成状态、保留 r1/r2 报告与证据并 commit，然后进入下一个 Issue；后续 Issue 仍按完整独立验收流程执行。
