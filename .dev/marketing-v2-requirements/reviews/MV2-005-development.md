# MV2-005 开发记录（2026-11-23，未完成）

基于已验收提交 `46940e2`。本记录不是整项验收报告，不能据此提交为 Issue 完成或进入下一 Issue。冻结验收正文没有修改。

## 基线范围

用户再次明确旧营销体系未上线，正式线上代码以 `custom/main` 为准。远程只读核对仍为 `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`。只验证该正式基线升级；不迁移旧营销表、历史奖励或预览资产。隔离测试数据库重建不等于生产数据迁移。

## 当前实现

- Responses WS 每个 create 在实际业务发送前持久记账；重新验证账号、当前赠金资格；同连接更早 response ID 不串入新请求。generate:false 只有明确空成功终态且无生成/矛盾用量证据才零结算。
- Realtime 一连接一资金会话、分轮资格和持久用量事实。赠金不足转扣付费资金，累计降价按真实来源逆序冲正；未结旧轮不使用新轮资格。
- 可信供应商用量先独立事务提交，再计算价格与投影日志。价格失败保留真实用量。
- 非空输出记录 OutputObserved；随后显式零输出用量视为矛盾证据，不按零费用释放。
- 持续输入保留临时占用；手动 commit/clear 的发送边界和确认分别记录在连接协调器。下一段未完成输入不归到上一段的回复。
- 先关闭真实上游，再排空已读取的服务端消息、等待读取协程退出，最后关闭账务会话；未知不按估算收费，也不自动清退。
- 用户消费仍只有一个逻辑连接日志，内部保存分轮/赠金/付费来源。

## 必须继续完成的内容

协议关联已完成新一轮实现及专项验证：手动请求使用真实回显关联，自动回复不抢 pending；只有 done 回显时保留 observed 响应为唯一计费行，继承已证实的手动资格并保留原响应顺序。VAD 的输入 item 与响应身份分开；正常默认 server_vad、明确关闭/开启及部分服务端配置更新分别处理，未归属输入不能被其他回复清除。

满16项用户metadata的安全串行复用与最终三库资金/真实双网关复验现已完成，结果见下方最终验证记录。当前仍需整项 A01–A06 独立验收；在独立验收前保持Issue未完成。

用户不限定供应商；协议核对不以用户选定供应商或提供资料为前提。此前 OpenAI 文档地址 403/超时的历史事实保留，后续已取得 Microsoft 官方 Realtime 文档与 OpenAI 官方 SDK 类型，见 `MV2-005-protocol.md`。没有供应商白名单，也不将模拟上游视为所有第三方均遵循协议的证明。

## 已运行的验证

- `.dev/run go test ./controller ./relay -run 'TestResponsesWS|TestResponsesWebSocket|TestNormalizeResponsesWS|TestBuildResponsesWS|TestRestoreConnectionContext|TestCheckResponsesWS|TestSelectResponsesWS' -count=1`：通过；`build/dev/mv2005-responses-ws-r5.log`。
- `.dev/run go test ./model -run '^TestMarketingPersistence/sqlite/(funding|realtime)$' -count=1 -timeout=180s -v`：通过；`build/dev/mv2-005-model-output-evidence.log`。
- `.dev/run go test ./service -run '^TestMarketingBillingSessionLifecycle/sqlite/realtime|^TestMarketingRealtimeUsageEvidenceAndCumulativePricing$' -count=1 -timeout=90s`：通过；`build/dev/mv2-005-service-r6.log`。
- `.dev/run go test ./service ./relay/channel/openai -run '^TestMarketingRealtime|^TestManagedRealtime' -count=1 -timeout=90s`：通过；`build/dev/mv2-005-final-focused.log`。
- `CGO_ENABLED=1 .dev/run go test -race ./relay/channel/openai -run '^TestManagedRealtime' -count=1`：通过。该传输测试使用真实双向 socket，但协调器是替身，只证明关闭/串行/排空，不作为资金验收。
- `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`：通过；`build/dev/mv2-005-migration.log`。真实 SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11，新库、custom/main 合成基线升级、重复启动、原数据和约束验证。
- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：通过；`build/dev/mv2-005-accounting.log`。三个真实数据库的 model/service 回归包括本轮新增用量、占用、冲正、未知、矛盾零用量与迟到结果。
- `python3 .dev/marketing-v2-requirements/validation/gateway_process.py all`：第三轮通过；`build/dev/mv2-005-gateway-r3.log`。两个实际网关进程共用 PostgreSQL 和 Redis 7.2.17 的逻辑库 14，外部供应商为受控 WS/HTTP。通过同账号并发分配、手动音频 commit 后轮次跨到期、断线未知阻断另一实例，以及既有普通请求/音频/异步崩溃恢复回归。没有验证自动 VAD。

