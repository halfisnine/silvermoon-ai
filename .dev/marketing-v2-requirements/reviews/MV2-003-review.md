# MV2-003 独立验收记录

## 第一轮不通过

独立验收者mv2003_acceptance，基线22dc18a加未提交实现。A03/A04通过；A01/A02/A05不通过。独立三库、双真实进程、基线升级复验均通过，但独立PG业务探针发现：

1. unlimited切换后冲正按当前令牌配置，原无限消费10后改有限余额5再冲正变15，凭空加预算；反向切换原已扣额度不恢复。
2. 进度费20、合法入账将余额补满后，真实终态费10冲正超出钱包安全边界，整个操作回滚，真实终态仍丢失，数据库残留active/20而非review/10。

首轮源码摘要：marketing_v2.go b12aaca1bc81397fa08c6e928929ab25d4d927d4d06f24f1bf21ba91fff31f27；funding.go 35c049ff93ced2b41c48bd811aad3cab760a9ee9234f27ae88948319b22e5c49；test.go 827f1e76dc4ae9827e8a737a3206046dc9a4095cda8e67ca402d14e13c8f5b47。原证据build/dev/mv2003-independent-review-manifest.json、mv2003-independent-probe.log保留。

## 修复完成，第二轮待独立全项复验

每个实际debit保存TokenQuotaCharged，部分冲正按原扣减事实恢复，不能用请求初始或当前开关代替。资金冲正通过数据库保存点隔离；钱包/赠金合计/令牌恢复达到上限时回滚所有部分冲正，仍提交新真实Actual/终态/定价与用量依据/明确review原因及版本结果。只有实际费用等于已结费用才允许最终释放；恢复通过新版本仅执行未完成差额，同版本始终重放原结果。

新增三库用例：不限额正反切换及同请求多次切换、部分冲正；付费余额上限保留真实终态/同版本重放/旧在途请求结算释放空间后恢复；赠金合计上限造成跨来源部分冲正回滚及恢复；令牌恢复上限保留证据及恢复。全部通过。

实际命令：accounting_matrix.py all → funding_process.py all → migration_matrix.py all → .dev/run go vet ./model，全部退出0。迁移从真实custom/main重建三库、新建升级各两次。双进程复测真实PG等锁backend13070等待13069后SIGKILL回滚，六场景全部通过。原日志funding-repair1.log、funding-process-repair1.log、funding-migration-repair1.log。未更改任何验收项，仍待A01–A05整体复验。

## 第二轮独立全项通过

验收者mv2003_acceptance。A01–A05及共同约束在资金模型范围全部通过，无新阻断项。独立重新编译三库行为、双进程、首轮同一探针，并完整重新构建真实custom/main三库基线/新库/升级重复启动，全部退出0；SQLite3.50.4、MySQL8.4.13、PG17.11、Redis7.2.17。

首轮探针复验：无限→有限remain5（不虚增）；有限→无限remain15（退原扣10）；余额上限冲正返回nil，actual10/settled20/review/finaltrue及真实终态证据均持久。新增多次切换/部分冲正/多来源中途回滚/容量释放后恢复/原结果重放全部通过。真实双进程PG等锁13792→13791、SIGKILL后等待者恢复已观测。

本轮model/marketing_v2.go SHA256 f9d559cdf63306c92d61cd27cc9265945235987659736f30b068f4b1cf9dfeb9；funding.go 4d895d9ed06c26ceb24f856f4d3f694dbcf8184654aa02aa2e381d6d18e2591a；test.go a6bc2ea9d1664b558a4019d72ac6e2ddc67d8a1d1e915d9cf7f110deb94816ab。证据build/dev/mv2003-independent-review-round2-manifest.json及mv2003-independent-{accounting,process,probe,migration}-round2.log。

全部15项及共同约束摘要匹配，未改标准，首轮失败证据保留。可标记003完成并提交。通过范围不包括HTTP网关、Realtime、管理员维护界面或对应路由认证，继续由004及所属后续Issue实现验收；未迁移旧营销数据。
