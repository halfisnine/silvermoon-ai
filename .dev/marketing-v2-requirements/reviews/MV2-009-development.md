# MV2-009 注册与邀请开发记录

2026-11-23，基于已验收提交 `0d28f89`。状态：开发中，未整项验收，未提交。

## 成功事实与资金隔离

普通注册、内置及自定义OAuth、微信及管理员创建账号复用实际用户创建事务。用户、零付费金额账户、可信未充值记录、有效邀请关系和注册成功事实同事务写入；OAuth绑定或管理员权限写入失败时全部回滚。注册时间在邮箱/邀请人锁及密码处理后由服务端取值。无效、已禁用、已删除邀请人沿用无效邀请码不阻断普通注册的行为，不建立邀请关系，不发邀请奖励。

旧QuotaForNewUser/Invitee/Inviter不再发放；aff_count只记录已提交有效邀请人数，aff_quota不再奖励或转入付费余额。用户更新不能后补/改绑邀请人；后置侧栏初始化只更新设置，不用旧结构覆盖余额、重置首充证明或重放旧奖励。

完整注册事务提交后，实际入口立即执行007生产奖励服务；失败只记录用户编号、保留持久责任，不将已创建账号报告成营销发放失败，不泄露密码/验证凭据。注册赠金归新用户，邀请赠金归邀请人。好友累计一次/循环、实付比例及时间范围使用007已确认规则；008的在线与卡密付款统一产生事实。

## 安全控制与验证计划

已阅读完整计费规则，以及OWASP Authentication、Session Management、Authorization、CSRF相关内容与OAuth2 Cheat Sheet（缓存`build/dev/mv2-009-owasp/OAuth2.txt`）。适用控制是服务端可信注册身份与字段白名单、密码处理与邮箱验证、OAuth state/绑定原子性/重放、会话认证与管理员权限、失败不发奖、日志不记录认证秘密。保留既有认证协议；不声称本任务重新认证全站ASVS合规。

模型回归扩展现有TestMarketingActivity，包含真实注册事务、邀请人状态锁边界、两个连接并发注册、回滚、首充证明保持及不可重绑。Controller集中于现有auth_flow_test，覆盖真实处理函数注册、两类OAuth、失败回滚、即时奖励失败后恢复。已有管理员创建/认证测试只补真实营销表schema，不削弱断言。真实双网关验证使用独立PG与Redis DB10，外部身份/支付可模拟，账户/事实/账本不能模拟。

待三库、实际进程、相关全包回归及独立A01–A04整项验收通过后，才能标记完成及commit。没有修改冻结验收项，没有旧营销数据迁移，没有推送/部署。

## 中间验证与早期独立阻断

初轮controller集中回归通过（`mv2-009-controller-r1.log`，15.504s）；`accounting_matrix.py all` r1真实三库通过，无FAIL/SKIP。实际双网关中间证据`registration-process-20261123T063159.634238Z.log`及增强后的`063513.447816Z.log`通过；controller三库中间证据`registration-controller-{sqlite,mysql,postgres}-registration-auth-20261123T063313.761807Z.log`通过。这些是修复下面缺口之前的阶段证据，不能用于提前宣称009完成。

独立早期审查者 `/root/mv2004_path_audit` 发现内置OAuth/微信的预检查竞态：A先查身份不存在，B注册提交，A再生成不同备用用户名并创建；空邮箱不提供唯一性保护，身份字段非unique，两个站内账号均能获礼及首充资格。自定义OAuth已有唯一绑定索引，不属于同一缺口。需复用已有ExternalIdentityClaim持久唯一归属，覆盖注册、正常绑定、解绑和GitHub旧ID迁移的同一生命周期；不改变认证产品、不禁用正常绑定、不引入新中间件。修复后重跑三库、真实两网关与整项独立验收。

## 身份归属修复与第二轮候选

