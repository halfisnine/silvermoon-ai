# MV2-009 整项独立验收 r1

日期：2026-11-23。审查者：`/root/mv2004_path_audit`。结论：**A01–A04 全部 PASS，本 Issue 适用共同约束 PASS，可以标记开发完成并提交。** 本报告仅覆盖注册有礼与邀请奖励统一，不代表尚未完成的签到、退款、用户界面、整体性能或上线验收通过。

## 验收对象与冻结校验

- 基础提交：`0d28f890fd206c3bd2e38244a3a31eabd6bcd313`，加本次未提交候选变更。
- 最终清单：`build/dev/mv2-009-review-manifest-r1.json`，SHA-256 `35f90f78e9fa4fc9b724e7b973863d5698ace7c1b6a156c95456b4ec9ac65f97`。
- 独立核对清单的 24 个文件、30 个产物/日志及 16 段冻结标准；开始与结束均逐项比对 SHA-256，确认一致。校验记录为 `build/dev/mv2-009-independent-r1-start-verification.json` 与 `build/dev/mv2-009-independent-r1-final-verification.json`。
- MV2-009 验收正文 SHA-256：`958865e0e14e1486fd7528e42c45b74edc4fe680658cb287c50315076e8fdc1e`；共同约束：`cd8c0a154988ce59c5976a99c4a196cf15412ff1d1f0a5bd288739dac400638d`。正文及共同约束与 HEAD 一致，没有修改或弱化验收项。

## 逐项结果

| 验收项 | 结果 | 业务核验与证据 |
| --- | --- | --- |
| A01 | PASS | 密码注册、五种 builtin 身份、custom OAuth、微信及管理员创建都由真实用户创建事务建立零本金账户与注册成功事实；绑定或管理员权限写入失败整体回滚。实际入口提交后调用生产奖励服务。注册赠金归新用户、邀请赠金归邀请人。旧 QuotaForNewUser/Invitee/Inviter 即使配置非零也不发放；AffQuota 转付费在模型层拒绝。三库模型/Controller、真实双网关验证成功与失败路径、即时到账及来源分离。 |
| A02 | PASS | 注册邀请、每好友累计一次、每好友循环累计、好友实际人民币付款比例共用 007 规则；进度键包含活动、受益人、好友。邀请关系在成功注册事务确定，事实保存关系快照；活动前关系和活动外付款不参与。双网关经真实在线支付与付费卡密验证两位好友不混算、累计一次/循环次数及余数、实付金额与到账面值不同仍按实付比例。已有用户登录不变更邀请人，普通资料更新排除 inviter_id，不开放注册后补绑。 |
| A03 | PASS | 注册事实以用户唯一来源键幂等，邀请关系每好友唯一，奖励沿 fact → decision → reward → gift → ledger 关联。重复消费事实、两真实进程恢复、提交结果丢失后的重试均不增发。修复后 builtin identity 由数据库唯一 ownership 保证，不会因两个账号各有独立事实而绕过幂等。账户首充历史与金额分开保存，后置初始化不覆盖本金或首充记录；余额清零不能把既有首充证明变回未充值。完整人工退款业务仍由 011 验收。 |
| A04 | PASS | 按 OWASP Authentication、Session Management、Authorization、CSRF、OAuth2 指南检查涉及入口；保留密码/邮箱验证、注册开关、OAuth state/会话绑定与单次消费、权限与二次验证失败保护。最终三库 Controller 每库 16 个顶层测试组通过；真实 PostgreSQL/共享 Redis 双网关、失败恢复及三库升级/重复启动通过。未将模拟外部身份当作模拟账本或认证成功事实。 |

## 早期阻断及闭环

独立早期审查发现内置 OAuth/微信身份预检查存在竞态：请求 A 先查身份不存在，请求 B 注册提交，A 再选择不同 fallback 用户名；空邮箱不能形成额外唯一约束，原身份列又只有普通索引，可创建两个相同供应商身份的账号，各发注册赠金并各有首充资格。不同合法 OAuth state 不能防止此问题。此前阶段测试成功不能代表本项通过。

最终候选复用已有 ExternalIdentityClaim 的供应商身份/用户槽位双唯一约束。五种 builtin namespace 与 Telegram 分离，claim 使用原始身份字节的 SHA-256；users 仍原样保存供应商身份，未截断、归一化大小写或去空白。注册创建暂不暴露身份的用户行后，在同事务认领、核对既有及软删除 owner、写入身份，再建立账户/邀请/事实；失败全部回滚。正常绑定、替换、解绑及 GitHub 旧标识迁移走同一 ownership 边界，普通资料写入排除这些列，Telegram 原流程保持。

