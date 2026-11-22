# MV2-003 真实进程占用与结算验证

执行日期：2026-11-22。执行者：`mv2002_migration_harness`，本工具实现协作者，不替代整项独立验收。

本验证直接调用当前生产模型的 `ReserveMarketingFunding` / `SettleMarketingFunding`，没有模拟账本。协调进程完成真实 `model.InitDB()` 后，启动独立 OS 子进程；每个子进程独立打开 PostgreSQL 和 Redis 连接，以 `IsMasterNode=false` 初始化，避免并发迁移。并发双方通过管道屏障启动，不依靠随机任务或固定休眠碰运气。

**这是资金模型的双进程验证，不是两个完整 HTTP 网关实例的端到端验收。** 管理 API、认证、Relay、Redis 旧缓存与计费入口的最终切换仍由后续 Issue 验证，不能用本结果宣称已经完成004/005/015。

## 隔离和版本

- Compose 项目：`silvermoon-marketing-v2`；没有修改生产或旧预览服务。
- PostgreSQL：17.11，数据库固定 `marketing_v2_funding_process`。
- Redis：7.2.17，同一个服务、同一逻辑库15，所有进程使用同一测试 CryptoSecret。
- Go：通过 `.dev/run` 固定工具链 1.26.10 编译；运行容器限制 2 CPU / 3 GiB。
- 只创建合成用户/令牌及可信 `OriginState=verified` 的新账户。付费资金20、赠金30、令牌可消费100均为内部测试单位；通过生产入账/赠金方法创建，不导入旧营销数据。
- `common.BatchUpdateEnabled=true`，RedisEnabled 保持 true，确认模型直接依赖数据库事实，不误入旧缓存扣款或延迟写队列。

## 已执行和观察

```sh
python3 .dev/marketing-v2-requirements/validation/funding_process.py all
```

退出码0。六组场景均通过，结尾为 `FUNDING_PROCESS_MATRIX_OK`。

| 场景 | 实际验证结果 |
| --- | --- |
| 高估的旧缓存 | Redis真实写入用户和令牌各1000000的旧额度，两独立进程争抢数据库仅剩50，只有一个成功；消耗50后新请求1被拒绝；旧缓存数值未被模型修改 |
| 缓存过期 | Redis对真实缓存执行过去时间的EXPIREAT，确认键已不存在，然后同样只有一次成功占用，耗尽后不能重新消费 |
| Redis连接故障 | 两进程实际关闭各自Redis客户端，并确认PING报错；RedisEnabled仍为true，占用/结算仍以PG为准，仅一个成功，不能重复使用资金；未停止Redis服务 |
| 同请求、同结算并发 | 两独立进程使用同请求键，得到同一资金事实；相同版本结算并发只写一次结算事实，实际消费35＝赠金30＋付费5，剩余付费15 |
| 提交成功但回复丢失 | 真实worker提交占用或结算后直接退出73，不写结果；新进程重试返回原结果，既不多占也不重复扣款 |
| 未提交事务进程崩溃 | worker已经执行实际占用写入后不提交；第二进程尝试相同请求，先从PG观察其wait_event_type=Lock，再SIGKILL持锁worker；PG回滚未提交内容，等待者成功占用，最终消费40＝赠金30＋付费10 |

每组检查数据库付费余额、赠金剩余/已消费、付费和赠金占用之和、令牌总占用、令牌已消费/余额、资金事实数量以及实际消费流水总数值。并发重复结算另外确认只存一条结算版本记录。

本轮真实并发进程示例：高估缓存场景OS PID 12、16，各自PG backend 9527、9528；事务中断场景OS PID 102、108，PG backend 9555、9556。杀进程前实际观测输出：

```text
POSTGRES_LOCK_OBSERVED blocked_backend=9556 owner_backend=9555
SCENARIO_OK sigkill_uncommitted_transaction_rolled_back waiting_process_recovered
FUNDING_PROCESS_MATRIX_OK two_real_worker_processes shared_postgresql shared_redis_db_15
```

原始日志：本工作区 `build/dev/marketing-v2-validation/funding-process.log`。

## 重现与边界

`funding_process.py build` 只构建；`run` 重建固定的资金进程测试数据库并运行；`all` 执行二者。不操作其他数据库，不清空整个Redis逻辑库。该PG数据库为一次性独立验收数据；不可在同一库并行运行两次此脚本。

本结果验证了关闭客户端时真实Redis命令失败，以及真实键过期；没有模拟网络分区，也没有声称完整服务在Redis整体停机期间已经完成认证和Relay端到端验收。未知用量、资格边界、金额安全以及其他数据库行为由003主行为测试覆盖。

## 本轮指纹

| 对象 | SHA-256 |
| --- | --- |
| model/marketing_v2.go | b12aaca1bc81397fa08c6e928929ab25d4d927d4d06f24f1bf21ba91fff31f27 |
| model/marketing_v2_funding.go | 35c049ff93ced2b41c48bd811aad3cab760a9ee9234f27ae88948319b22e5c49 |
| funding_process/main.go | 1ed00a041f623a0f9e4f73cd22d34c7cc4254f78f45d438764d1f9c1f8b9c94f |
| funding-process executable | adf692f17e387039aae04c9d27c0b23c2236ef40b95c9b49d32219ccead57f2d |

模型修正后须重新构建运行，不能将本轮指纹当作后续版本已经验收的证据。

## 独立首轮失败后的修复复测

首轮发现及修复详见reviews/MV2-003-review.md。修复后已完整重新编译运行三库行为、真实双进程、真实主分支基线升级和vet。此轮原始日志build/dev/marketing-v2-funding-{repair1,process-repair1,migration-repair1}.log，未替代后续独立整项验收。最新指纹：

| 文件/产物 | SHA-256 |
| --- | --- |
| model/marketing_v2.go | f9d559cdf63306c92d61cd27cc9265945235987659736f30b068f4b1cf9dfeb9 |
| model/marketing_v2_funding.go | 4d895d9ed06c26ceb24f856f4d3f694dbcf8184654aa02aa2e381d6d18e2591a |
| model/marketing_v2_test.go | a6bc2ea9d1664b558a4019d72ac6e2ddc67d8a1d1e915d9cf7f110deb94816ab |
| build/dev/marketing-v2-model.test | bf274661e04da27df66178de68b50db3ab500fd05c021191673a98287279334e |
| build/dev/marketing-v2-validation/funding-process | b3d7d0e8c8f8470862c347e0e3ff22e1e2d574daef84c3e4225bdadb62edd1ae |
| build/dev/marketing-v2-validation/candidate-probe | 86922096222cdbd07e49a60be0866b691ed613e79e958681e61a4cf4a749da57 |
