# MV2-007 开发记录

日期：2026-11-23。基于已验收提交 `8203011`。状态：开发中，尚未整项独立验收或提交。

## 实现方向

四类活动直接配置奖励，活动、事实、资格决议和实际到账分开。事实在成功入账事务内保存当时已存在且适用的规则与池容量依据；同用户按持久事实顺序决议，不能由乱序worker改变首充、首次达标或累计档位。邀请关系保存可信注册时间，好友分别累计。

互斥在供给判断之前按优先级和活动ID选定，非互斥叠加；选中、抑制与资格进度均保存。资格机会概率使用持久安全随机种子和档号/项号一次决定，种子不输出给用户。处理工作分批不构成用户奖励上限，也不把未处理资格宣称为已获机会。

机会发行与组合责任同一事务保存。事实捕获时的奖池Capacity上界避免延迟旧资格消费后来追加的名额；已记录shortage不回填，新资格可用新名额；已有Issue同key优先回原结果。此机制不预留支付名额，不保证不同用户按付款先后占奖池名额。已到期奖池的尚未分配机会记录真实pool_expired，不伪造名额不足或概率未中、不回拨时间。

受益人账号禁用不改变外部已收款事实。内部机会发行可以记录仍成立的承诺并占有限名额，但禁用用户不能开奖；兑付赠金检查当前受益人权限。清退旧事实/未完成责任及在途门禁由011整体实现，不能恢复账号后补出已清退的旧权益。

## 比例金额

固定赠金保留原MarketingMoneySnapshot。普通充值比例以真实到账付费内部金额为基数，邀请比例以好友实付人民币和冻结CNY转换为基数。保存PaymentID、实付、到账、比例、转换依据和计算结果，严格一次舍入；不反算12位显示金额再转换，避免配置极端换算率时误差。

金额向零舍入时记录zero_amount，不制造零额赠金或本金。overflow/非法配置返回错误并保留责任。直接Grant原来源摘要和Conversion格式不变；比例赠金Conversion明确保存计算证据，不把基数当成赠金面额。所有资金仍使用独立赠金账。

## 编排和权限边界

内部服务按最早未决事实处理有限工作批次，然后逐项兑现。某一项真实失败仍尝试其他独立项；返回真实Pending及错误，不把部分到账展示为全部成功。内部队列不暴露随机种子或底层金额字段，用户HTTP查询及管理授权在012/013接入；真实支付、注册、签到入口分别由008–010接入。

## 已运行验证

- `.dev/run go test ./model -run '^TestMarketingMoneyConversion$|^TestMarketingLottery/sqlite$|^TestMarketingPersistence/sqlite$' -count=1 -timeout=180s` 退出0，`build/dev/mv2-007-amount-focused-r1.log`。包括实付/到账分离、一次舍入、零结果、极端转换、原gift/lottery行为；不是完整007验收。
- 初始七表schema运行 `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` 退出0，`build/dev/mv2-007-migration-r1.log`。SQLite3.50.4/MySQL8.4.13/PostgreSQL17.11新库、正式custom/main基线升级和重复启动通过。若开发中schema继续变化必须复跑，不能沿用此结果宣称最终兼容。

尚需集中三库、真实多进程/服务编排、独立全项A01–A07验收。未改冻结验收标准，未提交/推送/发布/部署。

## 开发中补齐及早期独立复核

- 补齐草稿/启用/公开：EnabledAt为0时是草稿；首次启用固定真实生效时间，不因重复调用重置。Published仅控制用户中心可见性，不控制已经启用的资格。草稿可以调整计划，启用并开始后核心不可原改；草稿期间已捕获事实不因随后启用补发。
- 独立代理早期只读检查发现服务每次从头最多1024条可能使失败前缀永久挡住后项。新增每source用户一条技术游标，在共享主库轮转分页；只推进调度位置，不把pending改成成功或丢弃，进程退出后仍能重访。调用内避免重复尝试同项。不是新的奖励上限、审批或用户操作。
- 同一早期检查指出逐份到账时读取全部pending存在平方级开销，改为当前读取最多一项判断是否完成。
- 初版真实进程矩阵 `activity-process-20261123T020929.706549Z.log`通过五种充值、幂等、互斥、概率、历史暂停/复制、真实组合失败恢复、邀请比例、签到可信claims、草稿/启用/公开与即时service。这个中间版本尚无持久游标，不能当最终007通过。

新增启用字段及游标表后正在重跑最终迁移和真实进程边界；集中测试继续补旧事务快照、金额真实到账与分页恢复。独立早期检查不替代完成后A01–A07全项验收。

## 最终验证中的夹具问题（保留失败证据）

- 初次完整三库矩阵 `mv2-007-accounting-r1.log`退出1：Activity集中测试的清理使用同表子查询删除，在MySQL报1093。SQLite/PostgreSQL该组及其余model回归通过。已改为直接按测试名称前缀清理同表，未改变业务断言或生产代码，完整矩阵重跑中。
- `.dev/run go test ./model ./service ./internal/marketing -count=1 -timeout=300s` 中model/internal通过，service失败；`mv2-007-package-regressions-r1.log`保存原因。原service夹具只手工创建旧资金表，新的真实付款事实捕获需要活动/邀请等表，SQLite报缺表。已补齐测试数据库schema，不在业务中忽略缺表或取消事实捕获；service全包补跑中。
- 最终真实OS矩阵已通过：`activity-process-20261123T021207.622827Z.log`。新增真实活动生成1024份旧失败责任+第1025份正常受益赠金，两个service调用后正常项到账，旧项仍pending；恢复权限后旧项全部原路到账，正常项不重复。不是随机压力测试或伪造账本。
- 新schema迁移r2通过：`build/dev/mv2-007-migration-r2.log`，包括草稿启用/公开字段和持久调度游标表；后续只修测试夹具，schema未变。

