# MV2-005 整项独立验收 r1

日期：2026-11-23。审查者：`mv2004_path_audit`（独立 sub agent）。结论：**不通过，保持未完成，不能提交为已验收 Issue 或进入下一 Issue。** 已检查 A01–A06 及共同约束；发现两项有确定反例的阻断，不能用原有成功矩阵覆盖。

## 代码与证据绑定

- 工作树：`/root/.codex/worktrees/marketing-v2/silvermoon-ai`；分支 `codex/marketing-v2`。
- HEAD：`46940e2a05fa664f00bff55758995d1fec3d0918`。
- 审查清单：`build/dev/mv2-005-review-manifest-r1.json`；SHA-256 `dd1341d5e609573671180da23e57ee54ce1c4941fa3c279178793a09aa6ae0d1`。
- 开始及结束核对：清单内 30 个文件、18 个产物/日志哈希全部匹配；HEAD 未变。最终独立核对记录 `build/dev/mv2-005-independent-final-verification.json`。
- 15 个 Issue 的冻结验收正文及 README 共同约束逐字与 HEAD 比较，16 项全部未变。没有重写 `acceptance.sha256.json`，也不声称还原其旧规范化算法。
- 本轮仅写本报告及忽略目录下独立 overlay/日志；没有修改生产代码、原测试、冻结标准、Issue 状态，没有 commit/push/deploy。

## 逐项结果

| 验收项 | 结果 | 核对结论 |
| --- | --- | --- |
| A01 | 通过 | 对照正式 custom/main 协议入口、公开官方 Realtime 类型/指南、持续输入/commit/VAD/手动响应/取消/重复终态/关闭夹具。音频 append 不是轮次；真实 usage 与准入估算分开。正常取消仍按实际 usage 结算。传输关闭与排空已有覆盖，但发现的失败分支另列 F2，并阻断整项通过。 |
| A02 | 通过 | 手动响应只凭真实 metadata 回显确认原资格；created 不含 metadata 时独立观测，done 回显后保留 observed canonical 身份及 ResponseOrder，再继承已证实手动时间。自动/闲置响应按首次收到 created 的时间，不继承连接或音频开始时间。满 16 项 metadata 有原字段保留、活动中歧义拒绝、可信终态后串行复用及旧 response ID 优先回归。 |
| A03 | 通过 | 真实用量先独立提交，再按冻结价格计算累计费用；输入/输出/音频/cache 分类及累计舍入保留。降价按最新真实 debit 来源逆序冲正，不减少真实 tokens，不把赠金变成付费。模型用例 10 赠金＋15 总费验证付费承担 5，跨轮累计 19 降至 12 验证原付费冲正；service 价格用例 2350 降至 240 而真实总 tokens=240。 |
| A04 | **不通过** | 既有双实际网关验证正常耗尽时关闭真实上游、排空已读终态、保留 actual=1200/settled=600、另一实例不能新发请求，确有价值。但自动 created 的初次准入即失败时未持久响应身份，且 drain 输出被丢弃；F1/F2 证明未覆盖的边界破坏实际费用留存。 |
| A05 | **不通过** | 已确认最终日志来自两个实际网关进程共享 PostgreSQL/Redis，供应商才是模拟；覆盖跨到期、默认 VAD、闲置自动回复、手动自动交错、并发、断连与第三进程可信恢复。独立反例说明当前夹具未覆盖“在 created 阶段止损后的排空/恢复”。修复后须补该边界，再全项复验，不能把现有矩阵成功视为 A05 完成。 |
| A06 | **不通过** | 常规未知费用断连保持 review/占用，可信迟到终态可按原轮/原来源恢复，重复幂等且错用户/陌生响应被拒。F1 中已有真实自动响应却因缺持久身份被当陌生响应；F2 可将观察到生成的未知轮错误结清为零。由此未结清退门禁所依赖的资金状态也不可信。清退实际命令/授权入口属于 MV2-011，此报告没有宣称它已经实现或验收。 |

## F1（P1）：自动响应在初次准入不足时丢失持久响应身份，可信迟到终态无法恢复

