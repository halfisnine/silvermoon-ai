# MV2-007 整项独立验收 r1

日期：2026-11-23。审查者：`mv2004_path_audit`（独立 sub agent）。结论：**本轮不通过。A05要求的真实三库活动并发验证尚不完整：已有PostgreSQL真实多进程证据，但缺SQLite/MySQL活动决议和奖励的并发证据。** A01/A02/A03/A04/A06/A07在本Issue内部活动引擎范围通过；A05未通过。不能更新完成状态或提交为已完成，补齐后必须全项复验。

本轮没有确认新的生产实现阻断。缺少验证不等于发现SQLite/MySQL已经发生超发，但也不能用旧资金并发测试替代新活动事务验收。

## 绑定与范围

- 工作树 `/root/.codex/worktrees/marketing-v2/silvermoon-ai`，分支`codex/marketing-v2`，HEAD `8203011ce9adefdf3c9c8e2964bf6b028d5e6024`加本轮未提交实现。
- 清单`build/dev/mv2-007-review-manifest-r1.json`，SHA-256 `8dca46503cab6f44ee4a0c1b8660d3363bb920366cdc32e1226b501180977d7c`；开始/结束核对14个文件和19个产物全部匹配，含实际activity-process、model/service测试二进制及迁移probe。
- 15个Issue冻结验收正文和README共同约束与HEAD逐字一致、匹配16个清单哈希。没有改冻结标准、业务源、原测试或状态，未commit/push/deploy。
- 完整阅读billing规范；核对新版四类活动02以及01/03/05共享原则、MV2-007 A01–A07、README共同约束。读取活动配置/事实/决议/概率/兑现核心、金额及既有Grant重构、任务查询/调度、名额上限入口，集中三库测试、实际进程夹具与相应最终日志。
- 读到的签到claims、注册/支付事实入口属于内部引擎边界，008–010实际支付通知/卡密/注册/签到日历尚未验收；012/013正式HTTP权限和界面不提前标完成。
- 最终核对：`build/dev/mv2-007-independent-r1-final-verification.json`。

## 全项结果

| 条目 | 结果 | 整体业务及证据 |
| --- | --- | --- |
| A01 四类活动与互斥 | **通过** | 注册、签到每日/连续、五种充值玩法、好友注册/分好友累计/实付比例及组合直接配置。先按同受益人同组优先级、同分活动ID选择，未分组分别发，选中/抑制持久；之后才判断机会供给，不能因机会不足改低优先级，固定赠金独立。无活动赠金预算或通用用户奖励上限。草稿、一次性启用时刻、公开与资格分离，暂停历史按事实时间判断。 |
| A02 自动与非法组合 | **通过** | 正常赠金由真实服务即时兑现，无领取/激活/审核；全份责任先保存，逐项失败仍尝试其他项，错误和Pending不伪装全部成功。服务端拒绝累计比例、非充值累计概率机会、非法奖品组合。活动记录zero_amount且不创建零额赠金；金额非法或溢出保留pending责任与错误。任务工作批次不是用户奖励次数限制。 |
| A03 金额与概率独立 | **通过** | paid_ratio以真实到账付费额度为基数并验证原转换匹配；cash_ratio以PaidCents/100及冻结CNY转换为基数。保留PaymentID、实付、到账、比例、转换与结果，最终额度不经过显示金额反算。真实测试实付¥1/到账20quota时，50%普通赠金10quota，邀请赠金按50quota/¥1为25quota。本金不增加。机会概率只控制自身，固定赠金照发；直接/概率与必中奖/可空奖池不耦合。 |
| A04 事实及恢复 | **通过（已提供并发范围为PG）** | 成功付款同事务捕获规则和容量依据；事务回滚则付款/事实一起撤销。source用户锁串行事实，最早未决事实先处理，不接受worker任意跳指定事实。规则与邀请关系、旧暂停区间已冻结；暂停前事实延迟到活动后仍发赠金，暂停中事实不因恢复补成资格。PG两真实进程重试/丢响应/组件故障恢复不双发。SQLite/MySQL活动本身并发缺口统一列F1/A05，不把本项通过解释为三库并发已经验证。 |
| A05 不可变与三库并发 | **未通过：F1未验证** | 已验证启用开始后不能原地改核心，新身份复制不搬余数/事件；旧名额不足不补、概率只判一次、机会足额或不发。SQLite/MySQL/PostgreSQL顺序/快照测试和PG多进程通过，但没有SQLite/MySQL活动级并发证据，未满足条目末尾的“三库并发验证”。 |
| A06 持久赢家与顺序 | **通过** | 互斥赢家/抑制、probability_miss、shortage/issued/expired均保存，秘密随机种子随决议持久，档号与奖励位置确定后续概率，不由浏览器传入。一次付款跨多档分批处理时不会重抽已决档。在线/付费码共享真实入账序列，首笔未达标不会让第二笔变首充，累计余数按实付跨单。PG不同worker不能重复决议/重复发奖；正式入口接入仍属008。 |
| A07 追加与新旧资格 | **通过** | 名额不足不改互斥赢家，固定赠金真实到账。事实保存池Capacity上界，延迟旧资格不能消费之后追加；已shortage原key不补，新事实可用新上界。已承诺真实Issue占有限名额，丢响应后按同key恢复，不让竞争者拿走。复制活动新时期用新进度，既有余数留原活动。 |