首次 service 测试发现日志构造在事务里申请第二个数据库连接导致单连接夹具阻塞；已将日志元数据构造移到事务外。首次 gateway 测试发现按量价格的未使用按次价格 -1 被错误校验，已只验证当前计价分支实际使用字段并增加回归。第二次 gateway 的未知费用夹具没有生成任何预估占用，却要求占用大于零；现加入真实输出 delta 再断线，保持原资金保护断言。失败日志保留，没有删除或伪装为通过。

## 早期复核后的追加修复

独立代理随后发现音频输出被报告为纯文本、以及手动 commit/create 连续发送两个反例。已补 `AudioOutputObserved` 持久证据和 `AttachMarketingRealtimeInput`，模型集中回归扩展为16组 Realtime；服务增加 `realtime_audio_mismatch` 和 `realtime_pipelined_commit`。`build/dev/mv2-005-model-audio-attach.log`、`build/dev/mv2-005-service-r7.log` 通过。独立只读复核见 `MV2-005-early-review.md`；仍不是整项验收。

新增字段后的三库迁移与资金矩阵已重跑通过，最终对应 `build/dev/mv2-005-final-migration_matrix.log`、`build/dev/mv2-005-final-accounting_matrix.log`。验收文本校验：15个Issue及共同约束的SHA-256全部保持原值。

最后的真实双网关复跑也已通过：`build/dev/mv2-005-final-gateway_process.log`。最终三个串行验证脚本均返回0；协议关联缺口仍未解除，以上通过不改变Issue未完成状态。本轮未提交未验收实现，最近已验收提交仍为 `46940e2`。

## 供应商无关协议修复及后续回归（2026-11-23）

- 增加持久 ResponseOrder 与 SupersededBy：发送意图与实际响应分开，done-only关联不重排已计价序列；原用户metadata保留，内部标记不回显客户端。nonce/完整metadata指纹只保存必要安全证据。
- 新增内部 `ReconcileMarketingRealtimeResponse`：只接受关闭连接的可信原始终态，从主库加载原资金与日志快照，用原价格、原轮次和原来源核对；没有新增公开HTTP接口，不重新请求上游。错归属和未知response ID被拒绝，重复终态幂等。
- 独立复核确认官方默认session示例省略create_response和idle_timeout_ms，不能将缺字段一概当作不支持自动VAD。现按已证实默认值建立配置基线，server部分更新保留已确认设置，GA输入配置缺少format时不误重置格式。未确认转写状态仍保守处理。
- 追加反例发现关闭连接后补齐A费用可能错误清除B未结状态，已修为B仍pending/review、阻止新业务；A的可信费用仍可正常结算。before/after日志保留在 `build/dev/mv2-005-pending-recovery-*.log`。
- 追加反例发现每轮预占30实际5后，余25一直占用，实际剩95却无法进入需要80的新轮。现只释放可信完成且实际结清的当前轮冗余预占，其他轮/输入及既有原来源冲正行为仍受回归保护。原失败证据 `build/dev/mv2-005-unused-admission-before.log` 保留。

