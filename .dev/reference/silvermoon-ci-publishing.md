> 接手快照：2026-11-16，从运维工作区 docs/ 复制。历史相对链接可能指向原运维目录；开发工具以本项目 .dev/README.md 为准。

# Silvermoon AI 自有构建与镜像发布

日期：2026-11-16（Asia/Shanghai）。

代码仓库：https://github.com/halfisnine/silvermoon-ai 。
Docker Hub：https://hub.docker.com/r/vidbin/silvermoon-ai 。
默认分支：`custom/main`。

## 源码与上游

国内源码在 `/opt/snewapi/source`，Instabox 在 `/data/workspace/snewapi/newapi`。
两个仓库保留官方 `upstream`，新增自有 `origin`。Instabox 为首次推送获取了
固定基线之前的完整历史（基线 6,682 个提交），没有升级基线；国内仍保留浅边界。

基线：New API `v1.0.0-rc.49`，提交
`48a6c87f9e7757fdc58e5be9bcb4f8281ad0dfd0`。本次只添加自有构建和发布配置，
未修改业务代码、前端界面、认证和计费逻辑。

上游六个工作流逐字节保留并移至 `.github/upstream-workflows/`，避免新仓库触发
官方镜像发布、Electron、多平台 release 和 GitCode 同步。Dependabot 配置也逐字节
归档到此目录；首次导入自动生成的 5 个依赖升级 PR 已关闭，没有合并依赖变更。原始 Dockerfile、
README、Go 模块路径、署名、LICENSE、NOTICE、第三方许可文件保留。

## 配置入口

- [自有工作流](../source/.github/workflows/silvermoon-build.yml)。
- [自有 Dockerfile](../source/.silvermoon/Dockerfile)。
- [自有项目说明](../source/README.silvermoon.md)。
- [镜像运行检查](../source/.silvermoon/smoke-image.py)。

工作流只使用已经配置的 Secret 名称，不读取或输出实际令牌。Secrets 在发布步骤
注入，Docker 登录使用临时配置目录并在退出时清理。

## 构建与发布规则

| 触发 | 行为 |
| --- | --- |
| custom/main 提交 / 目标为该分支的 PR | 检查和构建，不发布 |
| 自有版本标签 v* | 检查通过后发布 |
| 手动运行 | 默认不发布，只有 custom/main 且 publish=true 才上传 |

Go 1.26.10、Bun 1.4.4、Debian 运行时基础镜像均按既有摘要固定。
apt 安装的软件包未逐项锁定，因此不宣称完全逐字节可复现。
前后端统一为 `v1.0.0-rc.49-custom.<12位源码提交>`。镜像发布标签与应用内
版本分别记录，不修改上游空 VERSION 文件。不生成 latest，按摘要部署。
前端页面从后端状态接口取得版本；镜像内另存前端产物版本文件，并在启动检查中
与后端版本核对，不要求版本字符串出现在前端压缩代码中。

前端冻结安装、类型检查和生产构建；后端依赖校验、根模块 vet、两个独立
模块 build/test、PGO 主程序编译。运行检查使用无外网、无宿主机数据挂载的
临时 SQLite 容器，验证版本、前端页面和待初始化状态。
不执行管理员初始化、真实模型调用，不连接生产数据库。

## 执行记录

首次上传后 GitHub 未注册工作流，手动执行返回 404。随后通过 GitHub Contents
API 更新工作流名称，工作流注册成功；源码工作副本已同步该提交。
首次构建使用手动触发且 publish=false，任务：
https://github.com/halfisnine/silvermoon-ai/actions/runs/43786246055 。

普通提交触发的完整构建已通过：
https://github.com/halfisnine/silvermoon-ai/actions/runs/43786782265 。
源码提交：`2d4a34fee7381d52d6807745c0b7fd784f99b70a`。
已创建自有候选标签 `v1.0.0-rc.49-silvermoon.1`，对应上述提交。
应用版本为 `v1.0.0-rc.49-custom.2d4a34fee738`。
标签触发的发布任务已成功（构建、启动检查、Docker Hub 上传全部通过）：
https://github.com/halfisnine/silvermoon-ai/actions/runs/43787327811 。

已发布镜像标签：

- `vidbin/silvermoon-ai:v1.0.0-rc.49-silvermoon.1`
- `vidbin/silvermoon-ai:sha-2d4a34fee7381d52d6807745c0b7fd784f99b70a`

固定拉取引用：

```text
vidbin/silvermoon-ai@sha256:4fd7579e1e341e388d94387e49e01435df79832a9e803f8cf6ce6f059e09f8bc
```

镜像架构为 Linux amd64；不包含 ARM64 镜像。2026-11-16 在 Instabox 从
Docker Hub 按上述 digest 实际拉取成功，镜像本地展开大小为 324,344,929 字节。
两个镜像标签解析到同一个 digest。OCI 标签的源码仓库、完整提交、应用版本均匹配。

再次执行同一镜像检查脚本通过：CLI 版本、前端产物版本、`/api/status`、首页 HTML
和待初始化状态一致。临时容器无网络、无宿主机业务数据挂载，检查后已删除。
镜像保留在 Instabox 缓存中；没有创建长期运行的 New API 实例。
国内与 Instabox 源码 HEAD 均为上述定制提交，工作区干净，自有版本标签一致。

Instabox 机器可读记录：`build/newapi/silvermoon-release-validation.json`，不提交源码仓库。

构建接入期间修复了两项包装检查：前端类型检查需要同时复制共享 JSON fixture；
前端通过后端接口读取版本，因此改为核对产物版本文件和后端版本。
没有为通过构建而跳过类型检查或更改业务源码。

## 验证边界

没有运行完整根模块和前端测试集，不将 SQLite 启动检查解释为 PostgreSQL/MySQL
兼容性验收。没有更新 Vultr 运行实例。后续正式上线仍需要真实模型、计费和
目标部署环境验证。

## 在 GitHub 查看与手动运行

1. 打开仓库 Actions，选择 **Silvermoon AI build and publish**。
2. 点击某次任务，查看检查、构建、启动验证和发布步骤。普通提交的 Publish image
   步骤显示跳过，这是预期行为。
3. 需要手动检查时，选择 Run workflow，分支选 `custom/main`，不勾选 publish。
4. 需要手动发布该分支时才勾选 publish，生成 `dev-完整提交` 镜像标签。正式候选
   发布优先使用新的自有版本标签，如 `v1.0.0-rc.49-silvermoon.2`，不要覆盖旧标签。
5. 发布成功后，在任务 Summary 查看实际镜像摘要，在 Docker Hub 的 Tags 页面
   查看镜像标签。GitHub 标签触发构建，不会自动创建 GitHub Release 公告。

发布操作由 GitHub 托管的临时 Linux 构建机完成，Instabox 无需持续开机。
本地 Windows 电脑不需要为这条流程安装 Linux 工具链。部署服务器拉取镜像
是单独的后续步骤，当前工作流不会自动更换线上镜像。
