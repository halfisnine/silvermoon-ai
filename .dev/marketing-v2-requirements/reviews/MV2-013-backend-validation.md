# MV2-013 用户后端实现及验证

日期：2026-11-23。实现 agent 的交接记录，不代替独立验收，不修改冻结验收项，也不表示完整 Issue 已完成。

## 范围与业务来源

用户活动中心、奖池与抽奖结果、活动奖励及赠金明细、邀请有礼 API 已接入新版真实业务数据。签到／补签沿用 MV2-010 的真实接口，并增加活动状态、服务端时间及关联奖池名称。详细返回结构见 `MV2-013-api-contract.md`。

- 中心只展示已启用并发布的四类活动，含当前、即将开始及结束未满 72 小时的活动；隐藏规则不撤销已发赠金和原池机会。
- 来源奖励与实际赠金按当前会话用户隔离。邀请关系来自新版事实，不使用旧邀请返佣余额；仅本人奖励的实际支付情况允许展示，好友支付详情不返回。
- 抽奖复用新版数据库事务、机会及奖项规则。请求标识绑定用户，重复提交和刷新恢复同一结果；每份组合赠金分别有实际到账与到期时间。部分失败返回真实 pending，原请求重试补齐原结果，不再次抽取或扣机会。
- 奖项返回真实可用状态，耗尽奖项不呈现为仍可中奖。原活动结束及中心隐藏后仍可查询本人机会所属的独立奖池。
- 消费、到期、清零金额分开。预占是剩余金额的子集，不能重复加到余额，也不能提前当消费；到期时仍被在途调用持有的部分保留至真实结算。
- 固定赠金和充值／邀请比例赠金均读取发放时的换算证据。比例赠金的证据中可能保存充值面值或单位换算值，展示必须使用真实获赠金额；站点后来改换算设置不能重写历史金额。

没有迁移、导入或兼容未上线的旧营销系统数据。没有增加额度激活、审批或赠金预算。没有新增数据库字段、索引、迁移或中间件。

## 真实三库 HTTP

执行命令：

```sh
.dev/run go test -c -o /artifacts/marketing-v2-013-controller.test ./controller
python3 .dev/marketing-v2-requirements/validation/user_http_matrix.py > build/dev/mv2-013-http-matrix-final.log 2>&1
```

最终均退出 0，无 SKIP。控制器二进制 SHA256：`ccbc586cc551de8ce3fa0ff2ce241dc19f10dd066b34cc710a6b3713e9d9c6fe`。每库日志首行记录同一哈希。

| 数据库 | 实测版本 | TestMarketingCheckinHTTP | TestMarketingUserHTTP |
| --- | --- | --- | --- |
| SQLite | 3.50.4 | PASS（0.58s） | PASS（0.69s） |
| MySQL | 8.4.13 | PASS（0.43s） | PASS（0.60s） |
| PostgreSQL | 17.11 | PASS（0.38s） | PASS（0.45s） |

日志：`build/dev/marketing-v2-013-user-http-{sqlite,mysql,postgres}.log`，编译日志 `build/dev/mv2-013-http-build-final.log`。测试在独立 `silvermoon-marketing-v2` 栈创建随机隔离数据库并清理，不使用线上库。没有引入依赖版本特性的 SQL；未实测 MySQL 5.7.8 / PostgreSQL 9.6，不宣称实测过最低版本。本 Issue 没有 schema 改动，基线升级／重复启动迁移证据保留在 MV2-012 记录。

覆盖的真实行为：

1. 服务端会话认证；仅 Refresh Cookie 的跨站请求拒绝，wallet scope PAT 不能代替浏览器会话；跨用户奖池、结果、奖励、赠金查询拒绝；撤销会话和禁用账号后读取及成功抽奖重放拒绝。
2. 四类活动、草稿及未发布隐藏、未来开始、结束 71 小时显示、结束 72 小时隐藏；原池仍显示未到期个人机会。
3. 真实注册事实自动发放赠金／机会，真实抽奖组合到账、原 key 重放与查询；暂停不扣次数且截止继续有效；来源筛选和字段隐私。
4. 固定及两种比例赠金的历史换算。实际支付 60、到账面值 30 的 10% 赠金分别显示 3（到账比例）和 6（实际支付比例）；当前站点换算改为 9 后，历史结果不变。
5. 真实预占及结算：赠金 3，调用持有 1 时到期，显示剩余 1、过期 2、消费 0；实际结算 1 后显示剩余 0、过期 2、消费 1。
6. 组合奖励第二份真实入库失败时，首份已到账、第二份 pending 且无伪造到期时间；原 key 重试补齐且只扣一次机会。
7. 已过期机会不因奖池延期重新有效；清零后延期同样不恢复机会；真实邀请事实生成本人好友列表和同站邀请链接。

路由／个人访问令牌策略另外执行：

```sh
.dev/run go test -p 1 ./controller ./router -run '^Test(MarketingUserHTTP|MarketingCheckinHTTP|AccessToken.*)$' -count=1
```

`build/dev/mv2-013-http-r4.log`：controller 1.738s、router 0.218s，均通过。此后仅新增 HTTP 边界断言、整理 import；业务与路由代码未变，最终三库二进制已包含新增断言。早期 r1–r3 的编译／断言失败日志保留，不隐藏失败历史。

## 认证与安全依据

已阅读 [Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)、[Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)，并核对 [ASVS 5.0.0](https://github.com/OWASP/ASVS/tree/v5.0.0) 的授权控制，尤其 V8.2.1、V8.2.2、V8.2.3、V8.3.1、V8.3.2。未更换认证协议或凭据生命周期，也不声称全站 ASVS 认证。

新增用户接口和签到接口要求当前 dashboard bearer session，不接受仅 Cookie 的写操作，因此不能借跨站携带 Refresh Cookie 写入；真实 bearer 同时受现有会话撤销机制保护。业务事务再次检查数据库当前账号启用状态，查询按当前用户所有权过滤，结果恢复和重试不绕过授权。数据查询不信任客户端 user_id；不返回支付引用、销售凭据、可用卡密、内部权重、随机种子、换算倍率或数据库异常内容。

测试只生成本地测试身份，不在证据日志保存可用会话令牌。浏览器完整主题／移动端及端到端验收由另一 agent 继续完成；本记录不据 API 成功推断 UI 通过。读事务持有用户行锁，多实例性能及热点影响仍由 MV2-015 的真实负载验收处理。
