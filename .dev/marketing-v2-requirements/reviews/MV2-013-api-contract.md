# MV2-013 用户活动 API 契约

实现中；以本文件供前后端并行，不代表验收通过。前缀 `/api/marketing/user`，全部真实 dashboard bearer session，PAT 与仅 Cookie 不接受。所有私有数据按会话用户与数据库当前启用状态核对，不接受 user_id 选择他人。统一 `{success,message,data}`；分页 `{items,total,page,page_size}` 默认20、最大100。所有金额 decimal string + currency，时间 Unix 秒，0 到期表示永久。ID 仅机器关联，不显示为产品信息。

## 活动中心

`GET /activities?page=&page_size=&kind=registration|checkin|recharge|invitation&state=active|scheduled|ended|paused`，可省略 kind/state。仅 enabled_at>0 且 published 活动，结束满72小时隐藏。排序进行中/暂停、即将开始、近期结束。`GET /activities/:id` 同可见约束，隐藏后404；不影响奖池、机会、奖励历史。

```ts
Activity = {
 id:number; state:'active'|'scheduled'|'ended'|'paused'; server_now:number;
 activity:{name:string;kind:string;condition:string;frequency:string;starts_at:number;ends_at:number;threshold_cny?:string;rewards:Reward[];checkin:CheckinRule|null};
 pools:PoolSummary[];
}
Reward = {kind:'gift'|'chance';calculation?:'fixed'|'paid_ratio'|'cash_ratio';amount?:string;currency?:string;ratio_percent?:string;valid_seconds?:number;pool_id?:number;pool_name?:string;count?:number;chance_mode?:'direct'|'probability';probability_percent?:string;state?:string;credited_at?:number;expires_at?:number}
CheckinRule = {condition:string;threshold_cny?:string;cycle_days:number;makeup_days:number;makeup_limit:number;makeup_daily:boolean;milestones:{days:number;rewards:Reward[]}[]}
PoolSummary = {id:number;name:string;state:'active'|'scheduled'|'ended'|'paused';starts_at:number;ends_at:number;capacity:number;remaining_capacity:number}
```

不返回互斥组、优先级、换算修订号/内部倍率。pools只含该活动每日与连续奖励实际关联的奖池，去重；capacity/remaining_capacity 是全站共享发行名额，不是当前用户次数。钱包可查询 kind=recharge&state=active，没活动时不虚构福利。

## 奖池与抽奖

`GET /pools?page=&page_size=`：公开活动关联或本人曾获机会的奖池，按独立pool去重；持有仍有效机会的奖池不因来源活动下架消失。`GET /pools/:id` 返回同一Pool详情。

```ts
Pool = PoolSummary & {
 style:'wheel'|'blind_box';server_now:number;available_chances:number;can_draw:boolean;
 chances:{id:number;remaining:number;expires_at:number;state:'ready'|'expired'|'cleared'|'used';source_name:string}[];
 prizes:{id:number;name:string;available:boolean;state:'available'|'sold_out'|'unavailable';no_prize:boolean;gifts:{amount:string;currency:string;valid_seconds:number}[]}[];
}
```

prizes 不返回权重/概率。耗尽奖项 truthful sold_out/available=false，不能显示为仍可抽中；保留完整奖项名称/组合。暂停期间已有机会仍可列出但can_draw=false、截止自然计时。available_chances仅当前未到期且未清零remaining合计；不把总发行剩余数当作个人机会。

`POST /pools/:id/draw` body `{operation_key:string}`（1–64字母数字下划线连字符）。同一次点击生成一个稳定key，网络重试保持key；成功结果后下次抽奖才换key。服务端按用户命名空间绑定key，改变pool用同key返回冲突。结果提交后重试即使池暂停/结束也返回原结果，但账号撤权/禁用拒绝。正常未中奖消耗1次，技术失败不冒充未中奖。

