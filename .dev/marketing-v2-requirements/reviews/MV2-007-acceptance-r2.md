# MV2-007 整项独立验收 r2

日期：2026-11-23。审查者：`mv2004_path_audit`（独立 sub agent）。结论：**A01–A07及适用共同约束全部通过，没有剩余阻断，可以标记MV2-007开发完成并commit。** 本结论限于绑定版本的内部活动引擎；不提前验收008–010真实支付/注册/签到入口、011退款清零或012–014正式HTTP/页面。

## 绑定与全项复验

- 工作树`/root/.codex/worktrees/marketing-v2/silvermoon-ai`，分支`codex/marketing-v2`；HEAD `8203011ce9adefdf3c9c8e2964bf6b028d5e6024`加本轮未提交实现。
- 清单`build/dev/mv2-007-review-manifest-r2.json`，SHA-256 `73fa37fc87dab0d36aa531a5ef54ca7a2e6b5bfc7973a14209b0773cd8d144cc`；开始、结束16个文件及24个产物全部匹配。
- 15个Issue验收正文和README共同约束逐字等于HEAD并匹配16个冻结哈希。没有修改标准或Issue状态，未修改源码/原测试，未commit/push/deploy。
- 本轮完整重读billing规范，逐条复核全部A01–A07及新版整体规则。金额、事实、活动、query/service、彩票Capacity、schema及真实进程夹具均与r1已审版本哈希一致；完整读取变更的事务重试实现、新集中并发及非重试错误回归。没有只验A05或仅看成功标记。
- 最终绑定与独立日志摘要：`build/dev/mv2-007-independent-r2-final-verification.json`。

## 全项结果

| 条目 | 结果 | 本轮整体复核证据 |
| --- | --- | --- |
| A01 四类活动与互斥 | **通过** | 注册、每日/连续签到、五种充值玩法、邀请注册/分好友累计/实付比例及组合规则保持。草稿、启用和公开分离；事实时间决定资格，暂停区间保存。同受益人同组先按优先级/同分ID选定，非互斥叠加，机会不足不改赢家，固定赠金照发。新增三库并发同时验证selected/suppressed、进度及固定奖。无活动预算和通用用户奖励上限。 |
| A02 自动发放与恢复 | **通过** | 责任先持久、真实服务自动兑现，无领取/激活/审核。非法累计比例、非充值累计概率等组合仍服务端拒绝；运行金额错误保持责任，zero_amount不产生零赠金。单项失败不拦其他奖励，持久游标继续覆盖1024失败前缀后的合法项；真实进程重新验证恢复后旧责任不丢且不重复。 |
| A03 基数与概率 | **通过** | 普通充值比例用真实到账付费额度；邀请比例用实际PaidCents及冻结CNY转换，门槛统一实付。不从显示金额反算结果，PaymentID/实付/到账/比例/换算/结果留证。赠金进入独立资产，不增加本金。概率仅决定该机会项，固定赠金独立；直接/概率与两类奖池兼容，实际0%、100%、50%重放回归保留。 |
| A04 事实与多实例恢复 | **通过** | 入账和事实同事务，规则/关系/池容量快照固定；最早未决事实按source用户锁顺序处理。旧暂停前资格继续恢复，暂停中事实不补成资格，迟到处理不重新取新规则。三库重叠事务与PG两个实际OS进程均验证幂等，部分真实数据库写失败及丢响应恢复保持原来源/原结果。 |
| A05 运行不可变与三库并发 | **通过，r1 F1关闭** | 新增真实SQLite/MySQL/PostgreSQL两独立事务已读库后屏障放行，验证同源并发规划、同奖励并发兑现、不同用户争最后名额；全部实际结果、概率、进度、gift/ledger/issue均检查。开始后核心不可改、复制新对象、旧余数不搬、历史不足不补和整项机会足额或不发保持。真实MySQL1213缺口已修复并重跑。 |
| A06 持久决议与顺序 | **通过** | selected/suppressed、shortage、probability_miss持久；种子与档号/奖励位置固定且不由浏览器提供。新增并发重复Process后decision/reward逐字段保持，真实50%结果不重抽；并发后累计ProcessedAwards=1、余数=50，未翻倍。在线/付费码共享入账序列、严格首笔与首次达标/累计口径保持。 |
| A07 追加与新旧责任 | **通过** | 同key原结果不变，事实Capacity上界阻止旧资格使用新追加；新事实使用新名额。新增两用户并发只一issued一shortage，两个固定赠金各到账一次，本金不变；随后追加并发重放不补旧shortage、池Issued仍1。已承诺Issue与未分配资格区分，组合技术失败恢复原责任，复制对象不搬旧余数/已领事实。 |

## r1证据缺口与MySQL真实问题的关闭

**r1 F1已关闭。** 新的`marketingActivityConcurrentTest`让两个真实数据库事务先各自完成读取，再通过屏障共同进入生产业务；两事务占用不同物理连接并存在重叠，不是顺序调用伪装并发。SQLite使用生产整事务重试。新增场景在三库全部通过，PG实际独立OS进程矩阵仍保留，明确不把同进程goroutine称为双实例。

三库测试核对：同用户最早事实仅一个worker处理；互斥赢家和叠加奖不变；累计只推进一次；0%/100%及真实50%概率结果重放不变；同一赠金只有一个gift ID、金额/有效期一致、只有一条gift_credit；两个不同用户最后机会只有一个issued，另一个shortage，两人的固定赠金仍各发一次；追加后旧结果和发行数不变。

