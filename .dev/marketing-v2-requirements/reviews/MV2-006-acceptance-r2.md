# MV2-006 整项独立验收 r2

日期：2026-11-23。审查者：`mv2004_path_audit`（独立 sub agent）。结论：**A01–A07及本Issue适用共同约束全部通过，没有剩余阻断，可以更新MV2-006完成状态并提交。** 本结论只适用于绑定版本的奖池持久服务；不提前宣称活动事件、HTTP页面、退款入口、线上性能或整个营销系统已经完成。

## 绑定与复验范围

- 工作树 `/root/.codex/worktrees/marketing-v2/silvermoon-ai`，分支 `codex/marketing-v2`；HEAD `bd2ca864f4adb9c35e6233638fdb440540339a80` 加本轮未提交实现。
- 主清单 `build/dev/mv2-006-review-manifest-r2.json`，SHA-256 `3ce4c4399232c80c75f00ed9d9bd43bacd9b07823b03c2ae5922d65b1eca0d17`。开始、结束核对12个文件与26个产物全部一致，其中包含实际lottery-process及迁移probe二进制。
- 15个Issue冻结验收正文、README共同约束与HEAD逐字一致，也匹配清单16段哈希。没有改验收标准、Issue状态或原失败报告。
- 完整重读billing规范；对照新版01/03/05及全部A01–A07复核。确认生产delta仅去除发行函数的pool.Paused拒绝并加注释，其余核心/查询/服务/迁移不变。通过重构原文件并校验r1哈希，确认原集中测试及原全部进程场景逐字保留，仅新增暂停发行场景；没有删断言换取通过。
- 最终核对记录：`build/dev/mv2-006-independent-r2-final-verification.json`，包含本轮绑定、冻结段及所有独立探针/失败和成功日志的哈希。

## 全项结果

| 条目 | 结果 | 整体业务依据与复验 |
| --- | --- | --- |
| A01 权重、组合、空奖 | **通过** | 统一服务端安全随机权重算法，不由浏览器提供中奖票；库存耗尽退出候选，组合奖一次只减一份库存。空奖正常持久、不发零元赠金；技术随机源失败回滚、不消耗机会。原空奖、耗尽及真实赠金测试全部保留并在新三库矩阵通过。 |
| A02 全站名额与供给 | **通过** | 有限总容量，Issued不因空奖、过期或清零回减；追加计算未发行容量与有效未用责任，pending中奖已经减库存，不重复占供给。正权重可兑付无限库存可支持有限机会，空奖和零权重不能扩大保障容量。并发追加单胜、操作幂等留痕；没有支付预留。 |
| A03 持久结果与多实例 | **通过** | 稳定来源/请求键与归属、payload校验保留，结果/扣机会/扣库存/全部责任同事务。最终真实进程矩阵覆盖同一请求并发、最后名额/库存、追加、丢响应、部分到账、SIGKILL前提交回滚及实际PG锁等待；Redis客户端断开仍保持一致。三库及MySQL已有快照当前读取回归通过。 |
| A04 运行规则 | **通过，F1关闭** | 池暂停只禁止新开奖，有效来源活动仍可发行机会，继续受有限名额和原截止约束。暂停中不足整项不足，不影响原机会；恢复可抽，暂停自然计时，恰到期发行/抽奖拒绝，恢复不复活。历史不足不回填、3对2不拆发、开始后或有承诺时核心不改、库存补充/追加与有效延期均保留。 |
| A05 归属与真实奖励 | **通过（本Issue持久服务范围）** | 发行与开奖分离恢复正确；current user/owner、稳定来源及server RNG不变，禁止越权或禁用后重放获取结果。真实服务即时送独立赠金，本金不增加。活动接入在007，管理/用户HTTP鉴权、CSRF在012/013，按冻结分工不在这里虚报已经完成。 |
| A06 整份组合责任 | **通过** | 结果提交时已有全部组件责任，逐项真实兑现；部分失败保持pending和真实error，最后组件成功才complete。真实PG写入约束使第二份失败，再两个实际进程恢复，原result及第一gift ID/期限不变，只有两份gift和两条gift_credit。查询、重放、补发均重新校验归属/当前用户状态。 |
| A07 供给及时间竞争 | **通过** | 正权重可兑付供应、组合一份、既有责任不重复追加及未开始承诺保护仍成立。延期与抽奖/清零竞态、到期边界、暂停自然到期场景全部重跑；新场景额外证明暂停中两个独立用户争最后名额一issued一shortage，不能借解除暂停发行限制超发。 |

## F1修复与独立探针说明

r1指出的额外限制已删除：`IssueMarketingLotteryChances`现在仅在真实截止到达时拒绝新发行，pool.Paused仍在Draw校验，发行仍持有同一用户/奖池锁并按容量原子递增。没有更改既有机会截止、权重或来源活动资格。

