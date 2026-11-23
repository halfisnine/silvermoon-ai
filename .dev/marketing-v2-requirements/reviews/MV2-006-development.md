# MV2-006 开发记录

日期：2026-11-23。基于已验收提交 `bd2ca86`。状态：实现与验证中，尚未独立整项验收、未提交。

## 实现范围

新增独立奖池、奖项、机会批次、发行结果、开奖结果、组合奖责任和管理操作记录。机会按批次存储，不逐次制造记录。活动通过稳定来源授予机会，奖池独立决定奖品；转盘与盲盒共用服务端安全随机权重算法。库存耗尽退出实际候选，空奖是正常结果，技术错误不伪装未中奖。

发行总数有限且只增不减；不限量小奖可用，零权重/空奖不计保证供给。追加核对未发行容量和有效未用机会；已开奖责任已扣库存，不能重复计算。需要3次只剩2次时整项保存名额不足；追加不补历史不足，过期/空奖/清零不恢复发行数。开始后或已有机会承诺的核心配置不可改，支持补原库存、追加、暂停和有效机会延期。

开奖结果、机会扣减、库存扣减和全部组合责任同事务保存，再逐份幂等到账。即时服务编排使用服务端时间；部分失败返回仍待处理的真实结果与错误，不重抽，不伪装全部到账。每份赠金期限从实际到账算，重复重试不改原期限。只使用新版独立赠金账，不加入付费资金。

供给/去重/状态读取使用当前锁定读取，避免MySQL已有快照遗漏新责任。普通兑付锁受益人，不占奖池共享热点锁。退款清零按用户与奖池稳定顺序锁定；罕见清零接受全池短锁，避免延期与清零死锁。SQLite只针对明确BUSY/LOCKED作有界整事务重试，业务错误和不明确提交错误不重试。所有事实在主数据库，不依赖Redis临时锁。

## 权限与公开接口边界

配置/追加/清零校验当前管理员角色；用户开奖、兑现、查询校验当前有效用户与记录归属，重放同样重新校验。管理奖池不隐式创建管理员营销钱包。记录只保存业务来源和操作信息，不保存认证凭据。

已核对 OWASP Authentication、Session Management、Authorization Cheat Sheets，以及稳定 ASVS 5.0.0 V8，重点8.2.1功能权限、8.2.2归属、8.3.1可信服务层、8.3.2撤权及时生效。官方资料及URL/hash保存在 `build/dev/mv2-006-owasp/`。本Issue是内部持久服务，无新HTTP路由、Cookie写入口或会话变化；正式管理/用户鉴权及CSRF在012/013接入验收，不能据此宣称全站ASVS合规。

## 验证记录

- 核心作者执行 `.dev/run go test ./model -run '^TestMarketingLottery/sqlite$' -count=1 -timeout=180s -v` 通过，包含组合中断、原结果恢复、权限撤销、钱包上限、真实SQLite锁竞争与取消。查询补充用例再次通过。
- `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` 退出0，日志 `build/dev/mv2-006-migration.log`。真实 SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11，新库及正式custom/main基线ca5af4c升级、重复启动、基础数据和约束保留。不是旧营销数据迁移，不声称全部最低版本均实测。
- 首轮独立OS进程夹具及增强后的完整矩阵通过，`build/dev/marketing-v2-validation/lottery-process-20261123T013106.335675Z.log`。共享PostgreSQL17.11与Redis7.2.17/DB13，测试实际断开Redis客户端、最后名额/库存、追加、3对2、崩溃恢复、部分到账、SIGKILL锁等待、不同管理员延期/清零竞争。不是HTTP身份认证验证。新增即时服务编排后需最终重建复验。
- 三库accounting矩阵与最终服务编排验证进行中，结果后续追加。

未修改冻结验收项，未push、PR、发布或部署。独立全项验收通过前不能标记完成。

## 最终实现验证，待独立整项验收

- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` 退出0，`build/dev/mv2-006-accounting-r1.log`；model与service真实三库全部通过，无失败或跳过。包含MySQL已有事务快照下的新发行责任、重放、已兑现查询，及原资金/Realtime回归。
- `.dev/run go test ./model ./service -count=1 -timeout=300s` 退出0，两包全部通过；`build/dev/mv2-006-package-regressions.log`。
- `python3 .dev/marketing-v2-requirements/validation/lottery_process.py all` 最终退出0，`build/dev/marketing-v2-validation/lottery-process-20261123T013411.564446Z.log`；保留全部旧场景，并新增真实service调用、同请求并发立即到账、丢响应重试、三入口错误归属/禁用拒绝。隔离PG CHECK约束使第二份赠金实际写入失败，返回pending与error；解除后两进程恢复仍为原结果，仅两份gift及两条gift_credit，不改变第一份到账时间、不增加本金。

最终证据绑定 `build/dev/mv2-006-review-manifest-r1.json`。仍未更新Issue状态、未提交，等待A01–A07和共同约束全项独立验收。

## 独立整项 r1：不通过

独立代理 `mv2004_path_audit` 报告A01/A02/A03/A06/A07通过，A04/A05不通过。F1：独立奖池暂停时，发行API错误拒绝仍有效来源活动赠送机会。暂停应阻止开奖，不能额外改变来源活动的奖励资格。独立真实SQLite反例保存在 `build/dev/mv2-006-pause-probe/`，实际返回ErrMarketingAccess。

这属于实现增加未批准限制，修正不需要修改冻结标准。保留原失败报告和反例；修复后需全项r2复验，不只确认F1。Issue继续未完成、未提交。

## F1修复及整项r2验证准备

发行仅按有效截止与有限名额判断，暂停仍只阻止Draw。集中回归增加暂停发行、容量不足、开奖不扣、恢复可抽、恰到期拒绝及不复活。没有改变持久schema或冻结标准。

- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` 退出0，`build/dev/mv2-006-accounting-r2.log`，model/service真实三库全部通过，无跳过。
- `python3 .dev/marketing-v2-requirements/validation/lottery_process.py all` 退出0，最终 `build/dev/marketing-v2-validation/lottery-process-20261123T014614.220067Z.log`。原全量场景及暂停池两独立用户竞争最后发行名额通过；一个issued一个shortage，暂停不扣、恢复可抽、自然到期不复活。
- 原独立overlay未改断言直接重跑，`build/dev/mv2-006-pause-probe/result-r2.log`仍退出1。暂停发行断言本身通过，但该探针新增发行消耗原共享测试池容量，导致原后续需要2次的断言得到shortage。已请独立审查者隔离探针夹具，保留真正暂停及原有断言，不能将该失败日志当作通过。

正式整项r2待独立审查。此前迁移和包级回归仍对应未变化的schema/其他实现，三库业务和真实服务进程已按最新核心完整复跑。未提交、未更新完成状态。

## 整项r2通过

2026-11-23独立代理完整复验A01–A07和共同约束全部通过，无剩余阻断。独立model/service全包回归再次通过；暂停探针隔离专用用户和奖池后通过，真正暂停及原全部断言均保留。原样复跑、仅隔离池时用户清零计数污染的失败日志仍保留，详见独立r2报告。正式源码、三库及多进程证据均匹配最终清单。按用户流程更新完成状态并保存本地commit，随后进入007；不推送或部署。
