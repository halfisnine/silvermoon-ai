# 赠金管理开发交接

工作区 `/opt/dev-workspace/silvermoon-ai-channel-model-batch`，分支 `codex/channel-model-batch`，基线 `d97cbc166d6fc201d62574471c972b82ceaef0e1`（当前 `origin/custom/main`）。按用户要求与批量定价共用分支，不提交、推送或部署生产。

## 入口与业务

系统设置 → 营销 → 赠金管理；管理员路由 `/marketing-admin/gifts`。

- 「全部赠金」查看所有用户、所有赠金来源，可按用户 ID／账号／显示名称、来源、状态、永久／限时及入账时间筛选；详情区分发放、消费、剩余、过期和清理金额。
- 「手动发放记录」查看批次、原操作人、内部原因、每人金额、实际成功合计、有效期及每位用户的处理结果；可重试失败／待处理项。
- 首版每批最多 100 位已启用用户，同批使用相同金额和有效期，支持小数、永久或按小时／天设置的限时赠金。每位用户成功入账时开始计算有效期。金额沿用系统钱包币种、精确换算及钱包上限，不自行定义兑换比例。
- 二次确认展示收件人、每人金额、总额、有效期和内部原因。金额配置发生变化时要求刷新；未知网络结果保留原操作键，重复请求不会重复发放。
- 手动赠金的来源为 `admin_gift`，不是营销活动，不增加充值余额，不制造充值记录或活动触发事实；使用已有赠金、钱包和账本规则。
- 用户「活动奖励 → 赠金明细」只展示自己的赠金，来源显示「管理员赠送」；管理员访问用户入口也只看自己。内部原因、操作人和手动批次信息不进入用户接口。
- 不新增活动、优惠券、撤回赠金、定时发放、导入名单、导出或消息通知功能。

## 实现与组件复用

新增 `model/marketing_admin_gifts.go`、`service/marketing_admin_gifts.go`，扩展原 `controller/marketing_admin.go`。权限沿用 `AdminAuth` 和仪表盘登录会话，每次读写及每位收件人的发放在事务中检查数据库当前管理员权限。

新增两张表：`marketing_v2_gift_batches`（原金额快照、有效期、操作人、原因、操作键）；`marketing_v2_gift_recipients`（收件人、处理状态、赠金关联、安全失败原因及最后处理人）。现有赠金和账本表继续是资产事实来源。操作人＋操作键、批次＋用户均有数据库唯一约束。

每位收件人独立事务，锁定批次后发放并更新结果；账本写入失败则整个收件人的资产改动回滚，再单独记录失败。重复或并发重试跳过已成功用户，保留最初金额换算快照。批次总额展示实际成功金额，不包含失败或待处理项。

管理接口均位于现有 `/api/marketing/admin`：

- `GET /gifts`、`GET /gifts/:id`
- `GET /gift-batches`、`GET /gift-batches/:id`
- `GET /gift-users`：只返回可选择用户的 ID、账号和显示名称，支持分页搜索。
- `POST /gift-batches`：创建并处理批次。
- `POST /gift-batches/:id/retry`：仅重试尚未成功的收件人。

前端位于 `web/src/features/marketing-admin/gifts/`，复用 `SectionPageLayout`、`DataTablePage/useDataTable`、`StaticDataTable`、`Dialog`、`ConfirmDialog`、`MultiSelect`、`Checkbox`、日期时间选择器、加载／错误状态、React Hook Form/Zod 及营销金额／日期格式化。既有页面没有跨用户赠金批次及处理状态的组合能力，因此增加业务组件组合共享控件，没有新增通用控件替代品。

公共改动限于新菜单、生成路由、七种语言、新表迁移注册和既有赠金来源投影。原批量定价文件通过开发前哈希快照核对保持一致；语言文件和路由树仅追加本功能内容。

## 已运行检查

通过仓库 `.dev/run` 使用固定工具链：

```sh
.dev/run bun run typecheck
.dev/run bun run build
.dev/run bun run test src/features/marketing-admin src/features/marketing/__tests__ src/features/channel-model-batch
.dev/run bun run test src/features/marketing-admin/gifts
.dev/run bun x --no-install oxlint -c .oxlintrc.json src/features/marketing-admin/gifts src/routes/_authenticated/marketing-admin/gifts.tsx src/i18n/static-keys.ts src/components/layout/config/system-settings.config.ts
.dev/run bun run i18n:sync
.dev/run go test -c -o /artifacts/gift-management/controller.test ./controller
python3 build/dev/gift-management/database-matrix.py
python3 build/dev/gift-management/migration-matrix.py
.dev/run go build -ldflags '-X github.com/QuantumNous/new-api/common.Version=channel-model-batch-local-preview' -o /artifacts/channel-model-batch-gateway.next .
git diff --check
```

