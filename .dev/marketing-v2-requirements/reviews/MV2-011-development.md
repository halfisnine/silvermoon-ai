# MV2-011 退款清零与正式基线切换开发记录

2026-11-23，基于已验收提交 `b71c4b7`。状态：开发/验证中，尚未独立整项验收，不标记完成。冻结 A01–A06 与共同约束不变。

## 实现范围

复用原管理员禁用账号操作及敏感操作验证；没有新增退款工单、前台退款或一键清零产品。普通禁用只拒绝全部 SK 新调用，原来源在途请求继续结算。人工在支付平台退款后，通过受信运维主机的维护命令登记现金、尾差及资产清退。

维护命令默认只读，不运行迁移或后台任务；写入必须同时显式执行开关和参数，OS 实际身份、当前管理员权限、原事实证据、隔离依据、操作键绑定审计。JSON 不能指定数据库或操作者 OS 身份，私有文件限制所有者、权限、类型及大小。该入口的信任来自主机与部署数据库权限，不把 actor_id 伪装成网站登录/MFA。

新增核对来源、原请求/旧异步任务结算与清退的单事务审计。赠金不折现；现金四舍五入到分保存尾差，不从当前展示配置猜历史实付。未知实际费用保持待核对并阻止清退，停写不是免账依据。新请求全部走新账本；未经来源核实的正式基线余额拒绝新调用，不默认视为本金。

清退使用数据库事务序列界线，配合有界旧资格准备，防止旧充值/跨用户邀请的延迟奖励复活；不使用进程锁、MAX(id) 或 Redis 临时锁作为最终依据。保留首充、一次资格、签到、消费和中奖事实，不回收已发机会的发行名额。清退后新入账仍按原规则取得新权益，旧清退操作重放不能清掉新资产。

Realtime 维护核对加入同一个外部事务，审计失败时费用、占用与原终态一起回滚；原日志投影缺失时必须核对原请求身份，保留一条使用记录，明确用量未知，不能编造 Token 数量。旧任务分别核实钱包净扣、用户用量、令牌扣款和渠道用量，避免旧流程部分返还造成再次退款。

关闭绕过来源账本的管理员直接改余额、旧队列写受管余额和新增订阅入口；保留正式基线历史查询与必要旧任务结算。没有迁移旧营销系统/预览数据。只使用人民币易支付与付费小店卡密；当前 1:1 仍是配置，换算不硬编码。

## 验证与修复历史

- 独立预审发现旧任务钱包已返还但任务标记未更新时可能重复退款；已改为四个独立已入账净事实，并增加零净扣、无限令牌历史计数、软删除令牌和终态未审核任务回归。
- 真实 CLI 回归发现 RawMessage 的 JSON null 被误识别为实时终态；入口已归一化，并在服务生命周期增加普通请求 JSON 往返回归，真实 CLI 重跑通过。
- 完整包 r1 暴露新调用强制来源账本后旧 fixture 仍只写 User.Quota，部分 WS 测试等待超时；正在按真实已核实来源与持久占用适配夹具。未知费用预期改为待核对及保留占用，不能为使测试通过恢复旧余额旁路。
- 模型专项三库 `build/dev/marketing-v2-maintenance-model-full-r4.log` 已通过，含资金、活动、签到、抽奖、维护和支付。
- 真实双网关/维护 CLI：`python3 .dev/marketing-v2-requirements/validation/refund_process.py all`，最终阶段证据 `refund-process-20261123T081800.959168Z.log` 退出0。覆盖双 SK 暖缓存禁用、在途 SSE/MJ、并发清零/重启、1.809→1.81 尾差、未知费用、旧任务部分返还、延迟权益、新付款，以及两个独立支付进程与清退的事务先后屏障。
- 权限三库：`python3 .dev/marketing-v2-requirements/validation/refund_process.py controller` 退出0；`refund-controller-{sqlite,mysql,postgres}-refund-auth-20261123T081924.219231Z.log`，每库12顶层/51含子测试，0跳过，独立日志库。
- 完整迁移：`python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` 退出0；日志后缀 `20261123T082233.994348Z`，实际 SQLite3.50.4、MySQL8.4.13、PostgreSQL17.11。每库正式 ca5af4c 基线 seed/verify、新建 seed/verify、升级 verify 两次，共18次真实启动。历史钱/码/Aff/任务/订阅/管理员赠送原文及索引保持，不伪造资产来源。
- 新增订阅不可用说明通过 i18n 脚本补齐全部7语言并运行同步，`build/dev/mv2-011-i18n.log`。

适用安全参考为 OWASP Authentication、Session Management、Authorization、CSRF Cheat Sheets 和 ASVS 5.0 的身份/会话/授权相关要求。原 HTTP 敏感验证、撤权、越权、失效与重放由真实认证回归覆盖；本机维护为独立 OS 信任边界，不声称全站 ASVS 合规。

最终全包、更新后的三库来源结算、缓存维护回归和独立 A01–A06 整项验收仍待完成。阶段 PASS 不能代替整项通过；最终证据将在 manifest 绑定后附加。