## F1：SQLite/MySQL活动并发尚未验证

冻结A05明确要求“三库并发验证”。目前：

- `model/marketing_activity_test.go`执行真实三库的顺序业务及已有快照查询，未启动活动决议/兑现并发事务。
- `activity_process.py`使用隔离PostgreSQL及Redis DB12，实际两个OS进程；其十类场景有效，但并未运行SQLite/MySQL。
- accounting矩阵包含旧`MarketingPersistence`的三库并发，仅证明资金基础，不能代替新活动互斥、概率决议、进度、名额及奖励事务。

父代理已确认没有其他SQLite/MySQL活动并发证据，本轮据此保留未通过，不降低标准。

需要补充实际SQLite/MySQL/PostgreSQL活动并发验证：不同数据库连接竞争同一用户最早事实/累计档、同源事实重放、同奖励并发兑现、互斥和概率结论不改变、名额竞争不超发，检查真实decision/reward/gift/ledger/issue/进度结果。真实数据库上的多连接测试可补三库并发维度；PG既有独立OS进程仍保留证明多实例，不能用同进程goroutine冒充两实例。SQLite遇BUSY要通过真实整事务重试，不将锁失败当业务成功，也不能忽略错误。

补充测试/产物及新清单后，仍需重新全项A01–A07和共同约束复验，不能只补A05即直接标完成。

## 早期问题关闭与重要实现核对

早期指出的1024条失败前缀饥饿已修复：每source用户有持久调度游标，查询在source锁下按reward ID轮转；推进只影响调度，不把pending改成功或丢掉责任，单次调用避免重复尝试。真实进程通过活动生成1024份旧邀请人待发+第1025份来源用户赠金，禁用邀请人时两个服务调用后第1025份到账，旧项仍pending；恢复邀请人后旧1024份恰好各到账一次，正常项不重复。本次不是随机循环性能测试。

逐组件刷新状态改为锁定读取至多一个pending，避免每份兑现读取全量剩余行。查询采用JOIN current read，MySQL已有事务快照不能隐藏已提交任务；兑现按source/beneficiary用户ID排序锁，再当前读取reward及权限。事实捕获不因付款者禁用丢掉收款事实，受益人禁用禁止兑现和用户开奖，内部仍可保存已经成立的机会承诺。后续011必须清理旧未决责任，不能恢复账号后重发已清退权益；此边界未在本Issue伪报完成。

名额AtCapacity把既有事实可用上界纳入摘要；同key更换上界冲突。它不预留支付名额，不保证不同用户按支付先后分配；未分配资格遇真实奖池截止记录pool_expired，不回拨now、不伪装概率未中或名额不足。已经持久承诺的Issue与未分配资格有区分。

