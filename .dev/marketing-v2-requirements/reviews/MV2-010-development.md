# MV2-010 签到与补签开发记录

2026-11-23，基于已验收提交 `153cf02`。状态：开发中，尚未整项验收或提交。冻结验收标准不变。

## 实现边界

沿用已有用户签到路由，新增补签写入口；用户身份只来自现有 UserAuth，不接收用户指定活动、资格时间或奖励金额。新增补签的个人访问令牌权限沿用签到的 wallet:write。现有 UserAuth 只接受 Authorization，不接受刷新 Cookie，未新增 Cookie 认证或调整认证协议。读 OWASP Authentication、Session Management、Authorization、CSRF 及 ASVS 5.0 V8 适用授权条目，检查每次请求权限/归属、撤权、失败及回放；不声称全站 ASVS 合规。

日历和补签事实由同一用户事务产生；普通签到在锁后取本站当前时间，上海日历不依赖主机时区。无需充值、当日实付、前日实付基于可信在线易支付/付费兑换入账事实。活动起算，旧签到不继承；旧直接增付费余额方法关闭。每日奖励与连续额外奖励独立叠加；补签次数为每用户每活动总量。

连续档位保存已经成立的责任，即使发奖暂时失败也不重复建立。补签合并段时保留各段既有责任；新增独立断签段不能被此前合并段的多余责任抵扣。只有连续奖、当天尚未达档或关闭补发每日奖的合法补签，仍记录日历；没有任何合格活动则不消耗当天资格。互斥在日历事务确定赢家，纯连续活动也参与，延迟发奖不重新选择。

签到成功与赠金到账分别表达：先提交事实，再同步调用生产奖励处理；技术失败返回签到已记录和发奖待处理，保留原责任重试，不提示用户重新签到。正常即时自动到账，不增加领取、激活或审核流程。

接口投影只提供站点配置换算后的金额、明确有效期、实付人民币门槛与日历规则；不暴露原始 quota、转换比例或内部资格证据。完整用户页面属013，本项不冒充页面已完成。

## 验证计划

- 三库模型行为、上海日界、目标日资格、一次/循环、合并段、跨月次数、复制/改名、暂停/结束。
- 实际双网关共享 PostgreSQL/Redis 的签到/补签、同日重试、权限失败；独立OS进程使用生产事务入口测试可信时钟边界，产品HTTP不允许传入时钟。
- 新库、正式custom/main代表基线升级与重复启动；历史数据保留且不生成旧签到奖励。
- Controller真实认证回归与金额非1:1配置、无底层单位响应、非法日期、Cookie不能认证、账号禁用拒绝。
- 最终代码/测试/日志绑定后由独立代理检查A01–A05及共同约束，全通过才标记并提交。

## 中间验证与独立预审阻断

- 模型SQLite及领域首轮完成，最终预审前定向 `TestMarketingCheckin|TestMarketingActivity|TestCheckinCalendarSegments` 通过（model6.197s/internal0.007s）；仍不能替代三库与正式全项验收。
- Controller定向r1因测试管理员邀请码与夹具用户重复失败；r2因非1:1换算预期手算错误、夹具账号创建时间晚于期望补签日失败。修正夹具事实与预期后r3通过0.642s，`build/dev/mv2-010-controller-r3.log`。失败日志保留，未放宽产品边界。
- 独立预审确认旧全历史档位高水位会吞掉新的独立断签段奖励；改为AnchorDay绑定当前段责任，并加反例。一次模式仍全活动最多一次，ordinal只作为永久唯一编号。
- 独立预审要求日历今日/可补日期与实际互斥赢家一致，隐藏但启用的高优先级活动同样影响资格但不泄露详情；落选显示suppressed，不伪报可领。
- 独立预审发现目标日开放区间与充值达标时间需要交集：若活动10点暂停、11点才充值达标，翌日恢复不能补造目标日资格；前日实付条件仍使用前日整日。正在落实同一校验并补对照回归。

正式三库、双进程及修复后全项验收待完成，本记录不提前标记通过。

## 修复与最终矩阵

