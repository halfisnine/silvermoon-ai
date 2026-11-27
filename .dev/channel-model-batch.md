# 渠道模型批量处理开发交接

## 开发位置与入口

- 工作区：`/opt/dev-workspace/silvermoon-ai-channel-model-batch`
- 分支：`codex/channel-model-batch`
- 基线：开始开发时获取的最新 `origin/custom/main`，提交 `d97cbc1`。
- 新菜单：管理区的「渠道模型批量处理」。仅超级管理员可见、可访问。
- 路由：`/channel-model-batch`，支持 `?channel=<渠道 ID>`。
- 原有渠道管理、模型资料和模型定价页面的实现均未修改。
- 未部署、未发布镜像、未修改线上配置；原工作区和其他已有工作区未写入业务改动。

## 数据与保存语义

页面从渠道已配置的 `models` 枚举模型，并按现有 relay 的链式映射及 reasoning 后缀规则解析最终上游名称。这里不重新接入渠道，也不把上游探测结果直接加入渠道。

新增接口：

- `GET /api/channel/:id/model_batch`：返回渠道公开路由字段、模型资料、系统供应商目录、影响范围和版本。
- `POST /api/channel/:id/model_batch`：在一个事务内保存渠道映射、路由能力、精确模型资料和价格。

接口复用现有管理员认证、超级管理员限制和渠道读写权限。响应不包含渠道密钥。保存需要资料快照版本及各模型的原价格版本；并发冲突返回 409，整批回滚。每次最多保存 1000 个变更模型。

客户名称修改影响当前渠道的 `models`、`model_mapping` 和 abilities。保留原映射键，避免破坏其他模型引用的链。原名称的全局资料、价格不会被删除。改名时保留该行明确编辑过的供应商、价格、缓存开关和未完成输入；没有编辑过的资料和价格仍按新名称解析。价格草稿的冲突校验绑定目标名称，并记住访问过的名称版本，避免改回原名时绕过并发冲突检查。尚未结束的旧价格转换继续完成所保留的输入草稿，但不能把原名称的价格版本写到目标名称。

模型资料、供应商和价格按客户模型名全局共享。最终确认列出使用同名模型的其他渠道。已有精确模型资料保留其可见状态；新建精确资料设为隐藏，关闭官方同步。保存不会更改渠道启停状态，已启用渠道的映射调整会在保存后生效。

供应商来源为现有 `Vendor` 表，以及 `model/pricing_default.go` 中的 23 个内置品牌、名称和图标。已保存的同名供应商优先使用其记录和图标，内置品牌不依赖前端硬编码。读取目录不写数据库；明确选择内置品牌并确认保存后，在整批事务中按现有供应商校验规则创建正式记录，多模型只创建一次。负数预设 ID 不会写入模型外键。供应商可搜索、逐个或批量选择。保存的模型 `icon` 置空以复用系统已有的供应商图标回退规则，后续供应商图标更新也会继承；页面不提供模型独立图标编辑。

## 定价与行内布局

按认可原型恢复紧凑工作台：一行渠道概况、紧凑工具栏、独立用户模型名／供应商／上游模型列、单行计费方式与四种小数价格输入、底部检查保存。普通按次/按量价格直接编辑，仅复杂阶梯、任务供应方或高级设置打开原有 `ModelPricingEditorPanel`。复用 `pricingFromDraft`、表达式可视化 AST、服务端价格校验及预览。用户明确通过行内快捷按量／按次或批量统一设置重新定价时，一律生成计费表达式。旧价格同行编辑先复用只读转换接口，保留可安全识别的缓存、图片、TTL 与请求倍率等独立价格；旧 `ModelRatio`、`CompletionRatio`、`ModelPrice` 等已配置字段留作非生效的兼容记录，不再用这些旧字段承载本次新价格。新建简单价格及缓存快捷配置也使用表达式。保留原价、仅改供应商／名称，以及批量比例调整不触发迁移。表达式使用 `billing_setting.billing_mode`、`billing_setting.billing_expr`；任务供应方覆写沿用 `billing_setting.plugin_billing_expr`。金额支持小数和显式零。

批量设置同时提供供应商搜索与批量价格；另有批量价格快捷入口。价格支持保留原价、比例调整、统一按量、统一按次、复杂阶梯/表达式五种选项；先预览并应用到本地草稿，再进行最终确认保存。比例调整复用现有表达式 AST 调价逻辑，保留阈值、使用量及请求倍率。缺价、无法安全识别的表达式及独立任务供应方覆写会阻止整批比例操作，需逐个处理。批量统一定价保留各模型原有任务供应方覆写，单个模型可通过现有供应方编辑器修改。

