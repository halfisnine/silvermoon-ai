# MV2-005 早期风险修复复核（非整项验收）

2026-11-23，独立审查代理 `mv2004_path_audit` 只读审查。代理没有参与实现、修改验收项或把部分检查标记为整个 Issue 通过。

## 首次风险与复核

1. 可信终态用量与算价处于同一事务，算价失败会回滚原始事实。已改为先独立提交安全用量证据，再算价/记账/投影。三库服务测试明确查询原始 UsageJSON，证明失败后事实仍存在。
2. append A → committed A → append B → 回复 A → 完成 A → 断线，原来可能吞掉 B 的未结输入。现分开当前缓冲、等待 commit、确认 commit、等待 clear；模型保存剩余输入占用和 pending。三库服务回归验证 B 保留 review。
3. 非空输出后报告总输出为零，原来可能按零释放。已持久保存 OutputObserved，模型层保留 Unknown/review；三库服务回归验证该反例。

## 第二轮发现与复核

4. 观察到音频输出后，终态却报告 text=1/audio=0，仍可能按文本或 ao=0 漏收。已新增持久 AudioOutputObserved，拒绝这种矛盾终态零音频结算；模型覆盖另一连接恢复，service 的 realtime_audio_mismatch 覆盖该路径。
5. 手动 append → commit → create → committed → created → done（不等待 commit 确认就请求回复），曾遗留错误 pending。已通过 AttachMarketingRealtimeInput 将迟到确认归原 pending 轮，保持资格并保留后续输入；realtime_pipelined_commit 验证完整结算后没有错误 pending。

独立代理确认后两项修复方向及代码覆盖，并读取 `build/dev/mv2-005-service-r7.log` 的通过结果，没有宣称自己执行了该测试。

## 未解除的协议缺口

- 存在 pending 手动 create 时，下一个 response.created 直接绑定 pending，尚无已验证依据排除自动回复插入。可能使用错误旧轮资格，不能上线。
- VAD 自动输入段、speech start/end、committed item ID 与回复之间的可靠关系尚未实现和核实。
- 到期前开始输入、到期后才收到 created 的资格必须依据可靠协议起点；只有没有可靠起点的自动回复采用本站首次接收通知时间。
- 早期官方协议访问曾为 403/超时，失败事实保留。用户现已明确不限定供应商，不能继续以等待用户资料为前提。后续已取得 Microsoft 官方指南及 OpenAI 官方 SDK 证据（见 `MV2-005-protocol.md`）；明确了 metadata 在 done 回显和 VAD 输入项与输出项不同，尚不能据此视为所有关联/释放边界已经实现。合成供应商夹具不能证明未核实的协议假设本身正确。

结论：已报告的具体资金风险已针对性修复，但协议关联和自动 VAD 未完成。A01、A02、A05 仍受上述缺口阻碍；全项 A01–A06 独立验收尚未执行，MV2-005 保持未完成。
