# MV2-002 开发自检证据

日期2026-11-22，基于d597245后工作区。自检不替代独立验收，不标记Issue完成。

已执行 `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`，退出0，无跳过。脚本用项目 `.dev/run go test -c` 编译，再在独立网络运行同一产物，要求SQLite/MySQL/PostgreSQL三库齐全。

SQLite3.50.4、MySQL8.4.13、PostgreSQL17.11全部通过：不同配置金额换算与安全上限、付款实付与到账分离、赠金独立、跨连接重复和冲突、持久入账序号、全局来源归属、请求及分配唯一约束、晚期写入失败及调用方回滚、两个连接同时入账/赠送、未知实付拒绝、二次AutoMigrate无结构变更。旧余额保留unverified期初，不推断历史首充。

新表和资金入口尚未接业务路由。支付验签/同事务订单接入由008，活动决议由007，消费预占/结算由003及后续Issue实现，不将基础测试称为全链路验收或双应用进程验收。真实基线升级证据见validation/MV2-002-migration-evidence.md。

| 对象 | SHA-256 |
| --- | --- |
| model/main.go | c57dca9d6dabdb5f4daea08d2ffbaaede1e6509d5316147f99d3e688e5fb60c2 |
| model/marketing_v2.go | beac987134a4829079eed1561ac0778e8796f7beee8d8e2ef5bfe87f7a8b1f70 |
| model/marketing_v2_test.go | c4132acbabf9e46cbb0f974b276d57de1f1aa8f6819a3b25671c3755193b0dca |
| .dev/marketing-v2-requirements/validation/accounting_matrix.py | 6556fd9050032b84f5ae78604d445b0bc2b0ee709abb0b4816477698260d7817 |
| build/dev/marketing-v2-model.test | f07941c6b4fc2e6d706a2ca5ac996d0313042f723a1948194caab188cfc49f80 |
| build/dev/marketing-v2-accounting.log | 4644afab7e798b56cb003f34517acb157905791feb832c5ac7ecb0e7a6a5af02 |

## 独立验收发现问题后的修复复测

第一轮自检未覆盖MySQL调用方已建立旧快照及完整首充历史，独立验收发现后修复；保留上文首轮指纹。新增相关回归后 accounting_matrix.py all 再次退出0，三库无跳过。新一轮源码及测试指纹：

- model/marketing_v2.go: 2ff4f19897fafa122997c8ffcd8a7f29e1a70afb42407e87fec127554c1293b9
- model/marketing_v2_test.go: 2525f528a22f5988338e3e78c89a7c7d5d8444516534cc9c7d83b49d33fc94b2
- build/dev/marketing-v2-model.test: 1fbbbc6bec6ac3fb99a5cc2fc63b7b6b7065fab8545316a91660fbd7578269ec
- build/dev/marketing-v2-accounting.log: 27cfa7aa1249736939197667c832afce33334162305553aa70f733ccbdf925a3
