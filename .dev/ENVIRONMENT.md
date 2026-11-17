# 银月独立开发测试环境

本环境从本仓库源码构建，包含已保存的 V1.2 前端。它与服务器上已有服务使用不同的容器、网络、凭据和持久化卷，不读取旧运维项目配置，不导入生产账号或数据。

## 入口

- 完整构建预览：`http://127.0.0.1:43000`
- 前端即时刷新开发入口：`http://127.0.0.1:45173`，API 仅代理至本环境的 `app:3000`。
- 首次启动为空库，访问后按系统初始化页面创建测试管理员。不要使用生产账号密码或上游密钥。
- up/rebuild 会给空白或上游默认品牌配置填入站点名称“银月”和 `/silvermoon/moonlight.webp` Logo。初始化页、首页、浏览器标题和图标共用已有品牌配置；保留上游版权署名。后续在后台自行配置的非默认品牌不会被启动脚本覆盖。
- 两个端口都仅监听服务器回环地址。远程浏览器需要 Codex 端口转发，或通过 SSH 转发上述端口；没有配置公网域名、生产反向代理或 HTTPS。

例如在自己的电脑运行 `ssh -N -L 43000:127.0.0.1:43000 -L 45173:127.0.0.1:45173 <你的服务器SSH地址>`，再打开本机对应地址。HTTP 仅适用于这个本机/SSH 开发入口；若今后开放外网，应单独配置 HTTPS 和正式的受信任浏览器来源。

## 隔离方式

| 服务 | 版本或来源 | 数据与网络 |
| --- | --- | --- |
| app | 当前源码经 `.dev/run` 编译；固定 Go 工具镜像运行 | `app_data`，storage + application 网络 |
| web | 固定 Bun 工具镜像，现有 Rsbuild 开发服务器 | 本仓库 web 与独立依赖缓存，仅 application 网络 |
| postgres | PostgreSQL 17.11，按摘要固定 | 独立 `postgres_data` 卷、`silvermoon_dev` 数据库，仅 storage 网络 |
| redis | Redis 7.2.17，按摘要固定 | 独立 `redis_data` 卷，AOF 持久化，仅 storage 网络 |

所有资源名称以 `silvermoon-ai-dev_` / `silvermoon-ai-dev-` 开头。数据库和 Redis 不发布主机端口；storage 网络为 internal。没有 external 网络、external 卷、host 网络或 Docker socket 挂载。日志和业务表均写入本环境数据库；没有独立连接外部日志数据库。

基础镜像可以复用 Docker 的只读镜像缓存，这不等于复用运行中的数据库、Redis 实例或数据。运行时使用固定开发工具镜像，避免依赖旧业务镜像；这是开发环境，不是生产精简镜像发布流程。

数据库密码、Redis 密码、会话密钥和加密密钥分别随机生成于 `.dev/private/`。目录权限 0700，凭据文件 0600，已由 Git 忽略。Redis 配置文件因服务降权读取需要为 0644，但宿主机父目录仍为 0700，且只挂载至专属 Redis 容器。重复 init/up 不轮换既有密钥。

不要单独删除私有配置再继续使用旧数据卷，否则会丢失数据库访问或已加密数据所需的密钥。移机备份应同时保留私有配置与开发数据卷，不能只拷贝源码。该环境没有复制生产数据，也不依赖生产凭据。

## 管理命令

在项目根目录执行：

```bash
.dev/stack init                  # 首次生成私有配置；已有密钥保留
.dev/stack brand                 # 为已运行的开发环境补齐默认银月品牌配置
.dev/stack build                 # 冻结依赖安装、构建前端、编译后端
.dev/stack up                    # 首次无二进制会构建，然后等待四个服务健康
.dev/stack status                # 查看本项目状态
.dev/stack logs app web          # 查看本项目日志
.dev/stack stop                 # 停止本环境，保留所有数据
.dev/stack start                # 再次启动，等待健康
.dev/stack rebuild              # 重新构建并重建 app/web；保留数据库和 Redis
python3 .dev/verify-stack.py     # 检查健康、连接隔离、卷和网络隔离、两入口与银月图片
```

编辑前端源文件时，45173 入口自动刷新；43000 的内嵌前端在重新构建后才变化。修改 Go 代码需执行 rebuild。主程序通过原子替换更新，避免覆盖正在运行的可执行文件。

脚本固定 Compose 项目名、配置文件及项目路径，清除外部 COMPOSE_* 变量，避免误操作其他栈。没有提供 down -v 或清空数据快捷命令。操作前应确认当前工作目录和分支。

首次迁移到其他服务器，按 `.dev/toolchains.lock.json` 和 `.dev/services.lock.json` 准备固定摘要镜像；默认使用已缓存的国内镜像，`DEV_IMAGE_SOURCE=official` 切换官方镜像入口。启动不自动拉取或升级镜像。

## 资源限制与检查范围

app 限制 1 CPU / 768 MiB，PostgreSQL 0.5 CPU / 512 MiB，Redis 0.25 CPU / 128 MiB，前端开发服务 1 CPU / 1536 MiB。Redis 使用 64 MiB 上限和 noeviction，日志每容器最多 3 × 10 MiB。构建继续使用 `.dev/run` 的 2 CPU / 3 GiB 限制；同机应避免并行多个完整构建。

本次仅增加开发环境编排，没有修改业务数据库模型、驱动、迁移或认证流程。实际验证对象为 PostgreSQL 开发环境；不将其描述为完成应用的 MySQL / SQLite / PostgreSQL 全矩阵业务兼容性验收。

具体启动与持久化检查结果见同目录 `environment-verification.json`。没有发布镜像、修改生产服务或推送到远端。