前端原有相关回归 146 个用例通过，补充「货币配置拒绝时显示原因并解锁表单」后赠金模块 7 个用例通过；类型、Lint、七语种缺失键检查和构建通过。后端管理员／用户接口回归在真实 SQLite **3.50.4**、MySQL **8.4.13**、PostgreSQL **17.11** 上通过，覆盖部分成功、冻结汇率重试、过期与永久赠金、权限撤销、用户隔离、事务回滚及不同管理员并发重试。

同三种数据库完成新库初始化、由上述主分支创建的代表性数据库升级、重复启动和唯一约束检查，保留原用户、钱包、赠金、账本及既有索引。两张新增表只属于主库，不涉及单独日志库。临时验证脚本、编译二进制、数据库及日志位于忽略目录 `build/dev/gift-management/`，未写生产数据库。未验证 MySQL 5.7.8／PostgreSQL 9.6 的最低版本实例；实现没有引入版本特有 SQL。

## 浏览器与独立预览

真实 Chromium 在一次性 SQLite 副本和专属 `127.0.0.1:46017` 容器中完成实际登录、两用户限时赠金、永久赠金、确认合计、入账有效期、批次结果、全部赠金记录、普通用户自己的赠金明细、管理员接口／路由拒绝和桌面／390px 手机布局检查；测试发放未写入原预览库。脚本及截图位于 `build/dev/gift-management/browser/`。

原独立预览 `silvermoon-channel-model-batch-preview` 已更新，仍监听 `127.0.0.1:46016`。公司电脑沿用已有 `62021 → 46016` SSH 隧道时，入口为 `http://localhost:62021/marketing-admin/gifts`；原批量定价仍为 `/channel-model-batch`。更新前使用 SQLite backup 生成即时备份 `build/dev/gift-management/preview-before-update-20261127T021127Z.db`，保留原数据库挂载，未恢复旧备份。更新后两路由均为 200，并核对渠道、模型、供应商、赠金和账本全部原记录不变。未发布镜像、未操作生产服务或线上配置。

浏览器中文字体检查使用现有 `/tmp/fonts` 字体目录（`FONTCONFIG_PATH=/tmp/fonts`），未修改前端字体或系统配置。一次性 `silvermoon-gift-management-browser-test` 容器已在验收结束后删除，保留截图、结果及本地测试数据库便于复核；原 `46016` 预览继续运行。


## 发放弹窗交互调整（2026-11-27）

接收用户改为单个可搜索多选框：直接按账号、显示名称或用户 ID 查询真实用户目录，下拉选择后显示已选标签，继续搜索或翻页保留选择；每批最多 100 人。移除原独立搜索框和外置候选列表。兼容扩展共享 `MultiSelect` 的远程搜索、过滤、禁用选项、加载／错误状态及分页插槽，其他调用沿用原默认行为。

「已选人数／赠金合计」保持同一行；「永久赠金／限时长度／小时或天」保持同一行。手机端隐藏有效期的重复可视标签，保留无障碍名称。复用已有翻译，无新增语言键，不更改发放规则或后台接口。

本次赠金及共享多选框 21 个测试、原批量定价 54 个回归测试通过；类型检查、定向 Lint、格式和生产构建通过。真实 Chromium 在独立数据库副本中验证名称／ID 搜索、跨查询及分页保留选择、确认合计，以及 1440px／390px 的单行布局，无页面异常；本轮未提交实际赠金。结果位于 `build/dev/gift-management/browser/inline-result.json`，截图为同目录的 `inline-issue-1440-zh.png`、`inline-issue-390-zh.png` 和 `inline-search-zh.png`。

已更新原 `46016` 独立预览，更新前生成新的即时 SQLite 备份（路径见 `build/dev/gift-management/inline-preview-update.log`），核对原渠道、模型、供应商、赠金和账本记录全部不变。公司电脑仍通过 `http://localhost:62021/marketing-admin/gifts` 访问。没有提交、推送、发布镜像或修改生产配置。