按当前确认布局，移除本页面的上游参考价格列、参考价格来源入口、报价提示和缺报价计数／筛选；页面不再请求 `/api/ratio_sync/channels`、`/api/ratio_sync/fetch`，也不自动获取或推断上游价格。原系统的价格同步页面未修改。

列表列顺序为选择、用户模型名、供应商、对客定价（USD）、上游模型、状态、操作。用户模型名和上游模型列平分所有可用剩余宽度，并通过悬停显示完整名称；供应商搜索选择紧随用户模型名独立成列，上游模型紧随对客定价，上游长名称省略展示且保留完整悬停文本。高级设置统一置于操作列，不再占用价格区。按量行的计费方式、输入／输出价格、缓存读／缓存写开关与价格全部在同一行，缓存单位简写为 / 1M；短标签仅用于紧凑列表，批量表单和可访问名称保留完整说明。工作台移除原 1460px 居中宽度上限，使用主区域全宽且左右各保留 8px 边距。对客价格列固定 618px，避免价格区闲置宽度挤占模型名称；供应商 120px、状态 72px、操作 80px，名称两列共享剩余空间。顶部选择渠道标签、选择器、渠道概况与计数在桌面同行，卡片上下各 8px 内边距，窄屏自然换行。价格输入缩短显示宽度，并限制宽屏下的拉伸；按次价格框宽度上限 110px，按量基础价格组共 216px，缓存组共不超过 318px，不设金额长度或小数位限制。较长语言的缓存标签省略显示且有完整悬停和可访问名称，操作及状态文案允许换行。表格最小宽度 1180px，窄屏在表格内部横向滚动；按次行不显示不适用的缓存配置，复杂阶梯通过操作列或计费方式打开现有编辑器。

### 缓存快捷价格

批量统一按量价格和列表复用 `CachePriceFields`，通过 `compact` 属性组合现有 `Switch`、`Label`、`InputGroup`，分别适配批量表单和行内输入。两个开关独立控制，金额支持连续小数输入及显式零。关闭开关表示不再独立计价，缓存读取和普通写入随输入价计费；开启并填写 0 则明确生成独立零价格。行内控件悬停提示说明此区别，批量表单仍显示完整说明。长效写入遵循系统 TTL 规则：修改普通写入价时保留已配置的 cc1h / cc12h 单价，关闭写入开关则明确移除全部独立写入项；TTL 单价使用高级设置修改。

表达式编辑保留其他媒体价格、请求条件倍率和显式零值。旧按量／按次的基础价格及缓存编辑先调用现有只读 `/api/option/model_pricing/convert`，仅提交完整显式配置，由服务端解析默认值，不把默认 1.25 倍写入价当成已配置项。仅浏览价格不改变旧模式；明确编辑基础价格或缓存时，才把转换结果和输入的快捷价格合入表达式草稿，仍经过最终服务端预览与保存确认。转换不支持的复杂规则明确提示并保留原草稿，使用高级设置处理。新建简单价格采用表达式。比例调价保留未配置缓存字段的回退状态。

行内价格校验不再在控件下方追加可见红字或在表格上方重复同一价格错误；错误字段通过共享输入组件的 `aria-invalid` 样式标记，状态列显示紧凑价格警示，悬停或键盘聚焦可查看完整提示。输入区保留原生悬停说明及屏幕阅读器可读描述。数值错误提示改为“请填写大于或等于 0 的价格，支持小数。”，未补全／非法价格继续阻止保存，修正后自动清除警示；旧价格转换失败改用价格区浮层说明，不撑高行。该组合复用共享 Tooltip / InputGroup，无新通用组件。

当前回归覆盖：行内直接出现两个缓存开关和输入框、没有参考价格列／入口／筛选及报价请求、小数连续输入保留小数点、关闭与显式零的区别、统一缓存价应用全部已选模型、长效写入及请求倍率保留、只读旧价格转换、不把回退缓存倍率固化为覆盖。旧定价仅在鼠标进入价格区或键盘聚焦后按需准备只读转换，未编辑前不改变定价草稿。未新增后端 API 或数据库变更。实际空价格模型的生效快照含默认缓存／图片倍率，新建价格不会再把这些回退值误判为已配置的复杂价格；显式配置的额外计费项仍保留高级编辑保护。

## 组件复用与文件范围

主要实现位于 `web/src/features/channel-model-batch/`，新路由位于 `web/src/routes/_authenticated/channel-model-batch/`。仅在 sidebar hooks 和生成路由树增加入口，七种语言同步增加新文案。