上述失败不是通过，最终三库和service补跑结果将在后续追加。仍未进入正式独立全项验收。

## 供正式整项独立验收的最终证据

- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` 最终退出0，`build/dev/mv2-007-accounting-r2.log`。model（含Activity、MoneyConversion、Lottery、Persistence）及service资金生命周期真实SQLite/MySQL/PostgreSQL全部通过，无FAIL/SKIP。
- `.dev/run go test ./service -count=1 -timeout=300s` 退出0，`build/dev/mv2-007-service-regressions-r2.log`；修复仅为补测试schema。model/internal全包已在前次package-regressions-r1通过，之后仅修MySQL测试cleanup，已由最终三库覆盖。
- 真实进程最终日志 `activity-process-20261123T021207.622827Z.log` 与最终核心/服务hash一致，十类场景全部通过。PG17.11、Redis7.2.17/DB12，只有真实模型/service事务，不替代008–010实际支付/注册/日历入口验收。
- 最新八表/启用字段schema仍对应migration-r2，未再次变动。基线为正式custom/main的ca5af4c，不迁移旧营销数据。
- 当前内部授权延续006核对的OWASP ASVS5.0.0功能权限/对象归属/可信服务层/撤权重放边界；付费事实允许保存已发生收款，不等于禁用账号可主动开奖或兑现。没有新增HTTP/Session/Cookie机制，相关公开入口在后续Issue按适用安全规则整体验收。

最终代码、脚本、二进制及日志绑定 `build/dev/mv2-007-review-manifest-r1.json`。15个Issue冻结验收正文及共同约束仍逐字保持HEAD原值；状态继续未完成，待独立全项A01–A07复核后才能提交。

## 独立整项验收 r1 与补验计划

独立审查者 `mv2004_path_audit` 完成 A01–A07 全项检查，报告 `MV2-007-acceptance-r1.md`。A01/A02/A03/A04/A06/A07 在内部活动引擎范围通过；A05 因缺 SQLite/MySQL 活动决议和发奖并发证据未通过。既有三库资金并发不能替代活动并发。没有确认其他生产实现阻断，状态仍未完成。

本次修复集中在既有活动测试：使用真实数据库的两个独立事务，双方完成数据库读取后再共同放行业务操作，核对重复规划、概率结果、互斥赢家、累计进度、同奖兑现与跨用户最后名额竞争。SQLite 使用生产整事务重试；不模拟账本、不把 goroutine 声称为双实例。已有 PostgreSQL 实际 OS 进程证据继续保留。补验通过后重新发起 A01–A07 全项验收，不修改冻结标准。

### 三库补测发现的真实 MySQL 死锁

`accounting_matrix.py all` 的 r3 退出1，保留 `build/dev/mv2-007-accounting-r3.log`。两个不同用户并发兑现赠金时，MySQL InnoDB REPEATABLE READ 的赠金余额当前读持有同一个空索引区间，随后双方 INSERT 相互等待；服务端以1213整事务回滚其中一个。`build/dev/mv2-007-mysql-deadlock-r3.log` 保存真实 InnoDB 锁诊断，SQLite/PostgreSQL 同场景通过。此失败属于生产事务处理缺口，不是测试通过。

修复保留当前读、钱包总额限制和真实并发断言，仅将公共整事务封装的有限重试扩展到 MySQL 1213（服务端明确整事务回滚）。不重试1205锁等待超时、连接错误、业务错误或未知提交结果；最多4次且支持取消。原SQLite整事务重试保留。新增集中回归确认其他错误只尝试一次，已写赠金及流水整事务回滚；不靠重复执行最后一条写入修复。最终三库及真实多进程重新运行后再提交独立全项r2。

### r2 整项复验候选证据

1213修复后 `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` 退出0，日志 `build/dev/mv2-007-accounting-r4.log`：真实SQLite3.50.4/MySQL8.4.13/PostgreSQL17.11活动、赠金、奖池、资金与service生命周期全部通过，无FAIL/SKIP；新增重叠事务和最后名额竞争均通过。

`python3 .dev/marketing-v2-requirements/validation/activity_process.py all` 重建后退出0，汇总 `build/dev/mv2-007-process-r2.log`，详细证据 `build/dev/marketing-v2-validation/activity-process-20261123T023239.453289Z.log`。真实OS进程共享PG17.11/Redis7.2.17 DB12，全部业务恢复场景通过；不替代后续真实HTTP入口与日历规则验收。表结构未变化，最终迁移r2证据继续有效。

候选绑定 `build/dev/mv2-007-review-manifest-r2.json`。r1未通过和r3实际MySQL失败记录均保留。源码与证据从此冻结，等待独立A01–A07整项r2复验；状态暂不改为完成。
