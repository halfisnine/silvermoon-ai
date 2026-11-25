# MV2-006 整项独立验收 r1

日期：2026-11-23。审查者：`mv2004_path_audit`（独立 sub agent）。结论：**本轮不通过，不应标记 Issue 完成或进入下一 Issue 实施。发现一个必须修复的业务限制：独立奖池暂停错误地阻止有效来源活动发行机会。** A01、A02、A03、A06、A07通过；A04、A05因该问题不通过。修复后必须重新检查全部 A01–A07 和共同约束，不能仅重跑失败点。

## 版本与证据绑定

- 工作树 `/root/.codex/worktrees/marketing-v2/silvermoon-ai`，分支 `codex/marketing-v2`；HEAD `bd2ca864f4adb9c35e6233638fdb440540339a80` 加本轮未提交实现。
- 主清单 `build/dev/mv2-006-review-manifest-r1.json`，SHA-256 `de6cf84dcb25177573f283c0c8056a6f39210f249fb5de52cef046e8a3f181fa`。开始及结束核对11个文件、20个产物/资料均匹配。补充 `build/dev/mv2-006-binaries.json` 的实际 lottery-process、candidate-probe、baseline-probe 共3个二进制在结束核对全部匹配。原清单未包含实际 lottery-process 文件名，因此使用该补充，不伪称原清单已涵盖。
- 15个 Issue 验收正文及 README 共同约束逐字与 HEAD 比较，16项未变；审查者没有修改冻结标准、业务源码、原测试、Issue状态或执行提交。
- 最终核对：`build/dev/mv2-006-independent-r1-final-verification.json`，包含清单、补充二进制与独立日志哈希。
- 完整阅读 billing 规则，核对新版01/03/05及本Issue冻结条目、奖池核心、查询、事务重试、即时兑付服务、集中测试、实际进程夹具及迁移差异；按独立奖池与活动资格、资金隔离、多实例真实业务验收，不以旧营销规则替代。

## 全项结果

| 条目 | 结果 | 验收证据与边界 |
| --- | --- | --- |
| A01 权重、组合、空奖 | **通过** | wheel/blind_box仅呈现方式不同，服务端统一crypto/rand按正权重选择；耗尽库存排除，有限库存减少不改变未耗尽奖权重。组合奖库存计一份，整份责任同事务保存。空奖持久结果complete且不发零元赠金；熵源失败回滚、不耗机会。集中真实数据库测试覆盖空奖、耗尽及技术错误。 |
| A02 名额与供给 | **通过** | pool锁串行发行/追加。容量有限，发放后Issued不回减，现存未用责任与未发行容量均进入追加校验；pending结果已扣过库存，不二次占同一份。空奖/零权重不计保证供给。有限库存不足拒绝追加，有效无限库存可支持有限容量。管理操作持久幂等留痕，无支付预留。 |
| A03 幂等与多实例 | **通过** | 来源键、请求键与payload/owner绑定；一次机会与库存扣减、结果、全部奖品责任同事务。两真实OS进程共享PG/Redis，最后名额/库存/追加单胜、同请求并发、提交后丢响应、部分到账恢复、实际SIGKILL回滚及PG锁等待均有断言。三库核心和MySQL旧快照current read回归通过；Redis实际断开客户端仍不超发。 |
| A04 运行规则 | **不通过：F1** | 已验证3对2不拆发、历史不足不补、空奖/过期/清零不恢复名额；开始后/已有承诺不能改核心，补库存/追加和有效延期保留。但池暂停同时阻止独立活动送机会，超出已确认的暂停抽奖语义，影响活动自动发奖。不得用此限制作为007接入既定行为。 |
| A05 核心业务归属与真实奖励 | **不通过：F1** | owner、稳定键与服务端随机、真实赠金到账均通过；暂停限制使有效来源活动的发行入口无法按新版分离规则工作，需修复后全项复验。无公开HTTP接口；正式管理/用户HTTP、Cookie/CSRF按已冻结分工在012/013验收，本轮不能声称已完成。 |
| A06 组合责任与恢复 | **通过** | 全部组件先持久，逐组件事务真实入独立赠金账；最后组件成功才complete。真实PG CHECK造成第二组件失败时返回pending及error，第一份保留；解除后两个进程同时恢复，原result/第一份gift ID/到账时间不变，正好两份gift与两条gift_credit，本金不增加。授权复核在replay前执行，禁用用户和错误归属的draw/get/fulfill拒绝返回内容。 |
| A07 供给与到期竞争 | **通过** | 有限/不限量混合只计正权重可兑付奖项；空奖不扩容，一次组合只一份。已发未用/已开奖待发责任不能重复支持追加，尚未开始但已发机会时核心修改拒绝。进程场景覆盖延期/抽奖、暂停/抽奖、清零/延期、清零/抽奖，恰到期延期及抽奖均拒绝；暂停不冻结自然时钟。F1是暂停额外阻止新发行，不是该项既有机会自然到期不变量失效。 |

## F1：独立奖池暂停不得取消有效来源活动的机会发行

位置：`model/marketing_v2_lottery.go:454`，`IssueMarketingLotteryChances` 中 `if now >= pool.EndsAt || pool.Paused`。

具体场景：充值活动仍启用，引用库存及名额足够、未到截止但暂停抽奖的奖池。用户满足充值奖励条件时，发行函数直接返回 `ErrMarketingAccess`，没有生成机会。它把“此刻不能抽奖”扩大成“来源活动不能送机会”。