原r1探针新增了暂停后发行1次的断言，并复用原集中测试的池。父代理原样重跑`result-r2.log`时，新增断言已经成功，但新增机会消耗了原池容量，造成后面的“发行2次”断言shortage。这是独立探针夹具污染，不能将该日志整体宣称通过。

审查者首次只隔离奖池，新增机会仍属于原用户，随后原清零测试查询该用户全部机会时从2条变3条，`result-isolated-r2.log`仍失败。再次同时隔离探针用户和奖池后成功。两次失败日志与对应探针源均保留，不覆盖历史。

最终探针保留原暂停成功断言及全部原有断言不变，只把探针数据放入独立用户/池；生产代码及原测试未改。`result-fully-isolated-r2.log`退出0，原业务断言与后续原全部SQLite测试均通过。这是修正审查夹具的共享状态，不是弱化冻结验收标准或修改预期来适配错误实现。

新增正式集中回归在真实三库覆盖暂停发行、名额不足、暂停开奖不扣次数、恢复可抽、恰到期发行和开奖拒绝、恢复后不复活。新增双进程回归检查最后名额原子争抢，保障此次修复不会破坏有限发行。

## 独立执行

```sh
.dev/run go test ./model ./service -count=1 -timeout=300s
# 退出0，两包通过；build/dev/mv2-006-independent-r2-regressions.log。

.dev/run go test -overlay=/workspace/build/dev/mv2-006-pause-probe/fully-isolated-overlay.json ./model -run '^TestMarketingLottery/sqlite$' -count=1 -timeout=180s -v
# 退出0，保留原暂停断言与全部后续断言；build/dev/mv2-006-pause-probe/result-fully-isolated-r2.log。

git diff --check
# 退出0。
```

另保留首次隔离不完整的退出1日志`build/dev/mv2-006-pause-probe/result-isolated-r2.log`及父代理原样重跑退出1日志，不将其当作通过。审查者只增加报告和忽略目录证据，没有修改生产、原测试或状态，未commit/push/deploy。

## 三库、实际进程及迁移证据

下列为实现代理执行、审查者复核脚本、断言、完整相关结果与清单绑定；审查者没有再次重置共享证据数据库，也没有声称独立重跑这些命令。

| 命令/范围 | 证据及结论 |
| --- | --- |
| `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` | `build/dev/mv2-006-accounting-r2.log`，model/service真实SQLite、MySQL、PostgreSQL全部通过，无FAIL/SKIP，包括新增暂停和已有全部资金/Realtime回归。 |
| `python3 .dev/marketing-v2-requirements/validation/lottery_process.py all` | `build/dev/marketing-v2-validation/lottery-process-20261123T014614.220067Z.log`，9个场景组及最终LOTTERY_PROCESS_MATRIX_OK。不同OS/PG PID，真实共享PG/Redis DB13，原全部场景与新暂停最后名额场景通过。观察到blocked_backend=173829、owner_backend=173828的真实锁等待再SIGKILL恢复。 |
| `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` | 沿用绑定r1迁移日志及六份fresh/upgrade证据；本轮持久字段/表/索引/迁移及probe未变，新七表、正式custom/main代表记录升级、重复启动、旧索引及实际唯一约束保持对应当前schema。不是旧营销数据迁移。 |

实际版本：SQLite **3.50.4**、MySQL **8.4.13**、PostgreSQL **17.11**、Redis **7.2.17 / DB13**。没有宣称全部最低版本均已运行；当前改动没有新增依赖更高版本的数据库特性。正式升级基线`ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`来自custom/main，用户已明确旧营销未上线。

## 共同约束与后续边界

- 财务和机会一致性依靠主数据库事务、稳定锁序与唯一业务键，非进程内状态或Redis临时锁。普通兑付不占奖池共享锁；本Issue不承诺线上并发容量，最终性能在015验收。
- 奖金与付费资产独立，实际显示金额按配置换算快照，不把1 quota当1元。无手动激活、审核、模型/分组限制、积分、主题、订阅、旧赠送码或新中间件。
- OWASP适用结论沿用r1：Authentication/Session Management适用章节、Authorization及稳定ASVS5.0.0 V8的8.2.1、8.2.2、8.3.1、8.3.2。授权实现与实际禁用/错误归属/replay场景未弱化。不存在新HTTP/Cookie/会话流程，不能因此声明全站ASVS合规；012/013必须在真实入口验收可信身份、权限及CSRF/Origin，不能把任意userID参数当完成浏览器认证。
- source活动结束不撤销已承诺机会；暂停不取消有效来源发奖。007对迟到尚未分配资格必须按真实时间记录，不能伪造now复活过期机会，也不能将机会错误牵连独立固定赠金。本轮不将尚未开发的007当作通过。
- clear原语处理未用机会和pending责任；实际退款账户停用、在途结算门禁、付费/已到账赠金/余数清零由011整项编排。核心通过不等于完整退款入口已验收。
- r1失败、探针污染失败及本轮成功证据均保留；没有更改冻结标准。现在可以更新MV2-006完成状态并commit，然后按同样流程进入下一Issue。