本轮实际执行结果：

| 验证 | 结果与证据 |
| --- | --- |
| metadata/恢复/传输专项r1 | model新增断言错用不存在字段导致编译失败；service及transport通过。修正为读取真实用户/赠金来源，失败日志 `mv2-005-protocol-focused-r1.log` 保留。 |
| metadata/模型/传输专项r2 | `mv2-005-protocol-focused-r2.log`通过，包含done-only、auto交错、原字段保留、可信迟到恢复和既有模型资金回归。 |
| 三库迁移 | `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`退出0；`mv2-005-protocol-migration.log`及验证目录子日志。两新增字段已涵盖；其后没有再改schema。 |
| 三库资金r1 | model通过；service两个新VAD用例错误把“总占用”当“输入占用”而失败。证据 `mv2-005-protocol-accounting.log`及单独保存的model/service-r1日志，不能作为最终资金矩阵通过。随后补了逐输入来源断言并修复上述真实冗余预占问题，待最终三库复跑。 |
| 真实双网关r1 | 正常VAD/新输入B/到期/自动回复通过；交错用例错误以已被别名替代的manual占位ID查询消费而失败。保留 `mv2-005-protocol-gateway-r1-detail.log`。夹具现跟随持久别名核对canonical身份、原资格、响应顺序与真实来源，没有删除金额断言。 |
| 真实双网关r2 | `python3 .dev/marketing-v2-requirements/validation/gateway_process.py all`退出0，`mv2-005-protocol-gateway-r2.log`。覆盖真实资金不足关闭、排空已读取终态、欠费、另一实例阻断、可信核对及重复终态。恢复由第三个验证进程调用真实内部service入口，明确不是网关自动找回供应商账单。此二进制早于最新冗余占用及默认VAD修复，仍待最终复跑。 |
| 默认VAD与冗余占用专项 | `mv2-005-default-vad-holds.log`通过，实际运行model新长连接回归及service默认配置、部分更新、GA默认、保持显式禁用等子项，没有把零匹配当通过。 |

冻结标准核对采用与已验收HEAD逐字比较验收正文和共同约束：15项均未修改，证据 `build/dev/mv2-005-criteria-vs-head.json`。没有重写原acceptance.sha256.json，也不声称已还原其历史规范化算法。

## 本轮最终验证（等待整项独立验收）

实现、测试及三库/双网关验证已完成；没有提交或标记Issue通过。

- 满16项metadata允许已确认终态后的安全串行复用，每次独立请求引用，历史response ID优先归属。新增完整回归覆盖活动中复用拒绝、终态后复用、新pending期间迟到旧done、第二轮done-only关联及断连后恢复。
- 用户主动response.cancel且最终仍有真实usage的场景已补入原service三库生命周期测试：照实际费用结算，不因cancelled状态退零；随后新轮可继续。
- 真实双网关现在从官方默认session字段形状开始，省略create_response/idle_timeout_ms；后续只确认阈值更新。正常VAD测试不再依靠人为补齐true/null。

| 最终命令/范围 | 结果 |
| --- | --- |
| `.dev/run go test ./model ./service ./relay ./relay/channel/openai -count=1 -timeout=300s` | 四包全部通过，`build/dev/mv2-005-final-regressions.log`。 |
| `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` | model/service真实三库全部通过且无跳过，`build/dev/mv2-005-final-accounting-r2.log`。 |
| 编译最新service测试后调用accounting_matrix的`run('service')` | 新增主动打断用例后，service全部真实三库再次通过且无跳过；`build/dev/mv2-005-final-service-matrix.log`，sqlite/mysql/postgres各自明确含realtime_cancelled_usage通过。未因此重复未变化的model矩阵。 |
| `python3 .dev/marketing-v2-requirements/validation/gateway_process.py all` | 最终代码及默认协议夹具的两个实际网关全部通过，`build/dev/mv2-005-final-gateway-r3.log`。共享PostgreSQL与Redis DB14；仅供应商模拟。 |
| `.dev/run go test ./controller -run 'TestResponsesWS\|TestResponsesWebSocket' -count=1 -timeout=180s` | 最新代码下通过，`build/dev/mv2-005-final-controller-ws.log`。 |
| `CGO_ENABLED=1 .dev/run go test -race ./relay/channel/openai -run '^TestManagedRealtime' -count=1 -timeout=120s` | 传输竞争检查通过，`build/dev/mv2-005-final-transport-race.log`。传输测试的协调器替身不作为资金证据，资金由前述真实矩阵验证。 |