依据：01共用业务规则要求独立奖池暂停与来源活动暂停分开；03独立管理与机会发放/开奖结果分开配置；05 N03规定暂停不扣机会、仍自然计时。已确认语义没有授权用池暂停取消有效来源活动资格。暂停应限制开奖；发行仍按来源活动资格、当前容量及真实截止校验。该修正落在A04/A05及整体业务，不需改变冻结验收或增加新玩法。

独立复现：在忽略目录复制集中测试并通过Go overlay增加暂停后发行1次的业务断言，使用真实临时SQLite、真实生产model事务，未改原测试或源代码。建立正常池 → 管理员pause → 有效用户以新稳定来源发1次（尚未截止、容量足够），断言发行成功；实际返回 `marketing funding access denied`，退出1。证据 `build/dev/mv2-006-pause-probe/result.log`，对应overlay/source/hash已保存。

修复要求：删除发行对pool.Paused的额外拒绝，保留已结束拒绝、归属、幂等、有限容量和供给约束。集中补充“暂停中发行成功；容量不足仍整项不足；暂停中开奖拒绝且不耗次数；恢复后按原机会可开奖”，同时覆盖两实例暂停状态下最后名额竞争。随后按原标准全项复验。

迟到活动事件在池截止后尚未分配的资格如何记录，是007事件执行需要正确处理的边界；不能为了通过本项伪造now、复活失效机会，也不能把机会失败牵连独立固定赠金。本轮不新增或提前宣称007行为已完成。

## 独立执行及提供证据复核

审查者实际执行：

```sh
.dev/run go test ./model -run '^TestMarketingLottery/sqlite$' -count=1 -timeout=180s -v
# 退出0；build/dev/mv2-006-independent-r1.log；无FAIL/SKIP。

.dev/run go test -overlay=/workspace/build/dev/mv2-006-pause-probe/overlay.json ./model -run '^TestMarketingLottery/sqlite$' -count=1 -timeout=180s -v
# 退出1，F1业务断言失败；build/dev/mv2-006-pause-probe/result.log。

git diff --check
# 退出0。
```

实现方已执行、审查者审阅完整相关结果和夹具、哈希绑定但未在本轮重复重置数据库的证据：

| 命令 | 结果及范围 |
| --- | --- |
| `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` | `build/dev/mv2-006-accounting-r1.log`，model/service真实三库通过，未发现FAIL/SKIP。包括本Issue及已验资金/Realtime回归，不能据此覆盖缺失的F1场景。 |
| `.dev/run go test ./model ./service -count=1 -timeout=300s` | `build/dev/mv2-006-package-regressions.log`，两包通过。 |
| `python3 .dev/marketing-v2-requirements/validation/lottery_process.py all` | 最终`build/dev/marketing-v2-validation/lottery-process-20261123T013411.564446Z.log`。实际子进程PID、PG backend PID、真实锁等待、Redis关闭、lost response和SIGKILL均从夹具实现核对；上述所有SCENARIO_OK与最终LOTTERY_PROCESS_MATRIX_OK存在。核心/service进程验证不是HTTP身份认证验证。 |
| `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` | `build/dev/mv2-006-migration.log`与绑定六份fresh/upgrade verify。真实新库、正式custom/main代表记录升级及重复启动，数据/旧索引及实际唯一约束保持；新七张奖池表存在。只增加本Issue迁移表，无旧营销数据迁移。 |

实际版本：SQLite **3.50.4**、MySQL **8.4.13**、PostgreSQL **17.11**、Redis **7.2.17 / DB13**。不宣称已运行所有最低版本；本Issue没有引入依赖更高数据库版本的新特性。升级基线为正式custom/main `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`，用户已明确旧营销从未上线，不要求旧营销迁移。

## 共同约束与权限边界

- PostgreSQL事务、唯一键、用户→奖池稳定锁顺序维护全站一致性，Redis不承担发行与资金真相。正常兑付只锁用户/责任，不占奖池热点锁；罕见清零锁池的成本属于退款入口后续整体性能验证，不能称已证明线上容量。
- 赠金实时发放且到账生效，使用配置金额换算保存快照，永久赠金仍有有限金额；不入付费本金。不引入人工审核、激活、模型/分组限制、积分/主题/订阅或旧免费兑换码。
- 已核对OWASP Authentication/Session Management适用章节、Authorization Cheat Sheet及稳定ASVS5.0.0 V8。核心适用8.2.1功能权限、8.2.2对象归属、8.3.1可信服务端、8.3.2撤权后重放复核。管理员改配/追加/清零校验当前角色，用户draw/get/fulfill均核验当前状态与归属。报告不把内部userID参数视为完整浏览器认证；012/013接入时必须使用可信会话身份、审计与CSRF/Origin检查。
- 本Issue无新登录、会话、Cookie或公开路由；没有对全站ASVS作合规声明。授权审查未发现除业务暂停限制外的新阻断。
- 清零原语保留来源并清理未用机会与pending责任，不自动回收发行容量；退款实际停用/在途结算/已到账赠金与本金清零由011整项编排，不提前验收。
- 失败反例保留，冻结验收未改。修复F1后应提交新代码/产物绑定，重新整项复验；当前不得用通过的单测或其他条目掩盖A04/A05不通过。
