# MV2-005 Realtime 协议证据记录

记录日期：2026-11-23。只读调研与开发依据，不是 A01–A06 整项验收结果；没有修改冻结验收项，也没有通过合成夹具证明未获证实的官方契约。

## 范围与来源

用户不限定上游供应商，不需要用户另行提供某家供应商资料才能继续。本次核对当前 `custom/main` 实际接入的 OpenAI-compatible Realtime 协议，同时区分官方规范与第三方实现是否遵循该规范。没有将任何一个供应商的实际可用性泛化为全部兼容服务已验收。

本轮开发和迁移基线为 `origin/custom/main` 的 `ca5af4c3fb5a9fdae8eb0a780f86ee1546230f12`。本地 `custom/main` 仍停在较早的 `7c5e0ef434e9cc87394273854f0a39f7d8ecf850`，不能把这个旧 ref 当最新发布基线。已比较下列四个协议入口/DTO文件，两提交间没有差异；本次协议映射适用于实际开发基线。

源码映射：

- `relay/channel/openai/adaptor.go`：Realtime 走 WebSocket 与 OpenAI Realtime handler；Azure 使用其 Realtime endpoint；legacy preview 模型保留 Beta 标记，GA 模型不加该标记。
- `relay/channel/advancedcustom/adaptor.go`：支持自定义 Realtime WebSocket 目标。自定义地址不能作为服务器严格遵循某家官方规范的证明。
- `relay/websocket.go`：现有 Realtime 网关入口与连接生命周期。
- `relaykit/dto/realtime.go`：旧本地 DTO 只在 Response 中保存 usage，没有 metadata、response ID、VAD 输入 item 关联字段。这表明本地旧实现不能提供这些字段的契约证据；不表明协议不支持这些字段。
- 本机已安装 `github.com/openai/openai-go@v1.12.0` 未找到 Realtime 类型定义，不能用它补证本次语义。

可访问的官方资料：

