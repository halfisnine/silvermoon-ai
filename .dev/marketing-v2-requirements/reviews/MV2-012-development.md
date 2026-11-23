# MV2-012 管理员营销配置开发记录

2026-11-23，基于已验收提交 `7acc948`。状态：实现已完成，最终验证及独立整项验收中。未修改冻结 A01–A06；未提交、推送或部署本 Issue。

## 实现与范围

新增管理员营销活动、独立奖池、奖励记录三个入口，改造原付费兑换码管理页。四类活动统一使用服务端已实现的规则、库存和账本，不复用旧营销系统的审批、激活、赠金预算、模型分组限制或旧数据迁移。

注册即时发奖；签到同一活动内配置每日与连续额外奖、三种参与条件、按活动累计补签次数；充值按实际人民币门槛区分首笔、单笔、累计和频率，累计不允许比例赠金；邀请只允许活动内新关系，好友分别累计，比例按实际人民币。赠金和机会可组合，机会按名称关联独立奖池。互斥组按稳定名称选择/新建并查询组内成员与优先级，未增加改名操作。

奖池配置真实赠金奖项、组合赠金、明确可选空奖、权重、库存和有限发行名额。核心配置开始后锁定，复制新对象不转移旧资格；补库存、追加、延期独立操作。服务端仍校验开始前既有供给义务，空奖不增加实际赠金供给；延期不恢复已经失效或清退的机会。

付费码没有赠送码模式。分别登记人民币实付金额、到账显示金额、销售渠道/批次/凭据；查看兑换者名称和真实来源，已兑记录只读。支付范围为人民币易支付与小店付费码，当前 1:1 读取站点设置，不写死兑换常数。

## 界面与组件复用

复用项目 `SectionPageLayout`、`DataTablePage/useDataTable`、移动列表、`Dialog`、`ConfirmDialog`、原兑换码抽屉、`CopyButton`、加载/错误状态、`Form`、`FieldGroup`、`NativeSelect` 等。活动奖励和奖项通过平铺字段及分隔线组织，没有复制原型 HTML 或引入新组件库。

`DateTimePicker` 的现有能力缺口是不能接收表单 label/ref/aria/disabled，且固定显示短横线日期。以兼容 props 扩展日期展示和可选固定时区，不新增一套日期选择器；原调用不传时区时仍保持本地时区。新增营销调用明确使用同一站点时区，中文显示年月日，不在页面暴露时区文案。

服务端返回已换算的十进制金额字符串。公共货币库增加明确币种的精确字符串格式化，避免当前配置二次换算和微小非零金额显示为零。表单保留字符串及打开时的金额版本/记录版本；同币种换算设置变化也拒绝旧表单。临时网络失败沿用同一操作键，修改内容后换键；校验失败修正可以再次保存。

奖励记录按名称/用户/日期筛选，详情读取真实充值、卡密销售、好友、签到日、奖池和实际发奖/到期事实，不回显可用卡密、内部种子或原始 quota。未中奖不显示到账；待处理不编造到账/到期时间。

## 服务端与数据库

管理读写每次重新核对当前数据库管理员身份，幂等成功重放也不能绕过撤权。复用既有 dashboard session 授权；仅 Cookie 不构成管理写入授权，PAT 不自动获得新管理路由权限。金额配置与当前时间、操作者由服务端生成。活动和奖池操作绑定稳定操作键、请求内容和记录版本。

新增活动互斥组查询投影列及索引，创建/更新同步，迁移按批幂等回填新版活动已有规则；不扫描/导入旧营销体系。三库行为由真实实例验证，MySQL 字符串排序规则与其他数据库的差异另外回归，不能只靠 SQLite 或 mocks 推定一致。

## 验证记录（验收前阶段）

- 日期表单 label/error 回归先失败再修复：`mv2-012-date-red.log` → `mv2-012-date-green.log`；后续覆盖时区与切换语言。
- `.dev/run bun run test src/features/marketing-admin src/features/redemption-codes src/components/__tests__/datetime-picker-time.test.tsx`：阶段 r1 共9文件67项通过，`build/dev/mv2-012-ui-r1.log`；最后补充打开表单金额版本快照及文案后正在全量重跑 r2。
- `.dev/run bun run typecheck`：r2，1457文件、2项目无错误，`build/dev/mv2-012-typecheck-r2.log`。
- 受影响59文件 lint：r4零警告/零错误，`build/dev/mv2-012-lint-r4.log`。
- `.dev/run bun run build`：r1退出0，`build/dev/mv2-012-build-r1.log`。生产分包构建完成，不等同于浏览器或真实付款验收。
- 306个新键完整提供七语，动态 schema/后端文案登记；通过规定脚本写入并 `i18n:sync`，`build/dev/mv2-012-i18n-sync-r2.log`。没有手工直接修改 locale。
- 后端真实三库管理接口、付费码与迁移及计费回归见后端最终验证记录；最后查询大小写修复的最终重跑尚待绑定。
- 浏览器将使用当前生产构建与明确标注的网络夹具核验 UI，不将模拟网络视为真实付款/数据库端到端验收。