**补验发现的MySQL1213处理缺口已关闭。** r3真实InnoDB诊断显示两个用户赠金当前读在`idx_mv2_gift_order`的supremum区间互持锁，随后INSERT互相等待，InnoDB明确回滚其中整个事务。该失败及完整诊断保留，未归类成测试夹具问题。

现在`WithMarketingTransaction`仅在MySQL驱动明确返回1213时允许重新执行整个事务，最多4次，等待可由context取消。不是重复最后一条INSERT；读取、资格检查、来源键和全部写入重新执行。1205、连接中断、EOF、普通业务错误及未知提交结果没有被泛化为可重试。SQLite原BUSY/LOCKED整事务机制保留，PostgreSQL没有被无依据纳入新重试范围。

新回归在真实数据库先写赠金，再返回业务错误/注入1205、driver.ErrBadConn、UnexpectedEOF错误，验证只有一次callback执行、gift和ledger均回滚。该回归验证错误分类及实际事务回滚，不宣称注入的1205是测试现场真实发生的服务端超时。1213依据是真实r3锁诊断、生产精确错误类型分支以及同一并发业务在r4通过；不把错误注入当真实死锁证据。

`grantMarketingGift`的MySQL current read、用户串行锁、remaining余额求和、MaxWalletQuota检查及来源幂等均与r1字节一致。未为避免死锁换成旧快照SUM、去掉上限、吞错误或使用模拟账本。超过有限重试仍返回错误，持久责任留待恢复，不承诺永不发生数据库冲突。

## 独立实际执行

```sh
.dev/run go test ./model ./service ./internal/marketing -run '^TestMarketingActivity/sqlite$|^TestMarketingMoneyConversion$|^TestMarketingLottery/sqlite$|^TestMarketingPersistence/sqlite$|^TestMarketingBillingSessionLifecycle/sqlite$|TestRecharge|TestPeriod' -count=1 -timeout=240s -v
# 退出0，model/service/internal三包通过，无FAIL/SKIP。
# 包括新增SQLite重叠事务、同奖兑现、最后名额和非重试错误回滚。
# build/dev/mv2-007-independent-r2.log

git diff --check
# 退出0。
```

独立运行使用真实临时SQLite和生产model/service，未重置共享矩阵数据库。并未声称审查者独立重跑MySQL/PostgreSQL矩阵或OS进程。

## 绑定的实现方证据复核

| 命令/范围 | 结果与边界 |
| --- | --- |
| `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` | `build/dev/mv2-007-accounting-r4.log`，model/service真实三库全部通过无FAIL/SKIP。九个新增关键组合（3库×并发规划兑现/最后机会/非重试回滚）逐项PASS，同时覆盖旧金额、资金、彩票、Realtime及service生命周期。 |
| `python3 .dev/marketing-v2-requirements/validation/activity_process.py all` | 重建后的`mv2-007-process-r2.log`、`activity-process-20261123T023239.453289Z.log`，十类真实OS进程业务全部通过。共享PG/Redis DB12，真实支付/机会/赠金账，无HTTP认证或外部支付/日历实现替身冒充集成验收。 |
| `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` | 沿用`mv2-007-migration-r2.log`及六份fresh/upgrade，schema/迁移/驱动版本未变化。最终八表/启用公开/游标、新库、custom/main代表数据升级、重复启动及基础约束保持仍对应当前代码。 |
| 之前包级回归 | r1的model/internal全包通过，service旧fixture缺表失败后只补测试schema并全包通过；这些失败仍保留。本轮又独立跑受影响model/service路径，三库和实际进程均按新事务helper重建执行。 |

数据库版本：SQLite **3.50.4**、MySQL **8.4.13**、PostgreSQL **17.11**、Redis **7.2.17 / DB12**。不宣称所有最低版本都已实测。正式升级基线为custom/main `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`；不迁移用户明确从未上线的旧营销数据。

## 共同约束及交付边界

- 主库事务、稳定锁序、唯一来源/决议及持久游标保障多实例一致性；Redis不承担财务或名额唯一性。工作批次限制单次处理量，不限制合法奖励总量。没有新中间件，未声称线上50–100并发容量已完成验收。
- 无旧审核、手动激活、赠金模型/分组限制、免费兑换码、积分、主题奖品、订阅或消费触发营销。限时赠金实际到账起生效，比例赠金与本金隔离，技术恢复不变成用户操作。
- 内部权限保持006/007 r1已核对OWASP适用控制：ASVS5.0.0 8.2.1、8.2.2、8.3.1、8.3.2。管理员操作重放前检查当前角色，兑现检查当前受益人及归属；真实已发生付款可捕获不等于禁用用户可开奖。没有新HTTP/Cookie/Session入口，不能作全站合规声明。后续每个真实入口仍需独立认证、授权、CSRF/Origin及审计验收。
- 008–010负责真实支付/付费卡密、注册/关系、签到日历接入；这里的可信claims不替代日历资格。011需处理已清退事实/未决责任与在途结算，不能重新启用后恢复旧权益；012–014需正确显示金额与来源，不能直接展示内部quota或把比例基数显示成赠金面额。
- r1未通过、r3真实MySQL失败、早期夹具失败及最终通过证据均保留，冻结标准未弱化。全项r2通过后主代理可按约定更新状态、提交本Issue，然后进入下一Issue。