最终schema仍为先前protocol-migration通过的版本，无后续schema变化；继续沿用真实SQLite 3.50.4 / MySQL 8.4.13 / PostgreSQL 17.11的新库、正式custom/main升级及重复启动证据。Redis版本7.2.17。没有声称验证所有最低版本，也没有迁移旧营销数据。

最终证据将绑定 `build/dev/mv2-005-review-manifest-r1.json`；整项审查必须逐条核对A01–A06与共同业务规则。现有日志/旧失败证据保留，不以部分审查或构建成功取代全项验收。

## 整项独立验收 r1：不通过，继续修复

独立报告 `MV2-005-acceptance-r1.md` 确认 A01–A03 通过，A04–A06 不通过。自然消耗完令牌预算后，供应商已经产生的自动响应在开始阶段被要求停止，却没有持久保存响应身份和顺序，迟到可信终态无法关联；同一提前返回还跳过输出跟踪，导致排空中的非空输出被漏记，矛盾零终态可能错误结清。

独立失败证据 `build/dev/mv2-005-independent-rejected-start-natural.log`、`build/dev/mv2-005-independent-rejected-output.log` 保留。先修复真实观测事实保存和停止逻辑，补自然耗尽及真实网关排空/恢复回归，再重新提交 A01–A06 整项验收。此时仍未完成、未提交；上述成功矩阵不能覆盖新增反例。用户再次明确不限定供应商，不要求用户提供供应商名单或资料，公开协议与保守处理未知事实的边界不变。

## r1 阻断修复与 r2 验证

- 已观察的自动响应在准入停止时仍初始化输出跟踪，随后保存开始事实，再返回停止信号；已读文本/音频证据继续进入原轮次，不能被矛盾零终态清除。
- 响应身份与实际响应顺序直接随 `BeginMarketingRealtimeRound` 在同一事务提交，消除创建轮次后、保存开始事实前进程退出留下无法关联轮次的窗口。未观察到响应的手动意图不分配实际响应顺序；重复响应不新增轮次或顺序。
- 无 metadata 的自动响应可在另一实例从持久身份恢复。可信迟到费用正常保留，例如实际 105、已付 100 时继续待核对，不重新赋予赠金资格，不将不足额消费当作结清。
- 集中回归新增自然消费用尽令牌后的开始拒绝、文本/音频与零终态矛盾，以及开始事务后立即由另一连接查找原响应的场景。没有修改任何冻结验收项或新建表字段。

已执行 `.dev/run` 专项与 `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`，全部通过。证据为 `build/dev/mv2-005-rejected-start-atomic-focused.log`、`build/dev/mv2-005-rejected-start-accounting-matrix.log`，以及独立保留的 `...-model-matrix.log` / `...-service-matrix.log`。model/service 真实 SQLite、MySQL、PostgreSQL 均无失败或跳过；版本同上。三库迁移证据继续适用：本次只改事务逻辑和函数输入，没有改变持久 schema。

