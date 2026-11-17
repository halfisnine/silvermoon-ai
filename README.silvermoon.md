# Silvermoon AI 构建与发布

本仓库基于 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 二次开发。
官方说明、署名、LICENSE、NOTICE、THIRD-PARTY-LICENSES.md 和 Go 模块名保留。
当前变更仅涉及构建与发布，没有修改业务功能。发布通过不等于真实模型调用、
计费、多数据库兼容性或生产容量验收。

- 自有源码仓库：`halfisnine/silvermoon-ai`；主分支：`custom/main`。
- 镜像仓库：`vidbin/silvermoon-ai`，当前发布 Linux amd64。
- 上游基线：`v1.0.0-rc.49`，提交见 `.silvermoon/source.lock.json`。
- 工具链：Go 1.26.10、Bun 1.4.4，容器镜像按摘要固定。
- 前后端构建使用同一个 `v1.0.0-rc.49-custom.<12位源码提交>` 版本，不改上游 VERSION 文件。
  当前前端页面从后端状态接口取版本，不要求版本字符串出现在压缩 JS 中；
  前端产物版本另记在镜像内 `/usr/share/silvermoon/frontend-version` 并在运行检查时核对。
- 保留 PGO 编译和上游运行时依赖。原始 Dockerfile 保留，自有构建使用 `.silvermoon/Dockerfile`。

## GitHub Actions

`.github/workflows/silvermoon-build.yml` 是当前自有构建发布工作流。
上游六个工作流完整保留在 `.github/upstream-workflows/`，移出自动扫描目录，
避免触发官方镜像、Electron、多平台 release 和 GitCode 同步。未来升级需逐项审查。
上游 Dependabot 配置也保存在该归档目录，暂不自动提出依赖升级，依赖变更需显式审查。

| 触发 | 检查与构建 | 上传 Docker Hub |
| --- | --- | --- |
| custom/main 提交、目标为该分支的 PR | 是 | 否 |
| 自有版本标签 v* | 是 | 是 |
| 在 custom/main 手动运行 | 是 | 仅勾选 publish 时 |
| 在 codex/* 功能分支手动运行 | 是 | 仅勾选 publish 时，上传 candidate-完整提交 标签 |

不要推送官方版本标签来触发自有发布。正式发布使用新的自有版本标签，不移动旧标签。
合并前的验证使用功能分支手动候选构建，不需要创建版本标签或先合并主分支。
GitHub 仓库需设置 `DOCKERHUB_USERNAME`、`DOCKERHUB_TOKEN`。
普通检查不注入这两个发布凭证，流水线不登录或修改生产服务器。

检查包括：冻结前端依赖、类型检查、银月首页功能测试、前端生产构建、Go 依赖校验、根模块 vet、
两个独立模块 build/test、PGO 主程序构建及运行镜像检查。
运行检查使用无网络、无宿主机数据挂载的临时 SQLite 容器；只读取健康状态、
前端和初始化状态，不初始化管理员、不调用模型。没有运行完整根模块与前端测试集，
不等价于 PostgreSQL/MySQL 业务验收。

正式发布生成版本标签与 `sha-完整提交` 标签；功能分支候选只生成 `candidate-完整提交`，不生成 latest。镜像标签仍可被仓库写入者
覆盖，部署时使用任务摘要中记录的 `vidbin/silvermoon-ai@sha256:...`。

## 合并前验证候选镜像

1. 推送 `codex/*` 功能分支。仅推送该分支不会自动构建或发布镜像。
2. 在 GitHub Actions 打开 **Silvermoon AI build and publish**，点击 **Run workflow**，
   在分支下拉框中选择该功能分支，勾选 `publish` 后手动运行。不要选择 `custom/main`。
3. 等待测试、构建、镜像启动检查和 Docker Hub 上传全部成功。
4. 从该次运行的 Summary 复制候选标签和 digest，以 `docker pull vidbin/silvermoon-ai@sha256:...`
   拉取这次构建的确切镜像，在独立本地测试配置、数据库和 Redis 中验证。
5. 验证通过后再合并 `custom/main`，随后另行执行正式发布。候选工作流不会合并分支或部署服务。

功能分支候选标签示例为 `vidbin/silvermoon-ai:candidate-<40位提交SHA>`；实际 SHA 以运行摘要为准。
未勾选 `publish` 时仍执行检查与构建，但不会登录 Docker Hub 或上传镜像。

## 本地构建

在源码根目录执行，按当前提交为前后端提供一致版本：

```bash
CUSTOM_VERSION="v1.0.0-rc.49-custom.$(git rev-parse --short=12 HEAD)"
docker build --platform linux/amd64 -f .silvermoon/Dockerfile \
  --build-arg CUSTOM_VERSION="$CUSTOM_VERSION" -t silvermoon-ai:dev .
python3 .silvermoon/smoke-image.py silvermoon-ai:dev "$CUSTOM_VERSION"
```

此示例适用于干净提交；正式镜像以 CI 的已提交源码为准。Debian 基础镜像摘要已固定，
apt 安装的运行时软件包仍受仓库更新影响，因此不宣称整个构建逐字节可复现。
源代码、容器构建文件和许可证应随每次发布保留，遵守上游许可证要求。