最终完整测试、浏览器、数据库证据与独立验收报告完成后补齐结果，并且仅全部通过才更新 Issue 状态并本地提交。

## 独立验收修复（最终验证中）

- F01：付费码创建/编辑原来直接跟随 React Query 最新货币版本，后台刷新可能改写金额含义或覆盖未保存输入。新增按本次打开目标冻结的币种/版本快照，关闭或切换目标重新初始化；关闭窗口聚焦/重连自动刷新。明确点击刷新才重读并重置表单。回归两例在真正触发重新渲染后稳定失败，证据 `mv2-012-paid-snapshot-red-r2.log`；此前只改变缓存但未等待订阅通知的测试假绿已修正，不以该次结果证明有效。
- F02：短有效期原来按小时保留两位小数，6秒显示0小时。活动/奖池详情改共用精确时长格式化，整除时按日/时/分，否则按秒。`Intl.NumberFormat` 的长单位支持七语单复数；普通数字格式器不提供单位格式，因此仅此单位格式使用 Intl，语言先通过项目映射。没有新增翻译键。中文6秒合法显示“6秒钟”，已获独立评审认可；所有列表/详情仍保留“有效期”标签。
- 首次完整包检查出现语音WebSocket deadline与总超时，日志 `mv2-012-packages-r1.log` 保留。隔离原失败集在不改代码/断言/timeout的情况下通过，后续完整包串行重跑排除测试机资源竞争；在最终通过前不标记整项完成。
- 首轮浏览器覆盖4路由×3主题×桌面/手机，以及独立真实gateway登录、规则保存/重新读取和实际奖励。模拟网络仅作布局/前端交互证据；真实组使用独立SQLite及真实管理/注册/兑换/签到HTTP，不以替身替代账本。首轮报告按stage保存，待最终构建后重新绑定复核。

## 修复后完整自动检查

- `.dev/run bun run test --maxWorkers=1 src/features/marketing-admin src/features/redemption-codes src/components/__tests__/datetime-picker-time.test.tsx`：10文件84项全部通过，`build/dev/mv2-012-ui-r3.log`。包含后台缓存刷新不改表单/换算、显式刷新重新加载、六秒/分/小时/天、语言切换、七语与非法语言回退。
- `.dev/run bun run typecheck`：1458文件2项目零错误，`build/dev/mv2-012-typecheck-r3.log`。
- `.dev/run bun x oxlint -c .oxlintrc.json src/features/marketing-admin src/features/redemption-codes src/components/datetime-picker.tsx src/components/__tests__/datetime-picker-time.test.tsx src/lib/currency.ts src/hooks/use-sidebar-data.ts src/hooks/use-sidebar-config.ts src/routes/_authenticated/marketing-admin`：59文件零警告零错误，`build/dev/mv2-012-lint-r6.log`。
- `.dev/run bun run build`：退出0，4分37.4秒，`build/dev/mv2-012-build-r2.log`。后续真实浏览器使用此构建重编gateway，首轮旧构建截图仅留作stage。
- `.dev/run go test -p 1 ./controller ./service ./model ./middleware ./router ./internal/marketing -count=1 -timeout=5m`：全部六包通过，容器最终退出0；`build/dev/mv2-012-packages-isolated-r2-container.log`及`build/dev/mv2-012-packages-isolated-r2-exit.txt`。原失败与外层会话退出事件见后端验证记录，未提高timeout或更改业务/断言。

## 最终构建真实浏览器复验

独立gateway重编后，实际提供入口JS/CSS与最终dist逐字节一致；index只有原有analytics注释替换差异。证据 `build/dev/mv2-012-browser/final-gateway-evidence.json`、`dist-final-manifest.json`，gateway SHA256 `82341b3b8c1c3d31263d063ec714dc2009f1d7d3ce43c3195ca43a7482f986a0`。

`final-real-report.json`：16项真实HTTP/浏览器检查通过，零应用runtime错误。真实注册/付费卡密兑换/签到生成的来源记录可在桌面与手机详情查阅；保存3天有效期实际为259200秒并重新打开保持一致。API建立六秒活动/奖池后，只读界面正确显示“6秒钟”。打开付费码表单后，在独立站点将换算3调整为4，旧版本提交被拒绝且0.00012输入保持；显式刷新加载新版本后保存成功，测试finally恢复3。测试执行期间未使用API拦截、替身账本或生产数据。

父agent人工检查最终390px六秒奖池详情、换算变更拒绝画面，确认有效期和小额清晰、错误提示有翻译、主题背景保留。七语306新增key均存在且非空，`build/dev/mv2-012-i18n-final.json`。

最终三主题浏览器布局矩阵 `final-report.json` 已完成：4路由×3主题×1440/390px共24页面，8交互组，零应用runtime错误。网络夹具只验证界面边界，包括加载失败重试、空状态、键盘焦点、长奖项滚动及小额/长文案，不替代上述真实gateway证据。最终报告明确绑定build-r2与dist index哈希；stage结果保留。
