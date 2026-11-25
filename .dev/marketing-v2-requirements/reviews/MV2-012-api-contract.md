# MV2-012 管理 API 契约

后端实现与数据库验证已完成，整项独立验收另记。所有路径以 `/api/marketing/admin` 为前缀，管理员浏览器会话；每次请求（包括幂等重放）在业务数据库重新核对当前启用管理员身份。管理写操作仅接受显式 bearer session，Refresh Cookie本身不能认证；刷新等Cookie入口沿用站点现有Origin验证。身份、现在时间、换算均由服务端决定。接口采用统一 `{success,message,data}` 信封；分页 data 为 `{items,total,page,page_size}`，page 从1开始，page_size默认20、最大100。

## 查询和操作

- `GET /metadata`：`{currency:"CNY",money_revision:"opaque-hash"}`。不返回quota倍率；奖池选择复用分页 GET /pools；`GET /groups?keyword=&page=1&page_size=20` 返回分页 `{name,count}`，组成员通过 `/activities?exclusive_group=精确组名` 分页读取。新组可输入非空名称（当前不可单独改组名）。组名身份按精确字符串区分（例如 GroupA 与 groupa 为不同组），三库查询与规则引擎一致。
- `GET /activities?page=1&page_size=20&kind=recharge&keyword=...`、`GET /activities/:id`。
- `POST /activities`：`{operation_key:"UUID",money_revision:"opaque-hash",activity:{...}}`，创建草稿。
- `PUT /activities/:id`：`{operation_key:"UUID",money_revision:"opaque-hash",revision:1,activity:{...}}`。
- `POST /activities/:id/:action`：action为 activate/pause/resume/publish/unpublish；body `{operation_key:"UUID",revision:1}`。
- 复制：读取原详情，前端复制其 activity 表单、改名称和起止日期，POST /activities 创建新草稿。不能转移旧资格/余数/已发记录。
- `GET /pools?page=1&page_size=20&keyword=...`、`GET /pools/:id`。
- `POST /pools`：`{operation_key:"UUID",money_revision:"opaque-hash",pool:{...}}`。奖池按时间开放，可暂停；没有第二套奖池发布审批。
- `PUT /pools/:id`：`{operation_key:"UUID",money_revision:"opaque-hash",revision:0,pool:{...}}`。
- `POST /pools/:id/:action`：pause/resume/restock/append/extend；body `{operation_key:"UUID",revision:0,prize_id:123,amount:20,ends_at:1800000000}`。prize_id只用于restock，amount只用于restock/append，ends_at只用于extend。运行时核心配置锁定，追加名额与补库存不同；延期只延长未到期机会。
- 复制奖池：详情 pool表单改名称/时间并POST新建，不转移旧机会。库存使用显式新配置数量。
- `GET /rewards?page=1&page_size=20&user_id=...&activity_id=...&kind=activity|lottery&starts_at=...&ends_at=...`；`GET /rewards/:kind/:id`。kind明确区分活动奖励行与抽奖结果，查询详情返回真实来源；不提供人工审核或重新开奖按钮。

创建/保存请求必须附带 `money_revision`，来自metadata或所编辑详情；活动/奖池详情顶层也包含该字段。同币种换算变更同样拒绝旧表单保存，要求刷新后确认。金额转换在每次写请求中冻结一次服务端配置。

写操作返回对应最新详情；同 operation_key+同payload 的成功请求安全重放，撤权后仍拒绝；同键不同payload冲突。校验失败不占用操作键，用户修正后可重试。保存及操作必须带revision，旧页面覆盖新配置返回409。成功重放不因revision已增加而失败。表单修改后生成新键；网络重试保持原键。货币设置变动引起的币种不匹配返回409，请刷新表单。

## 活动表单

```json
{
  "name":"充值有礼","kind":"recharge","published":true,
  "starts_at":1800000000,"ends_at":1801000000,
  "condition":"cumulative_payments","frequency":"every","threshold_cny":"100",
  "exclusive_group":"充值福利","priority":10,
  "rewards":[
    {"kind":"gift","calculation":"fixed","amount":"2","currency":"CNY","valid_seconds":259200},
    {"kind":"chance","pool_id":123,"count":1,"chance_mode":"probability","probability_percent":"50"}
  ],
  "checkin":null
}
```

