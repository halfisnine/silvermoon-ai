# MV2-011 独立整项验收 r1

2026-11-23。独立验收代理：`mv2004_path_audit`。结论：**A01–A06 与本 Issue 适用共同约束全部通过**，没有未解决的阻塞项。可以更新 Issue 状态并提交本次代码；本结论不是整个营销系统或线上发布验收。

## 验收对象与证据绑定

- 工作分支：`codex/marketing-v2`；验收前 HEAD：`b71c4b74d9605007a84b625f6e771e0a6b18a503`。
- 正式 New API 基线：`custom/main` 的 `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`。旧营销没有上线，本次不迁移或兼容旧营销/预览数据。来源核实仅处理正式基线原有数据。
- 最终清单：`build/dev/mv2-011-review-manifest-r1.json`，SHA256：`a6b0f78d1da884de8aad99e57dbcab9f64f5800bbb608d42a25936c22830b557`。
- 独立重新计算清单内 **54 个源码/脚本/说明文件、30 个产物/日志**的摘要，均匹配；清单覆盖当时全部 tracked diff 与 untracked 文件，没有漏绑生产变更。
- 独立核对 **15 个 Issue 验收正文和共同约束**，均与 HEAD 逐字一致，且匹配清单摘要。抽取保留标题后的空行和下一标题前的空行；不是通过修剪文本后比较掩盖变更。
- 本报告是验收产生的新增证据，不修改生产代码、测试或冻结验收条款。报告写成后再次校验清单及冻结条款。

## 逐项结果

| 验收项 | 结果 | 实现与可核对证据 |
| --- | --- | --- |
| A01 人工退款、禁用全 SK、普通禁用不清零 | 通过 | 复用原账号禁用/敏感操作认证；无新增 HTTP 退款工单或一键退款产品。真实双网关日志 `REFUND_DISABLE_OK` 覆盖两把 SK、暖 Redis、管理员验证后禁用、新调用被拒和原在途来源结算；`REFUND_LEGACY_HTTP_GUARDS_OK` 核对旧直接改钱/订阅购买/邀请转现金入口封闭。模型与控制器验证普通禁用不清退资产。 |
| A02 在途结清后清退、尾差、历史保留 | 通过 | `marketingAssertSettled` 同时检查未知费用、资金占用、实时/任务状态及软删除令牌的残留占用；任何未知终价阻止清退。`ClearMarketingAssets` 在事务内清退剩余付费、赠金、机会、待派奖和本人的累计余数，保留历史/首充/已领取事实。真实 `REFUND_CLEAR_OK`、`REFUND_ROUNDING_OK`、`REFUND_ZERO_AND_SUBSCRIPTION_OK` 覆盖双进程幂等、重启、恢复账号不恢复权益，以及 1.809 元退 1.81 元、差额 +0.001 元、赠金折现为零。 |
| A03 延迟旧权益不复活、新付款可获奖 | 通过 | 清退前有界准备旧资格，最终事务使用数据库事实序列边界及锁定读取；活动事实/发奖均检查受益人清退边界。`REFUND_REWARD_BOUNDARY_OK` 验证自身与好友迟到事实、部分完成的机会奖励、已发行名额不回收、新付款正常获奖、清退操作重放不清新钱。`REFUND_SEQUENCE_BARRIER_OK` 的 before/after 两种先后顺序使用独立支付进程与实际清退 CLI；前者新事实 16 大于边界 15 可得赠金，后者先发现新旧事实竞争并拒绝准备不完整的清退，再重试得到事实 17 小于边界 18、旧赠金为零。 |
| A04 正式基线来源盘点与切换 | 通过 | `GetMarketingMaintenanceInventory` 只读盘点余额、卡密、AffQuota、任务/订阅等；未核实余额不会自动成为付费本金。来源核实分别记付费和赠金、保留原来源/转换证据，不伪造充值收据或首充资格。Setup 初始 root 赠送不被标成实付。旧异步任务的净钱包扣款、用户用量、SK 扣款及渠道用量分别核实，修复旧流程部分返还导致重复退款的风险。三库正式基线升级和新建启动检查通过；`maintenance-runbook.md` 明确全实例停写、盘点、未知费用处理、切换和恢复整套数据库备份的回退限制。 |
| A05 双实例、三库升级与安全 | 通过 | 真实两个网关进程共享 PostgreSQL 和 Redis DB8，另有两个独立维护/支付进程竞争；不是同进程 goroutine 模拟。最终控制器三库每库 14 个顶层/67 个含子测试，零失败零跳过，包含敏感验证、撤权、越权、重放、审计失败和独立日志库。三库迁移共 18 次真实启动验证新建、正式基线升级和重复迁移；完整五包回归全通过。安全边界见下文。 |
| A06 受控维护、可信费用和原来源 | 通过 | CLI 默认只读，写入同时要求显式参数和环境开关；输入文件验证所有者、权限、类型/软链接、大小，部署环境决定数据库，OS 身份不能由 JSON 冒充。当前管理员身份、原事实证据、稳定操作键和资金变更在同一事务中校验/记录；撤权后不能重放成功。允许已启用 root 核对自己初始来源，普通管理员不能越权或自我核对，清退仍需目标禁用，不能自我清退。Realtime 审计失败全事务回滚；没有投影的原请求需核对身份，保留唯一使用记录，不编造 Token 用量。未知费用不能因停写被当零。真实 CLI 与模型/服务三库验证上述分支。 |