随后四包回归通过（`build/dev/mv2-005-r2-regressions.log`），但真实双网关新增场景首轮失败，不能提交整项复审为通过：无初始输入的实际连接预估为 0，自然消费完令牌额度后，下一自动轮仍按零预估判足，未物理关闭上游。失败日志 `build/dev/mv2-005-rejected-start-gateway-r1.log`、`...-r1-detail.log`、`...-r1-state.log` 保留，显示令牌剩余 0、已用 120，第二轮身份已正确保存但连接仍 active。这证明仅修身份和排空仍不足以覆盖真实零预估准入。正在补充零预估下的新轮可用资金/令牌检查；不增加最低消费，不改变真实定价，不削弱物理关闭断言。

零预估准入现单独检查真实可用资金与令牌额度；仅冻结价格明确免费时保留免费模型的零钱包语义。有限令牌自身耗尽仍按既有鉴权规则停止。没有把预估 0 改成收费 1，合法真实零费用仍为零。

首次零预估专项发现合法 `commit/create` 流水的迟到确认兼容问题（`build/dev/mv2-005-zero-estimate-focused.log` 保留）：本连接已发送但未收到确认的音频占用不应被误当成无资金。修复后只允许已有发送 commit 证据的手动意图计算本连接、当前资格仍有效的输入占用；自动回复不能借用未关联输入，其他连接/回复的占用和到期赠金不计入。后续专项已通过，新的三库与真实网关复跑中，仍须整项独立验收。

### 供整项 r2 独立复验的最终证据

| 范围 | 实际结果 |
| --- | --- |
| 零预估专项 | `mv2-005-zero-estimate-focused-r2.log`：model/service 全部 Realtime 集中回归通过，覆盖真实零预估、输入占用、免费模型及已有协议关联。 |
| 三库最终矩阵 | `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` 退出 0；`mv2-005-zero-estimate-accounting-matrix-r3.log`，model/service 分别保存 `mv2-005-zero-estimate-model-matrix.log`、`mv2-005-zero-estimate-service-matrix.log`，真实三库均无失败/跳过。 |
| 双实际网关完整矩阵 | `python3 .dev/marketing-v2-requirements/validation/gateway_process.py all` 退出 0；`mv2-005-rejected-start-gateway-r2.log` / `...-r2-detail.log`。新增五场景覆盖自然令牌耗尽、开始阶段物理止损、文本/音频已读证据、可信非零/矛盾零终态、无 metadata 迟到恢复；同用户全新有效令牌访问另一实例也因待核对被阻断。旧 Realtime、普通请求和异步恢复矩阵保持通过。 |
| 包级回归 | `go test ./model ./service ./relay ./relay/channel/openai -count=1 -timeout=300s` 使用 `.dev/run` 执行，`mv2-005-r3-regressions.log` 中 service/relay/openai 通过，model 因下述测试种子编译问题失败；仅修正测试后补跑 `.dev/run go test ./model -count=1 -timeout=300s` 通过，`mv2-005-r3-model-regressions.log`。没有再次修改已通过的生产代码。 |

三库矩阵前两次失败也保留：`mv2-005-zero-estimate-accounting-matrix.log` 是新测试的描述性用户名超过 MySQL/PostgreSQL 已有 32 字符限制；改用短编号只修测试种子、不改断言。`...-r2.log` 是该修正漏掉 `fmt` 导入造成编译失败，最终改用已有 `strconv.Itoa` 后再完整跑 r3。没有覆盖这些失败日志。

上述新日志均位于 `build/dev/`。最终代码、测试、方案与验证产物绑定 `build/dev/mv2-005-review-manifest-r2.json`。状态仍未完成，等待独立代理重新检查全部 A01–A06 及共同约束，而非只复查 F1/F2。此阶段未 push、创建 PR 或部署。

2026-11-23 独立 r2 全项复验结束：A01–A06 及共同约束全部通过，无剩余阻断。详见 `MV2-005-acceptance-r2.md`；完成状态在此结论后更新，冻结验收正文没有修改。按约定保存本 Issue 的本地提交，再进入 MV2-006。
