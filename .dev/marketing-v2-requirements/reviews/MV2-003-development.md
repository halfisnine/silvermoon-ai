# MV2-003 开发自检与待验记录

当前状态：整项实现及开发自检结束，待独立全项验收，不能标记完成。基于22dc18a，未使用旧营销实现或旧营销数据。

实现：主库持久请求占用、令牌总占用、累积费用版本及重试结果、按真实消费顺序的来源扣款记录。限时最早到期/同到期ID、永久、付费顺序；原资格结算/补扣；来源冲正及结束释放；未知/未足额成本记录review并阻止新的资金请求。费用未知不作为零，最终未知不能被迟到partial进度或伪装未发送取消清除。已发生费用在禁用/令牌删除后仍可结算，不能借此新发调用。

## 已执行

- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all`：最终第五轮全部通过，SQLite3.50.4/MySQL8.4.13/PG17.11，无跳过；含002回归。日志build/dev/marketing-v2-funding-fifth.log。
- `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all`：重新从真实custom/main基线建库，三库新建和升级各两次启动，9张新表及正式New API记录/索引保留通过。日志build/dev/marketing-v2-funding-migration.log。
- `.dev/run go vet ./model`：退出0。
- 独立工具实现协作者运行`funding_process.py all`：两个真实OS进程，共享PG17.11/Redis7.2.17逻辑库15；争抢/重复/提交丢响应/未提交SIGKILL与实际PG锁等待/缓存过期或断开全部通过，详见validation/MV2-003-process-evidence.md。这不是HTTP网关全链路验收，业务路由由004后接入。

第一轮行为自检发现传入Unscoped GORM语句复用导致账户查询沿用User表，三库全部出现user_id列不存在；隔离事务语句状态修复后通过。后续补齐原占用资金与实际消费来源不同时释放总令牌占用、MySQL旧快照结算发现、未知最终费用的终态保护回归；失败日志保留funding-first.log。

## 认证/授权边界

本Issue无新增HTTP/浏览器会话入口，不改密码、登录或Cookie机制。资金层使用数据库核对用户及令牌归属、当前禁用/删除/到期，避免缓存资格继续开新请求；结算允许已开始请求完成，但校验请求归属。模型权限/IP/调用组仍由现有鉴权及004接入负责，不扩展赠金权限。

开工前官方在线文档返回403/原始文件读取超时，已阅读本机缓存的官方OWASP Authentication与Session Management Cheat Sheets适用内容、ASVS5.0 V7及V8要求；缓存是官方参考文档，不是旧营销代码。应用控制：8.2.2对象归属、8.3.1服务端授权、8.3.2撤权后新操作拒绝；7.4.2的账号禁用效力由新请求资金层再次检查，完整会话撤销非本Issue新增。测试覆盖错用户、过期、禁用、删除后在途结算、版本回放。不给出整个项目ASVS合规声明。

## 待独立验收的指纹

| 文件 | SHA-256 |
| --- | --- |
| model/marketing_v2.go | b12aaca1bc81397fa08c6e928929ab25d4d927d4d06f24f1bf21ba91fff31f27 |
| model/marketing_v2_funding.go | 35c049ff93ced2b41c48bd811aad3cab760a9ee9234f27ae88948319b22e5c49 |
| model/marketing_v2_test.go | 827f1e76dc4ae9827e8a737a3206046dc9a4095cda8e67ca402d14e13c8f5b47 |
| .dev/marketing-v2-requirements/validation/funding_process/main.go | 1ed00a041f623a0f9e4f73cd22d34c7cc4254f78f45d438764d1f9c1f8b9c94f |
| .dev/marketing-v2-requirements/validation/funding_process.py | a463b5a60fa5c15855822387e4b35705521db7610e8580ce796c350555cd4a7e |
| build/dev/marketing-v2-model.test | 2feed7b15d9d103a0c422d5c88ae5f7c11f916c28bdf9c3d9c5018fe15cef821 |
| build/dev/marketing-v2-validation/candidate-probe | aac2235849dfebe8ad0c8af73f0adf5b11dd542835550e3cb8d5ba163e9c40ca |

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