1. [Azure Realtime API reference](https://learn.microsoft.com/en-us/azure/foundry/openai/realtime-audio-reference?view=foundry-classic)：明确 Azure Realtime follows OpenAI Realtime API specification。
2. [Azure Realtime 使用指南](https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/realtime-audio?view=foundry-classic)：包含手动 response metadata、VAD、输入缓冲、输出/usage 示例。
3. [OpenAI 官方 Python SDK Realtime 类型目录](https://github.com/openai/openai-python/tree/main/src/openai/types/realtime)：下载由其 OpenAPI 生成的相关模型原文件，作为公开协议形状和字段说明证据；不是获取了完整 OpenAPI 规范，也不是实际供应商请求验收。

原始资料保存在 `build/dev/mv2005-protocol/`。每个成功下载的原文件均配有 `.json` 来源记录，保存 URL、SHA-256 和下载时间；HTML 另有便于检查的 `.txt` 提取版本，hash 以原 HTML 为准。

| 原始文件 | SHA-256 |
| --- | --- |
| `official-0.html`（Azure reference） | `f484cb5fdffdd584fa7d447ec4499c2a3e1726ec1fea36e3bdae11617022cc44` |
| `official-1.html`（Azure guide） | `00a499bea48185a5e251deb8bb6981a63ebeb9641bc33b43b18a20672cc77afe` |
| `realtime_response.py` | `7dc91ac682bc28dcba222a6f68e339c58efa8ef558a499bd4af9ce8b583c808f` |
| `realtime_response_create_params.py` | `03f3d1bf32f971305e545796b1913a53889e53fd2a28c3680ba02e1160b48ba1` |
| `input_audio_buffer_speech_started_event.py` | `ef120e5ed3271356ff59586f940488e7a6f57b23ea2cf73bfe64414b1171af03` |
| `input_audio_buffer_speech_stopped_event.py` | `48872c35f33e22b5e5103066d316acc3562390de0e862999743154b1f16bf4ee` |
| `input_audio_buffer_committed_event.py` | `0afad1faec0eeb3a276a192c4f63470f8cba2a67baaa340605ae6c2d02f6829c` |
| `response_created_event.py` | `36bf360cb5d65a600e0580a6cf6887a229a03607d96218f400c4903b49ea5728` |
| `response_output_item_added_event.py` | `7e7db4a8d7c9247c494849d9f715d57bc3588d1b8748972f184245c868c4429d` |
| `response_done_event.py` | `a7f15c4bab87bef600434a6c8359df29378fdfbd5f7376f01bb6c2e2dc1f5aa3` |

## 已证实的语义

### 手动请求与响应 metadata

Azure guide 的 out-of-band response 段落明确：客户端可以在 `response.create.response.metadata` 中设置标识，`response.done.response.metadata` 返回此前提供的 metadata；默认会话内的响应也应检查 metadata，以免对应到错误请求。原文提取见 `official-1.txt:1290` 后的段落。

官方 SDK `RealtimeResponseCreateParams` 与 `RealtimeResponse` 均有 optional metadata，最大 16 个键值对，键长 64、值长 512。`response.created.response` 和 `response.done.response` 都是 RealtimeResponse。

**未保证**：模型字段可选，不代表每个供应商的 `response.created` 必带 metadata。文档明确回显点是 done。不能仅以“当前恰好只有一个 pending”推断下一个 created 属于它。客户端自填 metadata 也不是服务端资金归属认证，若实现增加关联标记，必须处理冲突、保留客户端数据以及协议容量，不得默默覆写。

### VAD 输入 item 与响应输出 item

`speech_started.item_id` 标识即将创建的用户消息输入项；正常情况下后续 `speech_stopped.item_id` 相同，官方说明特例是客户端在 VAD 激活期间手动提交缓冲。`speech_stopped`、`committed` 均指用户输入消息，随后有 `conversation.item.created`。

`committed.previous_item_id` 是会话插入位置的前驱，**不是触发该输入对应响应的 response ID**。

`response.output_item.added.item` 是生成的输出项，通过该事件的 `response_id` 归属响应；`response.output` 也是生成输出。没有证据支持输入 item ID 等于输出 item ID。

官方 RealtimeResponse 当前公开字段包含 id、conversation_id、metadata、output、status、usage 等，没有直接声明触发它的 VAD input item ID。`conversation_id` 只确定会话，不能唯一确定某轮。

因此自动 VAD/闲置回复不能抢手动 pending 的资格。没有可靠关联证据时，按本次需求采用首次收到响应开始通知的时间作为该自动轮起点，不能从连接建立时间或另一个待回复输入继承资格；具体实现仍须完整验收。

### 缓冲、会话与计费用量

Azure guide 明确 input audio buffer 保存尚未提交 conversation state 的音频；手动 commit 创建用户消息，VAD 检测结束后自动提交用户消息。手动模式依靠 commit 与 response.create 推进，并非每个 append 就生成一轮。文档还允许手动信号与 server VAD 混用，因此“手动 create 与自动回复不会交错”不是成立的前提。

官方 `RealtimeResponse.usage` 说明其对应计费；会话累积上下文，前一轮生成的文本/音频会进入后续轮的输入。因此：

- committed 是输入进入会话的通知，不是一个独立模型消费金额或用量回执。
- response usage 的 input tokens 可能包含历史上下文，不应按本轮 append 字节量重算、拆分或冲减供应商真实 input usage。
- `response.done` 对完成、取消、失败、不完整状态均会发送；必须检查 status 与可信 usage，不能仅凭收到 done 视为成功或零费用。

## 仍需明确的收口边界

**已存在的 response 缺终态，与尚未被模型回复使用的输入缓冲，是不同状态。** 前者不能因清空/断线而自动释放或按零结算。后者的本地准入占用也不应被误称为已经确定的独立模型欠费。VAD committed 本身不足以证明已经发生一轮费用。

当前取得的资料没有明确给出“关闭连接后所有未归属输入一律免费”的计费保证。特别是：

1. VAD 可能已自动生成，而网关在收到 created/终态前断线；不能仅因本地没有记录响应就断言没有执行。
2. clear 的确认最多描述指定输入缓冲的生命周期，不能据此清除已提交的 conversation item、已启动响应或迟到真实费用。
3. Azure guide 允许 opt-in input audio transcription，且具有独立的 transcription event/session 形态；当前资料不足以证明该费用一定被常规 response usage 覆盖。不能将“没有 response”泛化为“任何服务都没成本”。
4. 若采用纯缓冲准入占用释放规则，应列出所需服务端确认、关闭/排空顺序、已启动或可能启动响应的排除条件以及独立转写的处理边界，并让独立验收按整个账务业务检查。不能以本记录将未确认语义自动批准为产品规则。

后续实现应避免两类相反错误：没有真实欠费证据却永久锁住正常 VAD 已完成会话；已有或可能执行的回复费用缺失却凭关闭释放。具体释放分支尚需代码与协议夹具共同验证，本记录不将该项标通过。

## 访问失败与状态说明

此前及本次访问 developers.openai.com / platform.openai.com 的 Realtime conversations、VAD、server-events、Markdown 等地址仍有 403/超时，历史失败事实保留。后来成功取得上述 Microsoft 官方页面及 OpenAI 官方 SDK 类型；追加读取 commit/clear/转写类型时又出现 timeout/Network unreachable。没有用非官方博客替代规范，也没有取得并声称持有完整 OpenAPI 文档。

这些外网限制不再转化为“等待用户指定供应商/提供资料”的前提。应继续从项目已支持协议与公开官方资料核实；未取得证据的行为必须明示边界，不捏造保证。自动协议实现、真实双实例复验、A01–A06 整项独立验收仍待完成。
