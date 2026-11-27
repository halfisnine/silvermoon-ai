# 批量定价与失败退款统一预览（2026年11月27日）

本次只做本地提交、合并、验证和指定开发预览替换；没有推送、发布镜像或部署生产。

## 提交及完整性

- 来源工作区 `/opt/dev-workspace/silvermoon-ai-channel-model-batch`，分支 `codex/channel-model-batch`。
- 已将原23个修改文件及2个未跟踪文件独立提交为 `ab53c93`（基于 `9480506`）。提交前后25份文件SHA256一致；来源工作区干净，没有reset、clean或丢弃修改。
- 原失败退款修复 `a4c46a6` 保留；合并到 `codex/billing-failure-refund`，合并提交 `187f81c`。没有文件冲突；两提交均为其祖先，退款提交涉及的24个文件内容未变。
- 导入的共享MultiSelect兼容扩展和业务EndpointSelect沿用已有组件；未额外新增依赖、数据库结构或中间件。
- 二进制及内嵌前端基于 `187f81c`，本记录仅补充验证结果。

## 合并版本验证

固定工具链Go 1.26.10、Bun 1.4.4。以下命令在本次合并后的工作区执行并通过：

| 检查 | 命令及结果 |
| --- | --- |
| 前端测试 | `.dev/run bun run test src/features/channel-model-batch src/components/multi-select src/features/gift-management`，实际匹配7个文件72项通过；最后一个路径未匹配现有模块，随后按下行真实路径补验 |
| 赠金及原定价回归 | `.dev/run bun run test src/features/marketing-admin/gifts src/features/system-settings/models/__tests__/plugin-pricing.test.tsx`，3个文件20项通过 |
| 类型及Lint | `.dev/run bun run typecheck`；`.dev/run bun x oxlint -c .oxlintrc.json src/features/channel-model-batch src/components/multi-select.tsx src/components/multi-select/__tests__/selection.test.tsx`，通过 |
| 格式 | 导入的14个TS/TSX/CSS/CJS文件复制到临时目录，按项目规则剥离受保护版权头后oxfmt检查，通过；没有修改源文件 |
| 构建 | `CUSTOM_VERSION=preview-unified-187f81c .dev/run bun run build`；`.dev/run go build -trimpath -ldflags '-s -w -X github.com/QuantumNous/new-api/common.Version=preview-unified-187f81c' -o /artifacts/unified-preview/new-api-187f81c .`，通过 |
| 真实三库保存 | `.dev/run go test -c -o /artifacts/unified-preview/controller.test ./controller`，`python3 build/dev/unified-preview/database-matrix.py`，SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11均通过，无跳过；覆盖显示/隐藏、端点保存/清空、保留旧配置、无效输入及版本冲突回滚；使用测试栈隔离库 |
| 双实例失败退款 | 编译验证harness；`python3 .dev/marketing-v2-requirements/validation/gateway_process.py failure`，11组通过，包含用户提供的quota_exhausted 503、赠金/混合/付费来源、网络及空响应、重试、到期赠金、异步损坏回执；断言原占用释放、来源账目、下一次通过另一实例成功 |
| 双实例综合 | 同runner的 `run` 通过，真实网关共用PostgreSQL和Redis DB14；覆盖并发、Realtime按轮、资金耗尽、异常保留、计价回退、文本/流式/图片/异步接管；外部模型为可控夹具，不宣称已实测全部商业供应商 |
| 浏览器真实保存刷新 | `NODE_PATH=/tmp/silvermoon-visual-check/node_modules node build/dev/unified-preview/browser-check.cjs`，在现有预览SQLite备份的临时实例执行；单行开关及搜索多选保存刷新、批量显示及端点保存刷新、批量隐藏及自动端点清空刷新均通过，共3次真实保存；390/1440/1920宽度无页面横向溢出、行高60px、无页面异常 |
| 更新后预览 | 同目录 `preview-smoke.cjs` 通过，新控件可见、0次保存请求；状态及页面HTTP200，6个入口JS/CSS与本次构建逐一SHA256一致 |

测试日志、结果JSON及临时验证脚本位于忽略目录 `build/dev/unified-preview/`；双实例完整日志位置见其中 `gateway-failure.log` / `gateway-functional.log`。两组测试二进制SHA256均与预览完全一致：

`0a5ba65afcfd1f7c7418df24db8bbe0e21a6dc9d4ed0b40f4811df7bf5c85c0b`

临时浏览器实例首次固定端口已被占用，未操作占用者，改用Docker动态回环端口后通过。该临时实例验证结束后移除，数据库副本和证据文件保留。

## 统一预览与备份

- 分支：`codex/billing-failure-refund`；代码合并提交：`187f81c`；版本：`preview-unified-187f81c`。
- 容器：`silvermoon-channel-model-batch-preview`；服务端入口：`http://127.0.0.1:46016/channel-model-batch`。
- 沿用既有SSH转发时，公司电脑入口：`http://localhost:62021/channel-model-batch`。该地址依赖原SSH转发保持连接。
- 只替换二进制挂载，保留原数据目录、账号、环境、网络、端口、安全及资源限制。
- 更新前停止指定预览并复制完整data目录及容器配置。备份：`/root/.codex/worktrees/marketing-v2/silvermoon-ai/.dev/private/unified-preview/20261127-212055`。
- 保留的旧容器：`silvermoon-channel-model-batch-preview-backup-20261127-212055`（停止状态）。
- 更新前后36份营销业务表/用户资金快照一致，用户/令牌/渠道及营销表行数不变；另核对channels/models/vendors/options/abilities/tokens逐行摘要完全一致。未恢复旧数据、清除测试记录或修改现有账户账务。
- 现有历史待核对请求没有自动批量退款；本次验证使用隔离测试账号和独立数据库。历史账户账目须按既有核对流程处理。
