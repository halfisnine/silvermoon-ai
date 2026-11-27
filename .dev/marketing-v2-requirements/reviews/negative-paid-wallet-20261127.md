# 付费金额欠费与充值抵扣修订（2026年11月27日）

用户明确确认：赠金不足后扣付费金额；已准入请求实际费用超过余额的部分属于付费欠费。钱包/概览应让用户看见欠费及充值抵扣说明。本文替代旧设计中的“所有余额不为负、确定费用不足一律待核对”；冻结 Issue 正文与历史验收记录保持原样。普通失败退款修复及统一批量定价功能保留。

## 本轮验收标准

- N01 已准入请求按照限时赠金、永久赠金、付费金额顺序完整记录确定费用。赠金不能为负数；付费金额可在安全数值范围内为负。请求已结算不代表用户已无欠款。
- N02 付费0、赠金0.10、费用0.50得到付费−0.40；付费0.30、赠金0.20、费用0.60得到付费−0.10。充值10后分别为9.60、9.90，不重复记消费。同一请求仍只有一条消费日志，可展开来源拆分。
- N03 欠费阻止新调用，包括新赠金到账、零预估和其他实例；已发送请求仍结算。发送前的预占必须仍获完整支持，不能因另一请求补扣后余额仍为正而发送失去资金支持的等待请求。
- N04 易支付和正式付费兑换码支持充值不足/刚好/超过欠款，来源、实付与完整到账金额保持，重放不重复入账。赠金不偿还欠款。有限令牌的实际消费也完整结算并可为负，新调用仍受令牌限制；付费有余时，单枚令牌耗尽不构成全账号欠费。
- N05 Realtime 已发生的确定费用完整结算；付费或令牌透支后停止本连接/后续轮。原轮资格、到期、未匹配输入、自动回复、迟到终态和累计计价不丢失。降价按真实来源逆序冲正，赠金不变付费。
- N06 未知费用与欠费区分：未知只保留自身证据和占用。普通503/额度耗尽/错误模型/网络等原退款策略保持；不因普通失败重新全账号封锁。退款不向负余额支付现金或清掉欠款。
- N07 钱包和概览复用同一组件展示负付费金额、准确欠费及充值抵扣文案。金额采用站点转换和精度，用户不看底层 quota；前端七语言和AI客户端简中/繁中/英文齐备。
- N08 锁与幂等仍以共享业务数据库为准；实际三库运行，双网关共享 PostgreSQL/Redis 验证。正负上限、反向占用汇总上限和事务回滚不能破坏账本。独立整体验收发现问题后修复重验再提交，更新预览前备份并保留原数据。

没有新增表、字段、索引、中间件、自动还款任务或退款状态机。已确认费用直接扣成负付费余额，充值只做原子加法；账本保存全额消费及原来源。超出整数安全边界和历史未结例外继续保留核对依据，不批量改历史账。

## 验证记录

独立验收首次发现“正余额但预占已失去完整支持”和“多请求非最终冲正导致总占用超限”两项，已增加保护及回归。测试使用专用隔离数据库和可控上游，预览不会写入这些测试资金。


已执行的验证（独立开发测试栈）：SQLite 3.50.4、MySQL 8.4.13、PostgreSQL 17.11；Go 1.26.10、Bun 1.4.4。没有数据库结构、依赖或最低版本特性变更。