### 回归中发现的两项资金漏洞

1. Responses WebSocket 关闭输入 Token 估算时可能产生零预占。真实有限 SK 已耗尽、Redis 仍有旧值时，第三轮原先仍到达上游，再在结算时发现不足。独立审查确认后，非明确免费请求采用至少一个内部单位的来源占用；这不是最低消费，可信零费用完整释放。初次预约在幂等返回后核对真实 Token 可用量，派发前计入本请求占用再次核对，避免全额占住的合法请求误拒。新增三库模型回归与服务钱包零/Token零/明确免费/零费用释放用例；完整 WS r4 已通过，74.827s。
2. 即时成功任务完全缺少完成用量时，原条件跳过费用检查，把提交估算当作实际消费。现在共享完成评价通过编译表达式依赖识别必须提供用量的情形；即时入口不因空用量跳过。纯常量任务价格不依赖用量，仍可完成结算；用量未知继续占用、零已消费、待核对。集中控制器回归9个即时场景及原任务恢复5场景通过，`build/dev/marketing-v2-controller-fixture-r3.log`。

两项均保留原安全断言后修复生产逻辑，没有通过删除失败测试或恢复旧余额旁路取巧。`accounting-r1` 在这两项生产修复前已三库全通过，作为阶段记录保留；最终采用后续 r2 及完整包、真实 CLI 缓存回归证据。

### 最终候选验证

全包 r2 的 controller/internal/middleware 已通过；剩余失败均保留日志：两个固定零价测试将准入占用误当收费、两个 Realtime 夹具在连接前即使用耗尽 Token，以及共享任务评价器误改了 legacy overlay 契约。前两组修正夹具/占用断言，最终零消费及每轮拒绝仍保留；最后一项将严格完成用量检查收口到显式 `EvaluateMarketingTaskCompletionUsage`，即时与异步营销路径共同使用，原 legacy 评价器和测试恢复原契约。

- `.dev/run go test ./controller ./service ./model ./internal/marketing ./middleware -count=1 -timeout=300s` 最终 r3 退出0，`build/dev/mv2-011-packages-r3.log`：controller238.687s、service10.705s、model25.679s、internal0.003s、middleware5.484s。不是只重跑失败项。
- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` 最终 r4 退出0，`build/dev/mv2-011-accounting-r4.log`；真实三库，模型415个含子测试、服务163个含子测试，0失败/0跳过。包含最小准入、实际零费用释放、原始请求身份、JSON null、事务回滚，以及提交后缓存刷新失败/原操作重放回归。
- `python3 .dev/marketing-v2-requirements/validation/refund_process.py all` 最终日志 `refund-process-20261123T084622.840990Z.log` 全通过。真实两个网关共享 PostgreSQL17.11、Redis7.2.17 DB8；实际CLI原流程与新增Redis预检/三类缓存失效、双独立支付进程序列屏障均通过。网关SHA `0e46a56cbed81f0f7566449a13d5a12cd3601a63aa84193f2fcfa06d1d9fe374`，夹具SHA `d647dc023fde316ca264b4e287579e9911027a20de3ca7ab5a1022b203e965a1`。
- 缓存故障的层次：真实CLI验证无效URL/真实连接拒绝不会执行资金操作、预览仍只读、成功执行清理正常与软删除SK缓存；服务生命周期以真实go-redis关闭客户端验证数据库提交后失效失败返回已执行及待刷新，恢复后相同键只重放原结果。后者不冒充真实CLI中途断网时序。
- 数据库模式在迁移矩阵后未再变化。迁移仍以082233的18次真实启动及日志为依据；之后的最小准入和任务证据修复由最终三库业务与完整包验证覆盖。
- `git diff --check` 通过；15份Issue冻结验收段与HEAD逐字一致，7语言新增文案非空。没有更改验收文本、引入旧营销迁移、推送或部署。

最终控制器三库日志及绑定清单完成后，再由独立代理对A01–A06和共同约束正式逐项验收。

最终控制器三库：`python3 .dev/marketing-v2-requirements/validation/refund_process.py controller` 退出0，`refund-controller-{sqlite,mysql,postgres}-refund-auth-20261123T084804.705668Z.log`，每库14顶层/67含子测试、0失败/0跳过，保留真实权限回归并加入即时任务与异步提交路径。二进制SHA `b4f8d2b981dcf645abb3e8fdacc4d3f0081833d4ba983900e9b4cade1ce2cccc`。

最终候选绑定 `build/dev/mv2-011-review-manifest-r1.json`；当前代码、验证脚本、最终日志/产物及全部冻结验收段逐项记录摘要，等待独立整项结论。未提前更改完成状态。

## 独立整项验收完成

2026-11-23，`mv2004_path_audit` 正式复核 A01–A06 与共同约束全部通过，报告 `MV2-011-acceptance-r1.md`。开始/结束54源文件、30产物、16冻结段一致；保留阶段失败与验证边界。现仅更新状态及验收记录，立即本地提交，不推送或部署。