`GET /draws/by-request/:operation_key` 恢复已持久结果；不存在404，前端可用原key重发POST，不得换key重抽。`GET /draws/:id` 按本人归属查询。

```ts
Draw = {id:number;pool_id:number;pool_name:string;prize_id:number;prize_name:string;no_prize:boolean;state:'pending'|'completed'|'cleared';drawn_at:number;rewards:Reward[]}
```

正常所有赠金到账后completed；pending支持部分rewards已到账，不谎报全到账，不要求用户领取。GET只读，后台按原结果恢复；原key POST也可以继续发放原结果。已到账每份赠金返回实际credited_at/expires_at。动画仅按照prize_id呈现，不决定结果；页面重开通过by-request恢复。

## 奖励和赠金

`GET /rewards?kind=activity|lottery&activity_kind=registration|checkin|recharge|invitation&page=&page_size=&starts_at=&ends_at=`，kind默认activity；`GET /rewards/:kind/:id`。

```ts
RewardRecord = {id:number;kind:'activity'|'lottery';source_name:string;state:string;occurred_at:number;credited_at:number;rewards:Reward[];source:{reason_key?:string;activity_id?:number;trigger?:string;checkin_date?:string;friend_username?:string;pool_id?:number;prize_name?:string;no_prize?:boolean;opportunity_activity?:string;payment_kind?:string;paid_cny?:string}}
```

不返回用户名/用户ID、销售凭据、渠道批次、支付引用和内部规则。只在奖励触发者为本人时显示该笔payment_kind/paid_cny，邀请奖励不泄露好友支付详情。state含credited/issued/pending/shortage/probability_miss/pool_expired/zero_amount/cleared，lottery正常completed；reason_key沿用已有英文locale key。筛选时间为[start,end)，activity_kind与日期可同时用。

`GET /gifts?page=&page_size=`、`GET /gifts/:id`：

```ts
Gift = {id:number;source_name:string;source_name_key?:string;source_kind:string;currency:string;granted_amount:string;consumed_amount:string;remaining_amount:string;expired_amount:string;cleared_amount:string;credited_at:number;expires_at:number;state:'active'|'used'|'expired'|'cleared';reward_id?:number;reward_kind?:'activity'|'lottery'}
```

真实消费不含仅占用和到期损失。到期即时将未占用的剩余计入 expired_amount，不等待定时任务；remaining_amount 仅保留到期前在途调用已占用的部分，正常结算后转入 consumed_amount 或 expired_amount，不提前记为消费。不会返回 reserved 等内部状态。已发固定赠金及比例赠金都使用发放时持久化的换算依据；比例赠金显示实际获赠金额，不误用充值面值或单位换算值，不随网站当前配置重写历史。source_name 是实际活动／奖池名；没有可关联名称时为空，并给 source_name_key="Activity reward" 供界面翻译，不能直接呈现内部来源枚举。赠金明细与奖励记录放同页 tabs，不新建消费页。

## 邀请有礼

`GET /invitation?page=&page_size=`：`{invitation_code:string,invitation_path:string,friend_count:number,friends:{items:{username:string;joined_at:number}[],total,page,page_size}}`。invitation_path 使用 /sign-up?aff=。前端同站 origin + invitation_path 生成链接；无旧aff_quota或转余额操作。邀请活动规则使用活动列表kind=invitation&state=active，奖励使用activity_kind=invitation。好友列表包含真实本人邀请关系，历史保留，不以人数承诺一定有奖；不暴露好友余额、充值明细或用户ID。

## 签到

复用 `GET /api/user/checkin?month=YYYY-MM`、`POST /api/user/checkin`、`POST /api/user/checkin/makeup {date:YYYY-MM-DD}`。保留010的calendar真实字段，新增顶层server_now，各活动state；每日/连续机会奖励新增pool_name。用户不可指定activity/user绕过规则。暂无活动时activities=[]，保留入口。前端提交仍遵循原Turnstile接入。