## 最终验证证据

以下命令由开发主代理执行，独立验收读取完整相关实现、夹具、最终日志并核验产物摘要；为避免重复占用共享验证环境，本轮未另行重新构建或清理数据库。

1. `.dev/run go test ./controller ./service ./model ./internal/marketing ./middleware -count=1 -timeout=300s`
   - `build/dev/mv2-011-packages-r3.log`：五包全部通过，分别为 238.687s、10.705s、25.679s、0.003s、5.484s。
2. `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`
   - `build/dev/mv2-011-accounting-r4.log`：最终源码模型/服务三库回归合计 578 个含子测试（415 + 163），0 失败、0 跳过。已核对每库维护场景和 `crash_without_projection` 等服务场景。
3. `python3 .dev/marketing-v2-requirements/validation/refund_process.py all`
   - `build/dev/marketing-v2-validation/refund-process-20261123T084622.840990Z.log`：`REFUND_ALL_OK`，全部真实维护/双网关/序列屏障场景通过。
   - 网关 SHA256 `0e46a56cbed81f0f7566449a13d5a12cd3601a63aa84193f2fcfa06d1d9fe374`；夹具 SHA256 `d647dc023fde316ca264b4e287579e9911027a20de3ca7ab5a1022b203e965a1`。
4. `python3 .dev/marketing-v2-requirements/validation/refund_process.py controller`
   - `refund-controller-{sqlite,mysql,postgres}-refund-auth-20261123T084804.705668Z.log`：每库 14 顶层/67 含子测试、0 失败、0 跳过；最终即时任务/异步提交路径也在其中。
5. `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`
   - 后缀 `20261123T082233.994348Z` 的 15 份日志包含 18 次启动（每库正式基线 seed/verify、新建 seed/verify、升级 verify 两次），全部 `MIGRATION_PROBE_OK`。
   - 此后改动为准入/结算证据和回归夹具，未改数据库模式；最终业务路径另由 r4 和最终控制器矩阵覆盖。因此明确保留这批模式验证，不把早期探针冒称为最后一次全网关构建。
6. `build/dev/mv2-011-i18n.log` 及七语言差异：新增订阅不可用消息已补齐；没有暴露新的内部 quota 展示界面。

实际数据库版本为 **SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11**；双进程使用 **Redis 7.2.17，同一逻辑库 DB8**。本报告不声称测试了 MySQL 5.7.8 或 PostgreSQL 9.6 的最低版本。

## 复验关注点与共同约束

- 旧任务部分返还：独立预审指出的重复退款问题已修复。`legacy_partial_refund_uses_independent_confirmed_sources` 和真实 `REFUND_PREVIOUS_RETURN_OK` 证明不能把遗留 task.quota 当作当前净扣款；无限令牌负历史值、软删除令牌及终态未核对任务均被保留并明确处理。
- 零估算不是免费：非明确 FreeModel 请求最少占用一个内部单位，但实际可信零费用全部释放，不增加最低消费。已耗尽有限 SK 的真实数据库状态在预约及派发前复核；本请求合法全额占用不被误拒。Realtime 下一轮重新判断，无法利用旧连接绕过。
- 完成事实缺失：新营销即时和异步任务显式调用 `EvaluateMarketingTaskCompletionUsage`，依赖用量的表达式在完全没有完成事实时保持未知/占用。常量表达式可正常结算；原正式基线 `EvaluateTaskCompletionUsageAt` 的 overlay 契约保留。没有为了通过测试回到旧余额旁路。
- 缓存失败：真实 CLI 测试无效 Redis 地址/真实连接拒绝在执行前阻止变更，预览仍只读；正常路径删除用户与普通/软删除 SK 缓存并验证重新加载。提交后故障由服务层真实 go-redis 关闭客户端验证：返回 `executed=true`、`state=settled`、`cache_refresh_pending=true`；恢复后同键重放只返回原结果、补失效，资金和使用记录不重复。明确不把后者描述为真实 CLI 中途网络故障。
- 安全参考：OWASP Authentication、Session Management、Authorization、CSRF Cheat Sheets；ASVS 5.0 相关身份、会话、授权控制。HTTP 入口沿用并回归既有敏感操作验证/会话撤销/归属权限；本地维护信任边界是已授权 OS 操作者和部署数据库权限，不是凭 actor_id 取得网站登录身份。本次没有全站 ASVS 合规认证结论。
- 所有最终资金及数据库证据使用真实数据库；仅外部模型/支付供应商可模拟。唯一性、事务序列、行锁与审计承担多实例正确性，不以进程内锁或 Redis 临时锁作为最终账务依据；没有增加中间件。
- 冻结验收项未变；阶段失败记录保留，最终整项回归覆盖修复后的原验收范围。现金渠道仍限人民币易支付和正式付费卡密，不引入订阅、旧营销数据迁移、赠金人工审核/待激活等旧版产品行为。

## 边界

本次没有执行真实支付平台现金退款、修改线上配置、推送、发布或部署；管理员仍需对原凭据和退款现金承担人工核实责任。未知实际费用没有被自动免账。用户/管理员完整营销页面、钱包与日志的展示验收属于后续 MV2-012–014；50–100 并发规模的最终容量结论属于 MV2-015，不能从本次正确性回归推断。