日历与提交已共用互斥选择；加入全局can_checkin/checked_in_today供统一按钮使用，隐藏活动胜出不泄露规则，也不会误禁按钮。目标日资格使用实付首次达标时刻与活动未暂停区间求交；同日恢复与次日才恢复分别验证。

- `python3 .dev/marketing-v2-requirements/validation/migration_matrix.py all` 最终r2退出0，`build/dev/mv2-010-migration-r2.log`：SQLite3.50.4、MySQL8.4.13、PostgreSQL17.11，新库、正式custom/main ca5af4c代表基线升级、再次启动通过。原checkins保留、不生成新版签到，日历与档位复合唯一约束实际冲突检查通过。
- `python3 .dev/marketing-v2-requirements/validation/accounting_matrix.py all` 最终r2退出0，`build/dev/mv2-010-accounting-r2.log`：真实三库包含新增TestMarketingCheckin及既有资金/活动/支付/Realtime生命周期，全部通过，无FAIL/SKIP。r1为最后日历顶层字段加入前的阶段证据，保留。
- `python3 .dev/marketing-v2-requirements/validation/checkin_process.py all` 退出0，`build/dev/marketing-v2-validation/checkin-process-20261123T071353.677512Z.log`。两个真实网关共享PG17.11与Redis7.2.17逻辑库9，真实HTTP签到/补签/权限/重复、数据库发奖故障→签到成功pending→两个独立worker补发一次均通过。另用独立OS进程调用同一生产事务入口，可信fixture时钟覆盖日界、跨月次数、资格交集、互斥、复制、连续段合并及竞争；不是通过产品HTTP伪造时间。网关SHA efb0b50c913fd212bb554b43592c623e1d73951deb480d655937a1aa0bf239ef，夹具SHA 4aab95153703fbd99ff7cbb96dbbeb3b62676fc90045e309ba0581dbeb2cb58b。
- `python3 .dev/marketing-v2-requirements/validation/checkin_process.py controller` 三库通过。`checkin-controller-{sqlite,mysql,postgres}-checkin-auth-20261123T071453.943367Z.log`，每库3顶层测试组、0SKIP/0FAIL。包含签到真实认证与原登录授权失败/单次消费回归；二进制SHA 22a84d1afb1bd0ac41914074b6aa61a6fda07af681cdbf7c139d6b236100bbe9。
- 早期双进程fixture将ActivityIDs误认为候选列表而非已选赢家，断言失败；修正为只含赢家并核对落选日历suppressed/赠金0。`checkin-process-20261123T070617.175889Z.log`保留，未削弱互斥业务。
- 独立SQLite定向 `go test ./model ./internal/marketing -run '^TestMarketingCheckin$/^sqlite$|^TestCheckin' -count=1 -timeout=180s -v` 已通过model2.306s/internal0.004s，无跳过，日志`build/dev/mv2-010-independent-model-r1.log`。该运行早于最后can_checkin/checked_in_today字段，仅作阶段独立证据，最终字段由上述最终矩阵和全包覆盖。
- 5条新接口文案以脚本写入全部7语言，随后`bun run i18n:sync`通过，临时脚本已删。

最后全包回归仍在进行；等日志完成及manifest绑定后由独立代理正式整项验收，不提前标记完成。

### 最终全包与冻结绑定

`.dev/run go test ./controller ./model ./internal/marketing ./middleware -count=1 -timeout=300s` 退出0：controller189.864s、model21.965s、internal/marketing0.036s、middleware5.571s，`build/dev/mv2-010-packages-r1.log`。最终三库矩阵单独明确要求所有数据库、不允许跳过。候选绑定 `build/dev/mv2-010-review-manifest-r1.json`，所有15份冻结Issue验收正文和共同约束与HEAD逐字相同，原冻结锁文件未修改。等待独立整项正式结论。

## 独立整项验收完成

2026-11-23：独立审查者 `/root/mv2004_path_audit` 对 A01–A05 与共同约束全项 PASS，报告 `MV2-010-acceptance-r1.md`；开始结束25文件/24产物/16冻结段摘要一致。已保留预审阻断、失败及阶段证据，更新Issue状态并立即本地提交，不推送或部署。