活动详情为 `{id,revision,state,enabled_at,editable,activity:{上述表单}}`；state=draft/scheduled/active/paused/ended；0 ends_at表示长期。奖励的pool_id用于机器关联，回显同时给pool_name，UI显示名称。所有有效期由UI以天/时/分输入后换算 valid_seconds，0表示永久。

- kind=registration：condition=registration,frequency=once，无门槛。
- recharge：condition=first_payment/single_payment/cumulative_payments；first_payment频率once，后两者once/every；threshold_cny>0。
- invitation：friend_registration/once；friend_cumulative/once或every（有门槛）；friend_ratio/every。
- checkin：condition=checkin,frequency=every，checkin如下。rewards是每日奖；milestones是连续额外奖，两者叠加。

```json
{"condition":"today_paid","threshold_cny":"10","cycle_days":7,"makeup_days":3,"makeup_limit":2,"makeup_daily":true,"milestones":[{"days":7,"rewards":[{"kind":"gift","calculation":"fixed","amount":"2","currency":"CNY","valid_seconds":604800}]}]}
```

checkin条件none/today_paid/previous_day_paid；none不填门槛。cycle_days=0为一次性档位，正数为循环；makeup_days和makeup_limit同时0为关闭。比例赠金：calculation=paid_ratio（单笔到账付费金额）或cash_ratio（好友实付人民币），使用 `ratio_percent:"5"`，不填amount；有效期照常，currency仍为站点币种。累计两种模式不允许比例赠金。概率机会仅充值累计可用；直接机会 chance_mode=direct，不填概率。

## 奖池表单

```json
{
  "name":"幸运转盘","style":"wheel","starts_at":1800000000,"ends_at":1801000000,"capacity":100,
  "prizes":[
    {"name":"限时赠金","weight":80,"stock":-1,"gifts":[{"amount":"2","currency":"CNY","valid_seconds":7200}]},
    {"name":"幸运大奖","weight":1,"stock":1,"gifts":[{"amount":"100","currency":"CNY","valid_seconds":604800}]},
    {"name":"谢谢惠顾","weight":19,"stock":-1,"gifts":[]}
  ]
}
```

style=wheel/blind_box；stock=-1为不限量，capacity必须有限正整数。空奖gifts=[]，只有管理员勾选允许未中奖时才配置。组合奖多份gifts共同发放，仍计一份库存。详情 `{id,revision,state,editable,issued,remaining_capacity,appendable_capacity,pool:{...}}`，appendable_capacity为null表示存在不限量有效奖品供给；prizes回显id用于补原奖库存。核心配置不可改时仍可独立补库存、追加、延期，服务端现有模型执行供给校验。

奖励记录DTO以名称、显示金额和时间为中心，包含 `id,kind,user_id,username,source_name,state,occurred_at,credited_at,rewards,source`。详情source包含真实的充值来源/实付人民币、签到日、好友用户名或奖池名称和开奖结果；不返回可用卡密、支付密钥、内部概率种子、原始规则快照或raw quota。


## 安全和验证依据

沿用正式主线 dashboard bearer session，Refresh Cookie本身不能认证管理API；不新增Cookie写操作认证路径。每次HTTP请求仍验证session，业务事务再次锁定并检查当前管理员用户行（含幂等重放）；撤权、禁用或删除均拒绝。操作记录不保存请求token/卡密。依据OWASP Authentication、Session Management、Authorization及CSRF Cheat Sheets和ASVS 5.0.0授权/会话控制；实际回归包含跨站Cookie写入拒绝、真实session、撤权重放、越权ID和参数篡改。

## Schema补充

`marketing_v2_activities.exclusive_group`新增有索引字符串投影列，Create/Update与权威Spec同事务维护。启动迁移对已有新版Spec按100条批次幂等回填；custom/main尚无该营销表，不导入旧营销数据。三库fresh/upgrade与重复启动需随MV2-012验收记录。


补充筛选与来源：`/rewards?username=精确用户名` 不需要运营人员填写用户ID。source.reason_key返回可翻译的真实结果原因；pending只表示待处理，不虚构技术错误详情。每个已到账赠金/机会的rewards行携带state、credited_at、expires_at（永久赠金为0，未到账不伪造日期）。online来源有真实TopUp行时返回payment_reference=站内订单号；payment_id仅用于机器关联，不作为界面标签。