比例赠金Grant验证可信内部快照自洽性，不从浏览器接收该结构。capture从本事务实际Payment表读取真实金额，后续使用持久快照。固定赠金的旧摘要和Conversion格式保持；比例Conversion保存计算证据，不能后续把其中Money基数当赠金面额展示。

## 独立执行

```sh
.dev/run go test ./model ./service ./internal/marketing -run '^TestMarketingActivity/sqlite$|^TestMarketingMoneyConversion$|^TestMarketingLottery/sqlite$|^TestMarketingPersistence/sqlite$|^TestMarketingBillingSessionLifecycle/sqlite$|TestRecharge|TestPeriod' -count=1 -timeout=240s -v
# 退出0，model/service/internal三个包通过，无FAIL/SKIP。
# build/dev/mv2-007-independent-r1.log

git diff --check
# 退出0。
```

使用真实临时SQLite与生产model/service事务，没有模拟账本。本轮未新增独立业务反例或修改测试；F1是证据范围缺失，不编造数据库失败。

## 提供的证据复核

以下由实现代理执行，本审查者读取结果、脚本和断言并绑定，未重复重置共享证据库，也未声称独立重跑三库/进程矩阵。

| 命令/范围 | 结果与限制 |
| --- | --- |
| `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` | 最终`build/dev/mv2-007-accounting-r2.log`两组PASS、无FAIL/SKIP；Activity真实三库业务/快照通过，不能补足F1。初次r1的MySQL同表子查询清理1093失败保留，随后仅修测试清理。 |
| `.dev/run go test ./model ./service ./internal/marketing -count=1 -timeout=300s` | `mv2-007-package-regressions-r1.log`中model/internal通过，service当次因旧测试fixture漏活动表失败；不能称该整条命令通过。补测试schema后`mv2-007-service-regressions-r2.log`全service通过，后续三库再次覆盖。 |
| `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` | 最新`mv2-007-migration-r2.log`与六份verify日志对应最终八表/EnabledAt/Published/技术游标schema。真实新库、正式custom/main代表记录升级、重复启动、旧记录/索引/实际唯一约束保持。不迁移旧营销数据。 |
| `python3 .dev/marketing-v2-requirements/validation/activity_process.py all` | `activity-process-20261123T021207.622827Z.log`十类场景和ACTIVITY_PROCESS_MATRIX_OK。实际exec子进程与PG backend PID，真实资金/机会事务；支付顺序、五种充值、互斥与同分、概率重放、暂停/复制、组合故障、邀请、可信签到claims、草稿公开、服务部分失败和1024边界均有实际断言。没有新增HTTP入口，不作为支付平台或日历资格验收。 |

数据库实测SQLite **3.50.4**、MySQL **8.4.13**、PostgreSQL **17.11**、Redis **7.2.17 / DB12**。不宣称全部最低版本已测。升级基线为正式custom/main `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`，旧营销未上线、不要求旧营销数据迁移。

## 安全、范围及共同约束

- 主库事务、唯一键、固定锁序及持久事实维护跨实例一致性，不依赖进程内内存或Redis短锁承担资金/名额真相。无新中间件；有限工作批次和游标不等于额外运营奖励上限。
- OWASP沿用006已读取的Authentication/Session Management适用章节、Authorization及稳定ASVS5.0.0 V8：8.2.1功能权限、8.2.2归属、8.3.1可信服务层、8.3.2撤权后检查。所有管理员操作入口在幂等重放前查当前角色；兑付先校验受益人和当前状态。可信事实处理不等于开放任意userID浏览器写入口。无新HTTP/Session/Cookie流程，不作全站合规声明；008–013各真实入口必须自行完成适用安全验收。
- 资金与赠金隔离，实时到账生效，过期/永久属性保留；没有引入旧审核、手动激活、模型/分组限制、赠送兑换码、积分/主题/订阅或消费触发营销。
- 已有失败日志、早期检查和最终证据保留，没有改变冻结验收内容。F1完成且全项复验通过前，本Issue继续未完成。