复用原有`ExternalIdentityClaim`双唯一约束；五种builtin按绑定列名区分命名空间，claim主体用原始身份字节SHA-256，users保存原始身份、不截断、不改大小写或空白。注册先创建未暴露身份的临时用户行，再同事务认领及核对基线owner，最后写身份、可信账户和注册事实。失败全部回滚。资料更新不能写绑定列，专用绑定/替换/解绑/GitHub旧ID迁移共用归属约束；Telegram原规则保持。

初始化扩展回填既有builtin归属，逐用户锁后重读当前身份；保留软删除身份。不生成注册事实、钱包或奖励，不属于旧营销数据迁移。没有新增表/列。

- `migration_matrix.py all` 最终候选r1退出0，`build/dev/mv2-009-migration-r1.log`：SQLite3.50.4/MySQL8.4.13/PostgreSQL17.11新库和正式custom/main ca5af4c升级、两次启动通过。扩展代表基线用户五种身份及软删除身份，原始字段完整不变，ownership唯一且幂等，营销账户/事实/赠金仍0。
- controller定向r2退出0，`build/dev/mv2-009-controller-r2.log`，27.727s；包含确定性A查身份不存在后暂停、B真实创建提交、A继续生成不同fallback用户名的回归，A必须ErrExternalIdentityAlreadyClaimed，只有一用户、一fact、一注册gift。另含原OAuth、绑定和管理员创建保护。
- 身份修复前全包controller/model回归`mv2-009-package-regressions-r1.log`通过（190.376s/16.799s），明确仅作中间证据；修复后最终全包和矩阵正在重跑。

冻结标准未改；早期独立阻断与修复记录保留，待全项正式验收。

## 最终矩阵证据（身份修复后）

- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：r2退出0，`build/dev/mv2-009-accounting-r2.log`。真实SQLite3.50.4/MySQL8.4.13/PostgreSQL17.11，注册、身份归属、邀请/资金/奖池/活动及service生命周期全部通过，无FAIL/SKIP。
- `python3 .dev/marketing-v2-requirements/validation/registration_process.py all`：退出0，`build/dev/marketing-v2-validation/registration-process-20261123T064236.616040Z.log`。两个真实网关共享PG17.11、Redis7.2.17/DB10，密码注册/失败、真实OAuth state与callback、微信同身份竞争、实际卡密及易支付、分好友累计及实付比例、活动前关系排除、历史AffQuota拒绝转本金、原来源链均通过。外部身份/支付被模拟，本站账本不模拟。真实数据库赠金写失败保留责任；两个独立OS worker并发处理，其中一个提交后丢回复，再双进程重放不重发。它验证生产service恢复入口，不表示015启动自动扫描已实现。
- `python3 .dev/marketing-v2-requirements/validation/registration_process.py controller`：退出0，三份`registration-controller-{sqlite,mysql,postgres}-registration-auth-20261123T064314.598818Z.log`，每库16个顶层、0SKIP。TASK与SECURITY夹具均明确指向实际引擎，覆盖新注册/竞态及原授权、OAuth、登录验证契约。

最终全包回归仍在运行，之后生成代码/脚本/日志/二进制绑定manifest，等待独立A01–A04正式验收。没有提前标记完成。

### 最终全包回归与候选绑定

`.dev/run go test ./controller ./model -count=1 -timeout=300s` 修后r2退出0，`build/dev/mv2-009-package-regressions-r2.log`，controller191.575s、model17.156s。独立定向SQLite检查已通过4.341s，无SKIP（`mv2-009-independent-model-r1.log`），正式整项结论仍由独立审查者给出。最终绑定为`build/dev/mv2-009-review-manifest-r1.json`；15Issue冻结验收正文及共同约束逐字保持HEAD。

## 独立验收完成

2026-11-23：独立审查者 `/root/mv2004_path_audit` 完成整项 A01–A04 与共同约束检查，全部 PASS；正式报告 `MV2-009-acceptance-r1.md`。开始与结束校验24文件、30产物及16冻结段均一致。更新Issue状态并本地提交，不推送或部署。
