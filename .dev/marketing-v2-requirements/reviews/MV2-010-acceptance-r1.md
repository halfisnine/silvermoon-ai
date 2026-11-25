# MV2-010 整项独立验收 r1

日期：2026-11-23。审查者：`/root/mv2004_path_audit`。结论：**A01–A05 全部 PASS，本 Issue 适用共同约束 PASS，可以标记开发完成并提交。** 这是签到、连续奖励及活动内补签的服务端验收，不代表完整活动页面、退款、生产部署或最终性能验收完成。

## 验收对象与冻结校验

- 基础提交：`153cf02c3a6caec243f5ad2b6e3aa4f426424dd7`，加本次候选未提交变更。
- 最终清单：`build/dev/mv2-010-review-manifest-r1.json`，SHA-256 `22d7fc50c1e6c62f3484ff940c50af3474c40db92e989d65c1b7553761bb63a8`。
- 开始及结束逐项核对 25 个文件、24 个产物/日志及 16 段冻结标准，全部一致。记录：`build/dev/mv2-010-independent-r1-start-verification.json`、`build/dev/mv2-010-independent-r1-final-verification.json`。
- MV2-010 验收正文 SHA-256：`c3c1d91f9cfa773d127d9715391a60cf69e285c76eef860c24f093e050944a0c`；共同约束：`cd8c0a154988ce59c5976a99c4a196cf15412ff1d1f0a5bd288739dac400638d`。本轮清单的 Issue 摘要不含标题、包含标题后换行；与 009 部分清单的分隔符定义不同，不能直接据摘要差异推断正文被改。已逐字比较当前标准与 HEAD，一致，未修改冻结项。

## 逐项结果

| 验收项 | 结果 | 核验内容 |
| --- | --- | --- |
| A01 | PASS | 无需充值、当日实付、前日实付三种条件基于真实 MarketingPayment 入账事实；同日多笔在线/付费码合计实付，面值及赠金不计入门槛。固定上海日历不依赖主机时区；写入在用户/活动锁后取时。一次操作检查所有适用活动并按互斥优先级选中，纯连续奖活动未到领奖日也参与判断。旧 UserCheckin 已关闭，直接加 quota 的两个旧 helper 删除，旧随机设置不再产生本金。 |
| A02 | PASS | 每日与连续额外分别建立奖励；七天每日 1 元加第七天额外 2 元，共 9 元，第七天 3 元。一次档位全活动一次，周期模式按连续段计算，断签重新开始；不继承旧签到。仅连续奖、当天未达档也正常记日历。真实服务入账证据覆盖第十四天一次模式 16 元、循环模式 18 元及断签重计。 |
| A03 | PASS | 补签核验目标日、最近 N 天、活动创建/启用/开始/暂停及账号创建边界；当日付款资格使用首次累计达标时间与目标日可参与窗口的交集，今天付款不能补造过去资格。每用户每活动补签次数跨月不重置，开关决定是否补每日奖。连续责任独立持久记录，补签合并时不重发已经成立但尚未到账的责任，新增独立连续段不被旧段抵扣。 |
| A04 | PASS | 实际两个网关共享 PostgreSQL 与同一 Redis 逻辑库，正常同日签到、同日补签只建一个事实和日历记录；独立 OS 进程覆盖最后一次补签名额竞争、跨午夜普通/补签交叉、回复丢失与 Redis 客户端故障重试。三库行为、迁移、重复启动及日界/活动结束回归通过。数据库发奖故障时 HTTP 返回签到已记录及 pending，两个生产恢复 worker 只补一次。 |
| A05 | PASS | 日历/次数/连续责任均按 activity ID 隔离；复制活动的 created/enabled 时间阻止追领其存在之前日期，既有当日事实直接重放原结果、不挂接新活动。原活动漏签和档位不迁入副本。开始后重命名等更新被现有不可变规则拒绝，重试仍关联原 activity ID；真实进程覆盖复制、更新拒绝及旧事实重放。 |

## 独立预审发现与修复闭环

**连续段责任抵扣。** 全历史数学档位数量减永久 highwater 会吞掉新断签段的应发奖。例如周期 7 天、第 1 天奖励，1 日和 3 日各得一份，补 2 日后数学数量从 2 降为 1；如果 5 日新段仍受全局 highwater=2 限制，将错误不发。当前用 AnchorDay 把既有责任归属到其所在日期段，ordinal 仅作永久唯一编号；计算当前合并段应发量时抵扣该段已有责任，一次模式仍全活动最多一次。模型与真实进程反例验证旧合并段不重发、新独立段正常发；两段各满 7 天后补中间日也不重发，继续到 21 天仅新增第三份。责任在日历事务成立，不以赠金是否成功到账决定是否已经占据档位。

**早期阻断一：日历与提交互斥不一致。** 原草稿逐活动显示 eligible/可补日期，而提交只记录优先级赢家；GET 还只取公开活动，隐藏但启用的高优先级活动会使可见低优先级活动被误报可领。修复后共用 `selectMarketingCheckinActivities`，先纳入全部相关候选求赢家，再过滤展示；低优先级明确 suppressed，可补日期也过滤。全局 `can_checkin`/`checked_in_today` 使用实际赢家及既有当日事实，隐藏赢家不导致统一签到按钮被误禁。对应模型断言和最终三库回归通过；响应不泄露隐藏活动规则。