确定性 `stale_builtin_identity_precheck_cannot_register_twice` 复现 A 预检查后暂停 → B 真实提交 → A 使用不同 fallback 的次序，最终 A 被拒绝，只保留一个用户、事实、注册赠金。三库模型覆盖所有五种 builtin 的竞争、回滚、基线 owner、长身份、重复回填、解绑重绑、替换冲突和 GitHub 迁移；双网关微信用不同有效 code 指向同一身份竞争，最终仅一个用户/事实/赠金，重试也不重复。

初始化仅补齐当前正式基线账号的身份 ownership，不制造注册事实、钱包或奖励；这不是旧营销数据迁移。软删除身份继续保留，硬删除沿用全身份释放。新建与从 custom/main 基线升级的重复启动验证保护了这一约束。

## 最终证据与实际执行

以下父任务最终证据由审查者读取、核对脚本和二进制摘要，并校验结果。修复前 r1 日志保留但不代替修复后验证。

1. `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：`build/dev/mv2-009-accounting-r2.log`，真实 SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11 全部通过，无 FAIL/SKIP，包含注册、身份竞争及既有资金/活动回归。
2. `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`：`build/dev/mv2-009-migration-r1.log` 与清单的各库 candidate 日志；新库及正式 custom/main `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12` 代表基线升级、重复启动通过。五种既有身份与软删除身份保持原值、ownership 唯一且幂等，营销账户/注册事实/赠金没有被伪造；历史数据及原唯一约束保留。详细迁移日志是追加式，核对本轮末尾记录。没有声称验证 MySQL/PostgreSQL 最低支持版本。
3. `python3 .dev/marketing-v2-requirements/validation/registration_process.py all`：`registration-process-20261123T064236.616040Z.log`，两个实际网关 PID 12/18，共享 PostgreSQL 17.11 与 Redis 7.2.17 的逻辑库 10；实际注册、OAuth state/callback、微信、邀请充值及真实账本。恢复 worker 为独立进程，通过真实注册造成赠金写入失败后重放生产执行入口，证明持久责任和只发一次；不宣称启动自动扫描或生产供应商联调完成。网关 SHA-256 `546d8b39422587f74463c7b2bde66d7b775a4cd6a6538882638c4432f42482c4`。
4. `python3 .dev/marketing-v2-requirements/validation/registration_process.py controller`：三份 `registration-controller-{sqlite,mysql,postgres}-registration-auth-20261123T064314.598818Z.log`，每库 16 顶层组，全部通过，无 SKIP；TASK/SECURITY fixture 都使用对应实际数据库及独立日志库。二进制 SHA-256 `a7360f4f1fd96dc7668f6b691498d84e3f4f076bad219a740094617fea44bc99`。
5. `.dev/run go test ./controller ./model -count=1 -timeout=300s`：`build/dev/mv2-009-package-regressions-r2.log`，controller 191.575s、model 17.156s，退出 0。原有 fixture 变更仅补营销/identity schema 与必要清理，不削弱旧认证、权限或资金断言。

审查者独立执行：

- `.dev/run go test ./model -run '^TestMarketingActivity$/^sqlite$|^TestExternalIdentity|^TestClearTelegram|^TestInitializeExternalIdentity|^TestUpdateUserBind' -count=1 -timeout=180s -v`：退出 0，4.341s，无 FAIL/SKIP；`build/dev/mv2-009-independent-model-r1.log`，SHA-256 `cbf562170b5863a1c8d892518b74f5b617766d6c9f8d80b259ece4c8038fd223`。该命令明确只验证 SQLite，不冒充独立三库运行；三库采用前述最终矩阵。
- `git diff --check`：退出 0。
- 新增邀请转付费拒绝文案在 en/zh/zh-TW/fr/ja/ru/vi 均存在且非空，保持接口英文来源键；没有新增营销页面或底层 quota 展示。
- 开始与结束重新核对 manifest 全部文件/产物及冻结验收摘要，结果一致。

## 共同约束与范围

注册成功、关系与事实的原子性，以及用户身份、奖励归属、金额隔离和重试幂等由共享数据库约束与事务保证，不依赖进程内状态或 Redis 临时锁。没有新中间件，没有照搬旧营销的待激活、人工审核发奖、模型/分组限制、赠送兑换码、主题奖励、积分或订阅营销。

本期未新增登录协议或受保护路由。既有 OAuth/绑定操作仍保留服务端会话及状态校验，旧凭据/失效授权失败回归通过；实际双网关走现有认证入口。OWASP 参考涵盖本次适用控制，不宣称全站 ASVS 5.0 合规认证、生产 TLS 浏览器验收或供应商生产联调完成。新增错误日志只包含用户编号，不记录密码/验证材料；测试网关日志遮蔽可复用签名类内容。

尚未完成的签到 010、退款清零 011、管理/用户 UI 012–014、自动后台恢复与 50–100 并发整体交付 015 不被本结论提前标为通过。审查者未修改实现、冻结标准、线上配置，也未推送或部署。