通用交互复用 `Main`、`StaticDataTable`、`Combobox`、`InputGroup`、`Switch`、`Label`、`Dialog`、`ConfirmDialog`、加载/错误/空状态组件；价格编辑和确认展示复用现有定价编辑器、`ModelPriceCell` / `DynamicPricingBreakdown`。

新增 feature 弹窗承载渠道映射、供应商及定价的组合草稿和统一预览：原有单模型定价面板不负责渠道映射、供应商及整批事务，不能直接用作这个组合工作流。通用弹层、确认和表格行为继续使用共享组件，未新建通用替代组件。

后端位于 `controller/channel_model_batch.go`、`model/channel_model_batch.go`，路由新增于 `router/channel-router.go`。回归扩展已有 `controller/model_management_test.go` 和 `router/channel_router_test.go`。不新增数据库字段、迁移或依赖。

## 验证记录

在本工作区通过 `.dev/run` 运行：

```sh
.dev/run bun run typecheck
.dev/run bun x oxlint -c .oxlintrc.json src/features/channel-model-batch src/routes/_authenticated/channel-model-batch src/hooks/use-sidebar-config.ts src/hooks/use-sidebar-data.ts
.dev/run bun x oxfmt --check src/features/channel-model-batch src/routes/_authenticated/channel-model-batch src/hooks/use-sidebar-config.ts src/hooks/use-sidebar-data.ts
.dev/run bun run i18n:sync
.dev/run bun run test src/features/channel-model-batch src/features/system-settings/models/__tests__/plugin-pricing.test.tsx
.dev/run bun run build
.dev/run go test ./router -run '^TestChannel' -count=1
.dev/run go test -c -o /artifacts/channel-model-batch-controller.test ./controller
.dev/run go build -o /artifacts/channel-model-batch-gateway .
git diff --check
```

前端 57 项测试通过（本功能 45 项、相关既有供应方定价 12 项）。新增覆盖空／负数价格的字段错误状态、友好浮层、禁止保存及小数修正后恢复。覆盖筛选选择、行内小数价格、站点额度单位换算、缓存价格与请求倍率保留、复杂规则编辑入口、批量供应商与按次统一价、两次预览与保存、冲突保留草稿、非法名称、空/错误状态、长名称分批读取、任务供应方价格展示，以及筛选中编辑模型完成价格时保持输入焦点。

数据库测试为 `TestChannelModelBatchDatabaseMatrix`，通过编译后的真实 Go 测试二进制执行三个子测试，标志为 `-test.run=^TestChannelModelBatchDatabaseMatrix$/^sqlite$` / `^mysql$` / `^postgres$`、`-test.count=1`、`-test.v`。实际版本为 SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11，均通过。MySQL/PostgreSQL 使用本任务新建的独立临时容器、内存文件系统及回环 DSN；未使用共享或生产数据库。覆盖映射链、供应商图标继承、隐藏资料、路由能力同步、整批回滚、资料/价格版本冲突，以及内置供应商只读目录、明确选择时创建、批量复用同一供应商和非法价格回滚供应商创建。没有迁移或数据库驱动变更，不涉及日志库。

独立浏览器回归脚本：`web/src/features/channel-model-batch/__tests__/browser.cjs`，使用 Playwright 和 Chromium；`SILVERMOON_REVIEW_URL` 指向本地静态构建预览，所有管理接口均使用测试响应，不连接真实账户或写入真实数据。以 20 个模型验证 390 / 768 / 1440 / 1920 宽度、页面与弹窗溢出、供应商键盘搜索、行内修改不弹编辑器、选择禁用状态、批量预览及未确认不保存；桌面断言表格起点小于 320px、行高不超过 90px、一屏至少 6 个模型。每个宽度启动独立浏览器；布局和交互断言默认独立运行，可用 `SILVERMOON_REVIEW_SCREENSHOTS=1` 额外保存截图，避免无界面 Chromium 偶发截图超时影响交互回归。新增断言检查用户模型名／供应商／对客定价／上游模型的列顺序、工作台全宽与两侧小边距、紧凑顶部、宽屏下名称两列扩展、价格与上游模型间不保留多余宽度、独立操作列、四个短价格输入同一行、操作列打开高级编辑器及取消不改变价格、两组缓存开关直接可见且宽度适配、小数连续输入、参考价格列和入口移除，以及批量标签和输入框间距；错误价格反馈不增加行高，键盘聚焦可读提示，修正后恢复保存。