**早期阻断二：历史付款资格与开放窗口错位。** 原草稿分别证明“目标日曾开放”和“目标日累计付款达标”，可能让 10 点暂停、11 点才付款的用户在次日恢复后补签，尽管当日从未同时满足条件。修复后按 posted_at/id 排序确定实付首次达标时刻，再与原目标日、活动及非暂停窗口求交；次日才恢复拒绝，同日恢复后仍有有效窗口允许。前日实付条件继续取前一日整日累计。模型正反例及最终真实进程证据均通过。

上述修复后重新检查全部 A01–A05，没有只复验单个缺口。失败及阶段证据继续保留，没有调整验收标准来规避问题。

## 最终验证证据

以下父任务证据均由审查者阅读相应脚本/结果并校验清单摘要。实际数据库版本为 SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11；Redis 为 7.2.17。不声称验证了 MySQL/PostgreSQL 的最低支持版本。

1. `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`：`build/dev/mv2-010-migration-r2.log`，退出 0。三库新建、从正式 custom/main `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12` 代表基线升级、重复启动通过；保留原 checkins 记录且不生成新版签到或赠金；新增日历和连续档位复合唯一键通过实际冲突检验。详细迁移日志采用追加方式，核对本轮末尾结果。没有迁移旧营销数据。
2. `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：`build/dev/mv2-010-accounting-r2.log`，退出 0，无 FAIL/SKIP。最终模型含两个阻断修复、AnchorDay、新日历顶层状态及三库 Checkin 测试，同时重跑既有资金/活动/支付/Realtime service 生命周期。
3. `python3 .dev/marketing-v2-requirements/validation/checkin_process.py all`：`build/dev/marketing-v2-validation/checkin-process-20261123T071353.677512Z.log`。真实网关 PID 70/76，共享 PostgreSQL 17.11 和 Redis 逻辑库 9；当前时间签到/补签经实际 HTTP、真实鉴权和数据库执行。历史日界及周期场景由独立 OS worker 调用同一生产事务入口并传入可信夹具时钟，不是产品 API 接受用户伪造时间。覆盖资格交集、补签次数、互斥、复制、段合并、提交结果丢失和恢复。网关 SHA-256 `efb0b50c913fd212bb554b43592c623e1d73951deb480d655937a1aa0bf239ef`；夹具 SHA-256 `4aab95153703fbd99ff7cbb96dbbeb3b62676fc90045e309ba0581dbeb2cb58b`。
4. `python3 .dev/marketing-v2-requirements/validation/checkin_process.py controller`：三份 `checkin-controller-{sqlite,mysql,postgres}-checkin-auth-20261123T071453.943367Z.log`，每库 3 个顶层测试组通过，无 FAIL/SKIP。TASK/SECURITY fixture 指向相应实际数据库；新 HTTP 签到测试使用真实 UserAuth，原登录失效/过期/单次消费回归通过。二进制 SHA-256 `22a84d1afb1bd0ac41914074b6aa61a6fda07af681cdbf7c139d6b236100bbe9`。
5. `.dev/run go test ./controller ./model ./internal/marketing ./middleware -count=1 -timeout=300s`：`build/dev/mv2-010-packages-r1.log`，退出 0，controller 189.864s、model 21.965s、internal/marketing 0.036s、middleware 5.571s。全包结果不代替前述明确要求三库不跳过的矩阵。

审查者独立运行 `.dev/run go test ./model ./internal/marketing -run '^TestMarketingCheckin$/^sqlite$|^TestCheckin' -count=1 -timeout=180s -v`，退出 0，model 2.306s、internal/marketing 0.004s，无 FAIL/SKIP。日志为 `build/dev/mv2-010-independent-model-r1.log`，SHA-256 `1db10b54a96d9c85aff4c5f0cd4cdcc69da8bc7686ca3a7e410a356c19f0221e`。**该运行发生在最终 can_checkin/checked_in_today 字段加入之前，只是阶段独立证据；最终新增字段由只读源码审查和父任务最终三库/全包覆盖，不冒充独立最终版本或独立三库测试。**

另独立执行 `git diff --check` 通过；五条新增 API 英文来源键在七种 locale 均存在且非空。Controller 明确投影站点显示金额及有效期，不直接序列化模型，非 1:1 换算用例通过；不输出原始 quota、换算比例、付款资格证据或内部规则快照。

## 共同约束与边界

用户、活动、日期、连续档位责任和事实在数据库事务中提交，数据库复合唯一约束兜底；多实例不依赖进程内计数或 Redis 临时锁。签到成功与技术发奖失败明确区分，恢复沿原 fact/claim/decision/reward/gift/ledger 关联，不重新签到或领取。新增补签使用现有 wallet:write 权限及 Turnstile 路由，身份来自 UserAuth，调用者不能选择用户、活动或当前时间；Cookie-only 写入拒绝，实际双网关注销后旧凭据重放拒绝且资金不变。

已依据 OWASP Authentication、Session Management、Authorization、CSRF 及 ASVS 5.0 相关服务端授权要求检查适用控制；本项没有改认证协议，不宣称全站 ASVS 合规认证、生产 TLS 浏览器测试或线上验收。未引入新中间件、手动激活、人工审核发奖、模型/调用组限制、积分、主题奖品或订阅。

完整用户签到页面及个人资料跳转属于 013，届时应使用已成立连续责任展示一次档位已领取及下次奖励，不能只根据当前 streak 猜测。日历目前存在按活动/日期读取资格的查询成本，50–100 并发及实际容量由 015 实测，本报告没有提前作性能承诺。退款、后续资金展示与自动后台恢复仍按各自 Issue 验收。审查者仅写本报告及本地校验证据，未改实现、冻结标准、线上配置，未推送或部署。