位置：`service/marketing_realtime.go:202`、`:481`、`:1021`；`model/marketing_v2_realtime.go:1085`；`service/marketing_realtime_usage.go:179`。

`begin` 已提交一个 `Observed=true` 的轮次，但 `result.Stop` 时立即返回轮 ID 和不足错误。`serverEvent` 的 `response.created` 分支只把 ID 放进进程内 `responses`，遇到错误就返回，没有执行后面的 `fact(start/...)`。数据库轮次因此 `ResponseKey=""`、`ResponseOrder=0`。连接关闭后，内部恢复入口按真实 response ID 找不到原轮，没有自动响应 metadata 可替代，返回 `ErrMarketingConflict`；真实迟到 usage 不保存。

独立自然业务复现：初始令牌预算 100，第一轮真实 usage=100 正常结算，用尽令牌预算；随后收到供应商自动 `response.created(id=automatic-unfunded)`，准入正确要求停止。关闭后输入同一响应的可信 `response.done`（usage=5）。预期应保存其原身份/原时间/真实费用，资金不足仍保留待核对；实际 ResponseKey 空、ResponseOrder 零，恢复失败且 UsageJSON 仍空。无需伪造已消费资金、绕过鉴权或直接篡改资金余额。

证据：`build/dev/mv2-005-independent-rejected-start-natural.log` 四个失败断言。最初使用“连接期间令牌上限降低”夹具的同一反例也保留在 `mv2-005-independent-rejected-start.log`；自然消费版本排除了对直接更新余额的依赖。

修复需要在要求停止前持久保存已观察的供应商身份及响应顺序。禁止以恢复时新建轮次、沿用连接时间、跳过资金审查或直接免账来绕过。止损要求与保存事实必须同时成立。

## F2（P1）：同一失败起点导致已读输出丢失，矛盾零终态被当作结清

位置：`service/marketing_realtime.go:202`（早于 estimates 初始化）、`:530` 附近 output delta 分支；真实传输排空入口 `relay/channel/openai/relay_realtime_managed.go`。

父代理在 F1 通知后提出需检查该连带路径；本审查者另建独立 overlay 实际验证，没有直接采纳推测。上述不足自动轮从未进入 `estimates`；随后在关闭前已经读取、排空中的非空 `response.output_text.delta` 命中 `!active` 而直接返回，连 `OutputObserved` 都未持久化。再到达显式零 usage 的 done 时，没有输出矛盾证据，最终轮 `Unknown=false`，整个资金会话 `State=settled`。

独立复现同样先通过真实第一轮消费用尽令牌，再送不足自动 created、非空 output delta、零 usage done、Close。实际三个失败断言为：OutputObserved=false、Unknown=false、State=settled（预期 review）。这不是要求用估算结算，而是要求已观察的非空生成不能被零回执抹去。

证据：`build/dev/mv2-005-independent-rejected-output.log`。修复须分别保护文本/音频输出事实，以及已读可信非零终态；只补 ResponseKey 不能消除本问题。不能将所有停止后的已读帧直接丢弃。

## 本轮独立执行

以下均使用项目 `.dev/run` 和实际 model/service 资金实现，SQLite 为真实数据库。overlay 只替换独立测试副本，生产与清单文件不变：

```sh
.dev/run go test ./service ./model ./relay/channel/openai -run 'TestMarketingRealtime|TestMarketingBillingSessionLifecycle/sqlite/realtime|TestMarketingPersistence/sqlite/realtime|TestManagedRealtime' -count=1 -timeout=240s -v
# build/dev/mv2-005-independent-r1.log：退出 0，三包通过，无 FAIL/SKIP。

.dev/run go test -overlay=/workspace/build/dev/mv2-005-independent-rejected-start-overlay.json ./service -run '^TestMarketingBillingSessionLifecycle/sqlite/realtime_two_rounds$' -count=1 -timeout=120s -v
# build/dev/mv2-005-independent-rejected-start-natural.log：退出 1，F1 四个断言失败。

.dev/run go test -overlay=/workspace/build/dev/mv2-005-independent-rejected-output-overlay.json ./service -run '^TestMarketingBillingSessionLifecycle/sqlite/realtime_two_rounds$' -count=1 -timeout=120s -v
# build/dev/mv2-005-independent-rejected-output.log：退出 1，F2 三个断言失败。

git diff --check
# 退出 0。
```

