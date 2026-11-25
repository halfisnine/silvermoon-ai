# MV2-002 三库迁移验证证据

执行日期：2026-11-22。执行者：`mv2002_migration_harness`（实现协作者，不替代独立验收）。

本工具针对本轮新增的主业务数据库表，不连接生产、不执行支付、不启动 HTTP。
独立 Compose 项目为 `silvermoon-marketing-v2`，未发布宿主机端口；测试数据使用 tmpfs。
工具构建统一调用本仓库 `.dev/run`，固定 Go 1.26.10，每个构建容器限制 2 CPU / 3 GiB。

## 实际运行版本及结果

| 数据库 | 实际版本 | 干净新库 | 真实基线升级 | 重复启动 | 历史数据、索引、唯一约束 |
| --- | --- | --- | --- | --- | --- |
| SQLite | 3.50.4 | 通过 | 通过 | 通过 | 通过 |
| MySQL | 8.4.13 | 通过 | 通过 | 通过 | 通过 |
| PostgreSQL | 17.11 / Debian 17.11-1.pgdg12+2 | 通过 | 通过 | 通过 | 通过 |

基线确切为 `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`，通过 `git archive` 导出到本工作区缓存，再将同一迁移探针编译进该基线。不是用当前模型生成一个数据库并称作旧版数据库。基线及候选均调用真实 `model.InitDB()` 完整启动迁移。

升级路径：基线首次启动 → 写入独立 User / Token / Redemption / TopUp 合成记录 → 基线再次启动并核对 → 候选首次升级 → 候选再次启动并核对。

新库路径：候选首次启动 → 写入同类合成记录 → 候选再次启动并核对。

快照核对上述四类记录的序列化字段、用户认证版本，以及四张历史表的索引名称、列顺序及唯一属性；独立事务尝试重复用户名、令牌键、兑换码键、充值订单号，确认数据库仍拒绝重复写入。候选必须存在六张新营销表，基线必须不存在。这些验证不代表 MV2-002 的资金行为、事务回滚和并发验收已通过，后者由 `model/marketing_v2_test.go` 验证。

本次仅增加主库营销表，未修改日志表或独立日志库路径，不将本结果宣称为 ClickHouse 验证。

## 已执行命令

在本工作区根目录依次执行：

```sh
python3 .dev/marketing-v2-requirements/validation/migration_matrix.py prepare
python3 .dev/marketing-v2-requirements/validation/migration_matrix.py build-baseline
python3 .dev/marketing-v2-requirements/validation/migration_matrix.py seed
python3 .dev/marketing-v2-requirements/validation/migration_matrix.py build-candidate
python3 .dev/marketing-v2-requirements/validation/migration_matrix.py verify
```

各命令退出码均为 0。`seed` 在三库各运行两次基线启动；`verify` 在三库各运行两次新库启动及两次升级启动，共输出 12 条候选 `MIGRATION_PROBE_OK`。

全量重现可运行 `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`。
**prepare/all 会重建该隔离项目内固定的 `marketing_v2_fresh`、`marketing_v2_upgrade`、`marketing_v2_behavior` 测试数据库，不可与行为验收同时执行。** 单独运行 verify 要求 fresh 库尚未写入种子；修改候选后完整重跑应使用 all。

原始日志与快照位于本工作区 `build/dev/marketing-v2-validation/`（构建产物不提交）。主要日志为 `<engine>-fresh-candidate-*.log`、`<engine>-upgrade-candidate-verify.log`；升级 verify 日志包含两次启动输出。日志中的预期唯一冲突是探针主动触发的约束检查。

## 本次候选指纹

记录时父提交为 `d597245ac2193443823edf214996516e9bb2c294`，模型仍处于工作区待验收状态。

| 对象 | SHA-256 |
| --- | --- |
| model/main.go | c57dca9d6dabdb5f4daea08d2ffbaaede1e6509d5316147f99d3e688e5fb60c2 |
| model/marketing_v2.go | beac987134a4829079eed1561ac0778e8796f7beee8d8e2ef5bfe87f7a8b1f70 |
| migration/main.go | 39b4bdb4288e8dc822678b8e84cedc99b4c39e9cf83cdb9d405375242c49551b |
| baseline-probe | af11d4b84ba10956bb900ee508bda5e427c92a291da00bdb6888a7e6722c452e |
| candidate-probe | b9de9b370c2330414b57f7dc7aaa9994b87feb371faaddf8a90a864525d64b31 |

若模型或迁移代码发生变更，须重建并重跑相关迁移，不可沿用本次指纹作为最终通过证据。

## 第二轮：首充字段及当前锁读修复后

完整重跑 `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`，退出0。三库重新建库，真实基线seed及二次启动、候选新库和升级各两次全部通过。日志汇总build/dev/marketing-v2-migration-round2.log，详细探针日志同前。上文保留首轮历史，本轮以以下指纹为准：

| 对象 | SHA-256 |
| --- | --- |
| model/main.go | c57dca9d6dabdb5f4daea08d2ffbaaede1e6509d5316147f99d3e688e5fb60c2 |
| model/marketing_v2.go | 2ff4f19897fafa122997c8ffcd8a7f29e1a70afb42407e87fec127554c1293b9 |
| .dev/marketing-v2-requirements/validation/migration/main.go | 39b4bdb4288e8dc822678b8e84cedc99b4c39e9cf83cdb9d405375242c49551b |
| build/dev/marketing-v2-validation/baseline-probe | af11d4b84ba10956bb900ee508bda5e427c92a291da00bdb6888a7e6722c452e |
| build/dev/marketing-v2-validation/candidate-probe | a5a56836fee3d94cabc5c2b26eeae0a22942bf3be76ca0f237318f97e8599587 |
