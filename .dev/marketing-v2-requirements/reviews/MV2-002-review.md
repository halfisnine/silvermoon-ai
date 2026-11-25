# MV2-002 独立验收记录

## 第一轮：不通过

日期2026-11-22，独立验收者`mv2002_acceptance`。父提交d597245，未修改验收标准。

A01、A02、A04通过；A03、A05不通过。既有三库行为矩阵及真实基线升级全部通过，但独立MySQL探针复现调用方先建立旧快照、另一连接入账后，相同payload重试看不到新账户，报重复主键；旧快照SUM还存在赠金总额漏计风险。A05仅有新账本顺序，缺全站首充历史状态，不能将Sequence=1视为真实首充。

本轮生产源码SHA256：beac987134a4829079eed1561ac0778e8796f7beee8d8e2ef5bfe87f7a8b1f70。测试SHA256：c4132acbabf9e46cbb0f974b276d57de1f1aa8f6819a3b25671c3755193b0dca。独立证据在build/dev/mv2002-independent-review.json及mv2002-review-stale-snapshot.log。未标记完成、未提交为通过。

## 修复及第二轮待验

账户与幂等原事实采用当前锁读，赠金总额从当前锁读金额安全合计，避免MySQL旧快照绕过。账户保存unknown/none/paid历史状态和证据，付款保存unknown/first/later及确认首笔引用。旧账户包括零余额账户默认unknown；首笔新事实不伪称全站首充，其后已有真实付费证据，标later。确认无历史仅由后续009可信新注册创建事务初始化，002不开放“余额为零即未充值”接口；历史核实/切换仍归011。

新增真实MySQL旧事务快照重复付款/重复赠金/聚合上限回归，三库可信新用户首笔、重复、付费卡密后续、清空余额不重置首充测试。`accounting_matrix.py all`重新编译及三库全部通过，无跳过。全部A01–A05需由同一独立验收者整体复验，尚不能标记完成。

## 第二轮：全项通过

独立验收者`mv2002_acceptance`重新检查A01–A05及共同约束，全部通过，可提交。独立重新编译accounting_matrix.py all三库无跳过；fresh/upgrade三库再次启动均通过；首轮MySQL独立探针复编译重跑输出REVIEW_RETRY_PASS。全部冻结摘要未变。

A01资产/来源/分配及唯一约束通过；A02精确实付、配置转换、上限与未知期初通过；A03跨连接幂等、冲突、旧快照当前读及金额聚合通过；A04真实三库新建/基线升级/重复启动/数据索引约束/回滚通过；A05独立首充历史与引用、重试、清空余额不重置通过。009仍须在实际新注册事务建立可信none，不将测试夹具当成入口接入。

用户最新边界：只升级干净custom/main，不迁移旧营销数据。没有声称本Issue完成真实双应用进程/Redis故障/调用计费/支付注册路由/页面，后续Issue按冻结标准独立验收。

本轮源码摘要2ff4f19897fafa122997c8ffcd8a7f29e1a70afb42407e87fec127554c1293b9；测试摘要2525f528a22f5988338e3e78c89a7c7d5d8444516534cc9c7d83b49d33fc94b2；独立测试产物1fbbbc6bec6ac3fb99a5cc2fc63b7b6b7065fab8545316a91660fbd7578269ec；迁移产物a5a56836fee3d94cabc5c2b26eeae0a22942bf3be76ca0f237318f97e8599587。详细原始证据build/dev/mv2002-independent-review-round2.json、mv2002-review-stale-snapshot-round2.log。