前两个 overlay 对应的独立测试副本在 `build/dev/mv2-005-independent-rejected-{start,output}_test.go`。它们扩展现有完整资金夹具，固定输入/确切结果，没有随机压力或仅证明代码运行的断言。初始 rejected-start 日志对应的是该副本的初版；复跑以 natural 日志为准。

## 已核对但未在本轮重跑的证据

核对了脚本、真实网关夹具资金断言、日志及清单绑定产物，不将其表述成本审查者重新运行：

- `.dev/run go test ./model ./service ./relay ./relay/channel/openai -count=1 -timeout=300s`：`mv2-005-final-regressions.log` 四包通过。
- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：`mv2-005-final-accounting-r2.log` model/service 真实三库通过，无 FAIL/SKIP；追加主动 cancel 之后最新 service 三库 `mv2-005-final-service-matrix.log` 通过。
- `python3 .dev/marketing-v2-requirements/validation/gateway_process.py all`：`mv2-005-final-gateway-r3.log`。两实际网关端口 18371/18372，共享 PostgreSQL 与 Redis DB14；第三验证进程通过真实内部 service 恢复原轮。夹具明确断言赠金/付费/token 余额、原资格、response 顺序、上游物理关闭、另一实例阻断及重复不扣款。
- `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`：`mv2-005-protocol-migration.log` 与清单内六份 fresh/upgrade verify 日志；正式 custom/main `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12` 代表数据升级、重复启动、旧数据/唯一约束核对。没有迁移旧营销或预览数据。日志内预期唯一约束冲突用于验证约束，不是未处理测试失败。
- controller Responses WS 专项与传输 race：`mv2-005-final-controller-ws.log`、`mv2-005-final-transport-race.log` 通过。传输专项使用真实 socket 但协调器替身，仅证明关闭/串行/排空；不拿它替代真实资金验证。

数据库版本：SQLite **3.50.4**、MySQL **8.4.13**、PostgreSQL **17.11**；Redis **7.2.17 / DB14**。不声称已验证全部最低数据库版本。此轮未改独立日志库 schema，原 MV2-004 日志库验证不自动扩充为新协议已验收。

## 共同约束与边界

- 新版赠金/付费独立，按到期赠金→永久赠金→付费的资金模型检查；本轮没有添加旧版待激活、审批、模型/分组限制、赠送码或主题奖品。旧营销未上线，不能新增其迁移需求。
- 用户/令牌行锁与持久轮次、debit、事件唯一键是权威；共享 Redis 不是资金锁替代品。两个实际进程的验证与同进程测试明确区分。
- 未新增浏览器 Cookie 写入或公开恢复 HTTP 路由；原 SK 路由鉴权/归属和发送前 account-wide review 复查保留。内部可信恢复入口的未来维护授权属于 MV2-011，不能因为内部函数可调用就声称已完成受控管理员界面。
- 协议依据已独立核对 Microsoft 官方 Azure Realtime 指南/reference、OpenAI 官方 Python SDK DTO；本地原始文件/URL/hash 见 `MV2-005-protocol.md`。确认 done metadata 回显、最多 16 项、输入 item 不等于 response ID、默认 server_vad 可省略 create_response/idle_timeout_ms。未将官方可选字段推导为所有第三方 created 必带 metadata。
- 仍不承诺全部第三方上游均符合规范；不要求用户指定供应商或提供私有协议资料，也不新增独立转写计费产品。未知事实保守保留不能当免费，正常已知完成输入也不应无故永远占用。
- Realtime 现保持一个逻辑连接消费日志，底层分轮/分来源；前端来源展示和清退操作不属于本 Issue 的已交付页面。

下一步：修复 F1/F2，将自然耗尽后的自动起点、已读文本/音频输出、可信非零/矛盾零终态、断连后旧身份恢复纳入原有集中回归；更新证据清单，然后再次独立检查整个 A01–A06，不能只验修复的两条。此次报告不批准标记完成。