| 检查 | 实际命令/范围 | 结果与证据 |
| --- | --- | --- |
| 修改前复现 | `go test ./model -run '^TestMarketingPersistence$/sqlite/funding/confirmed_cost_overdraws_paid_after_gifts' -count=1`（经 `.dev/run`） | 两个例子在原实现均失败，见 `build/dev/negative-wallet/before.log` |
| 三库资金/充值/兑换码 | `python3 build/dev/negative-wallet/matrix.py`；编译 model.test 后执行 `^Test(MarketingPersistence\|EpayMarketing\|PaidRedemption)$`，设置 MV2_REQUIRE_DATABASES=1 及 MySQL/PG 专用 DSN | 三库全通过无跳过；`matrix-model.log`。覆盖充值不足/刚好/超额、重放、并发派发支持、赠金守恒、原路冲正、整数边界及回滚 |
| 三库钱包和账目读取 | 同 runner，以独立空库执行 service.test 的 `^TestMarketingAccountingReads$` | 三库全通过无跳过；`matrix-accounting.log`。包括当前钱包负金额与历史费用转换快照不同口径仍正确 |
| 三库计费生命周期 | 同 runner，以另一独立空库执行 `^Test(MarketingBillingSessionLifecycle\|MarketingBillingMessagesRespectReaderLanguage)$` | 三库全通过无跳过；`matrix-billing.log`。真正求值失败/未知/迟到结算继续覆盖 |
| 相关后端整包 | `.dev/run go test ./model ./service ./i18n ./relay -count=1 -timeout=300s` | 全通过，`packages.log` |
| 静态检查 | `.dev/run go vet ./model ./service`、`git diff --check` | 通过，`vet.log` |
| 前端 | `.dev/run bun run test src/features/wallet/__tests__/balances.test.tsx src/features/usage-logs/components/__tests__/accounting.test.tsx`；`bun run typecheck`；`bun run build` 均经 `.dev/run` | 20/20、1525文件类型检查无误、正式构建通过；`frontend-tests.log`、`frontend-typecheck.log`、`frontend-build.log` |
| 双网关失败及欠费 | `.dev/run go build -o /artifacts/marketing-v2-validation/marketing-gateway .`，构建 gateway-process 后运行 `python3 .dev/marketing-v2-requirements/validation/gateway_process.py failure` | 12组失败/重试及2组实际HTTP欠费→另一实例拒绝→充值→恢复通过。每个欠费请求仅一条消费日志，赠金扣零、付费记负，消费不因还债重复 |
| 双网关综合 | 同二进制执行上述 runner 的 `run` | 15组Realtime、共享账号并发、Redis链路异常、文本/流式/图片、异步受理后进程崩溃恢复全通过 |

三库及前端详细证据位于 `build/dev/negative-wallet/`。三库 runner 为各组创建独立业务测试库，不将账目读取夹具的人造投影混进日志恢复测试。模型测试的来源标识遵守真实列长度约束。未放宽生产鉴权、输入或数据库限制。

双网关详细证据：`build/dev/marketing-v2-validation/gateway-failure-20261127T141936.199830Z.log` 与 `gateway-functional-20261127T141948.983960Z.log`。网关、PostgreSQL、Redis 均为真实进程，外部模型响应是受控夹具，不表示已验证全部商业供应商。`build/dev/negative-wallet/source-manifest.json` 保存本轮变更源码与前后端产物 SHA256，预览复用同一已测可执行文件。

已测网关 SHA256：`08e00cf3334a510b3590bf90b822d941da0995862795ee96233d6c1f40bfe414`。

## 独立验收

`negative_wallet_acceptance`（gpt-6.1-sol，只读）对完整业务及运行证据复验：N01–N07全部通过，N08代码、边界及实际运行部分通过；两项生产缺陷及测试夹具问题均已修复，无剩余生产阻断项。预览备份、更新及数据一致性检查完成后另记操作结果。原冻结Issue未修改。


## 预览更新与最终操作验收

- 代码提交：`fecdffc`，分支 `codex/billing-failure-refund`。已逐文件确认已测变更源码 SHA256 与该提交一致；预览使用与双网关测试相同的可执行文件。
- 仅更新 `silvermoon-channel-model-batch-preview`，运行版本 `preview-unified-fecdffc`，服务器回环端口 `127.0.0.1:46016`。之前合并的批量定价功能与普通失败退款修复保留；错误日志 `ERROR_LOG_ENABLED=true` 保留，资源、网络、只读根目录和数据挂载沿用。
- 停止目标预览后先完整备份数据，再替换。备份目录 `.dev/private/unified-preview/20261127-222235`；原容器保留为 `silvermoon-channel-model-batch-preview-backup-20261127-222235`。
- 更新前后42项数据快照一致、业务记录数量无变化：全部营销表、用户资金以及渠道/模型/供应商/选项/能力/令牌设置。健康接口版本核验成功。详细结果为 `build/dev/negative-wallet/preview-update.json`。
- 实际浏览器读取原预览批量定价页面，新列与端口选择可见、无页面异常、零保存请求：`build/dev/negative-wallet/browser-batch.log`。已有完整保存/刷新验证仍见此前统一预览记录，本轮没有改批量定价源码。
- 浏览器钱包/概览欠费显示通过：1440桌面、390手机宽度，负金额及抵扣说明可见、无横向溢出、无页面异常。负金额仅由浏览器拦截响应构造，未修改预览账户资金；先读取了原真实钱包，全部营销写请求为零。证据 `browser-wallet.json` 及截图位于 `build/dev/negative-wallet/`。
- 未执行生产部署、镜像发布或推送。

最终由同一独立验收 agent 读取预览更新与浏览器证据后确认：N01–N08全部通过，验收关闭，无剩余生产阻断问题。