日志、测试二进制和浏览器截图保存在本工作区被 Git 忽略的 `build/dev/channel-model-batch-*`。

## 未实测范围

未对真实供应商渠道执行在线报价读取，未使用真实账户执行生产管理保存，也未发起实际计费中继请求。多模型浏览器回归使用接口测试响应，后端保存由上述三种真实数据库验证；另外使用实际本地账号登录隔离 SQLite 预览服务检查当前渠道 3（New API，4 个模型）、实际供应商目录与价格控件，无浏览器运行错误。新增资料隐藏不等同于禁用渠道；正式切换客户模型名应结合渠道当前启用状态安排。

## 本地预览与原型保护

仅更新本任务容器 `silvermoon-channel-model-batch-preview`，回环入口 `http://127.0.0.1:46016/channel-model-batch?channel=3`，源码构建二进制嵌入前端，独立 SQLite 数据位于本工作区 `build/dev/channel-model-batch-local/data`。重建保留数据挂载、环境文件和用户已保存的渠道/映射；当前渠道 3（New API）包含 4 个模型，未复原已移除的渠道 1 / 2 或示例数据。预览中三个历史示例供应商只更正为源码正式名称，保留记录 ID 和关联，不再显示“本地示例”。

认可原型只读核对，文件 SHA256 保持 `d0cdc8dbec0b95d152b8ccae6c8263404eaa7ea706c7fdc2fe18c3d15b4b972a`。新页面样式局限于 `web/src/styles/channel-model-batch.css` 的 feature 选择器，不改原渠道、模型、定价页面或全局主题文件。

最新行内布局截图保存在 `build/dev/channel-model-batch-local/inline-list.png`、`inline-list-edited.png`、`inline-row.png` 和价格区特写 `inline-prices.png`；最新全宽布局另存为 `wide-workspace.png`。刷新嵌入前端的本地预览后，实际渠道 3 在 1920px 视口下，工作台两侧边距均为 8px，顶部渠道卡片高 50px，用户模型名／上游模型列各约 380px，对客价格末端到上游模型列间距为 8px。4 行高度均为 60px，四种按量价格同一行，全部控件位于单元格内。实际账号验收完成基础价格和两种缓存小数价试填，并进入最终保存确认页；没有价格校验错误、浏览器运行错误或批量保存请求。验收输入仅进入临时草稿，关闭浏览器后丢弃；比较更新前的独立备份，channels / models / vendors / options 内容均未改变。上游合同成本和实际中继扣费仍未验证。

2026-11-25 本轮列顺序与价格提示验收截图：`build/dev/channel-model-batch-local/reordered-columns-20261125.png`、`compact-price-warning-20261125.png`。实际账号试填负数后，价格输入标错、检查保存禁用、状态浮层显示友好文案，行高仍为 60px；补全输入／输出及缓存小数价后可进入最终确认，未提交保存。对应日志为 `channel-model-batch-errors-{tests,typecheck,lint,build,go-build,browser}.log` 及 `channel-model-batch-local/check-inline.log`。

2026-11-25 价格输入宽度微调：输入／输出价格框各从 85.5px 增至 105.5px，基础价格组增加 40px，对客定价列同步增至 618px；两个弹性名称列共享宽度调整，在 1920px 下各保留 380px。1440px 桌面名称列最低宽度预算相应由 160px 调整为 140px，仍验证表格不溢出桌面容器、窄屏仅表格内部滚动、四种价格单行及行高不变。实际本地账号验收仍为 60px 行高、无写入请求。新增浏览器断言保护基础价格框至少 104px，已在旧构建上观察到失败；最新价格特写为 `build/dev/channel-model-batch-local/inline-prices-wider-prices.png`，全表截图为 `reordered-columns-20261125-wider-prices.png`。检查日志位于 `build/dev/channel-model-batch-wider-prices-*.log` 和 `channel-model-batch-local/check-inline-wider-prices.log`。

2026-11-25 快捷定价统一表达式：移除 `quickPriceValues` 中继续生成旧倍率／按次单价的分支；旧定价行内编辑复用只读转换接口，合入新价格后统一返回 `tiered_expr`。转换等待期间保留输入并阻止保存，以对应输入草稿为条件应用异步结果，防止较早响应覆盖新输入、改名或已放弃的草稿。无法安全解析的复杂转换保留草稿并要求高级编辑，不回退旧方式保存；未生效的旧表达式不作为新快捷价的请求倍率来源。新增回归覆盖旧按次改价、旧按量的缓存／图片／TTL／请求倍率保留、不支持转换、延迟转换的最新小数输入与放弃草稿；浏览器检查旧按次转表达式后继续比例调价的模式与金额。源码修改仅在独立 feature，复用既有接口及翻译，无数据库／后端契约变更。验证日志为 `build/dev/channel-model-batch-expression-*.log`。

只读后端核验：`build/dev/channel-model-batch-local/check-expression.cjs` 使用实际隔离预览账号，仅将 GET 定价响应中的当前模型临时替换为旧按次／按量测试配置，转换及预览请求调用真实后端。旧按次价转为 `tier("request", fixed(0.03125))`；旧按量测试配置转为含新输入价及原输出、缓存读取、短效／12h 缓存写入和图片单价的表达式，两个模式都通过后端预览，管理保存请求均为 0。浏览器输入与转换夹具不写入已保存配置；按量／按次切换另有回归，旧模式遗留的不生效倍率不会进入新表达式。既有旧字段仅保留作为兼容记录，当前生效模式为表达式，预扣／舍入遵循项目既有表达式规则，单笔舍入可与旧模式不同。本轮未执行实际中继扣费或生产保存。日志为 `channel-model-batch-local/check-expression.log`。


2026-11-26 修复改用户模型名丢失未保存修改：移除改名时清空价格和重置已选供应商的逻辑；单独／批量编辑的供应商、快捷价格、缓存开关、小数及未完成输入、校验状态、高级表达式均随行保留。首次价格编辑保存原始价格上下文，避免先清空输入再改名时丢失缓存 TTL 等其他规则。异步旧价转换沿用原输入的条件校验，完成时保留当前目标名称的价格版本；往返改名保留各名称已捕获的版本，不刷新旧草稿的并发校验。原型和原有渠道／模型定价页面未修改。

本轮验证：修复前新增的三个交互回归均失败，修复后通过；完整 feature 与 model-pricing 测试 8 个文件、114 项通过，typecheck、限定 feature 的 oxlint／oxfmt、前端构建、Go 预览构建通过。1920px 浏览器回归增加批量供应商／价格后改名检查；实际本地渠道 3 验收修改四种小数价格和供应商后改名，所有字段保留并通过真实后端只读价格预览，保存尝试和浏览器错误均为 0。日志为 `build/dev/channel-model-batch-rename-{tests,types,lint,format,build,gobuild,browser}.log` 和 `channel-model-batch-local/check-rename-draft.log`，截图为 `channel-model-batch-local/rename-preserves-draft-20261126.png`。隔离预览更新前已创建当时数据的独立 SQLite 备份；未恢复旧数据、未进行真实管理保存或实际中继扣费。


2026-11-26 修复改名时价格控件闪烁：复用 `useDebounce`，模型名称连续输入停止 300ms 后再查询目标价格，价格和供应商草稿编辑不重启名称查询计时。后台查询不再禁用全表价格；已有价格草稿与已加载的其他模型行保持可编辑，仅没有草稿且尚未取得目标价格的行等待。查询期间新编辑的价格不提前记录“目标无配置”的版本，最终检查仍等待当前名称价格查询完成，确保目标已有价格使用正确版本进行冲突校验。高级设置仅对尚未取得目标价格的行等待。

新增两个交互回归，修复前分别复现已有价格被禁用和连续输入逐次请求，修复后通过；相关 8 个测试文件共 116 项通过，typecheck、限定 feature 的 oxlint／oxfmt、前端和 Go 构建通过。1920px 浏览器回归通过 MutationObserver 确认价格输入没有 disabled 状态闪烁，连续改名只有一次目标价格读取，并验证焦点保持、原表格行没有重建、批量价格与供应商草稿保留。日志为 `build/dev/channel-model-batch-flicker-{tests,types,lint,format,build,gobuild,browser}.log`。更新隔离预览前已备份当时 SQLite 数据。

实际本地渠道 3 浏览器验收也通过：修改四种小数价及供应商后连续输入新模型名，价格控件禁用状态变化为 0，只有一次目标价格读取，焦点和行 DOM 保持，真实后端价格预览成功；保存尝试和浏览器运行错误均为 0。与本轮新备份比对，channels / models / vendors / options 内容一致。脚本及日志为 `build/dev/channel-model-batch-local/check-rename-flicker.cjs` 和 `check-rename-flicker.log`，截图为 `rename-no-flicker-20261126.png`。

2026-11-26 显示名称调整：菜单和页面标题统一改为「批量定价」，复用 `Batch pricing` 翻译键，价格快捷入口同步使用该文案；已同步七种语言。入口路由仍为 `/channel-model-batch`。
