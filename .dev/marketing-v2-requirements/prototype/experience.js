/* Static review interactions only. Fixed outcomes, no APIs, no real random draw. */
'use strict';
let linkedPool = null;
let sampleCount = 0;
let demoFormat = 'wheel';
let outcomeId = '';
let drawBusy = false;
let drawTimers = [];
let drawEpoch = 0;
const referencePalette = [
 ['#eff6ff','#bfdbfe','#2563eb'], ['#ecfdf5','#a7f3d0','#059669'],
 ['#fffbeb','#fde68a','#d97706'], ['#fdf2f8','#fbcfe8','#db2777'],
 ['#f5f3ff','#ddd6fe','#7c3aed'], ['#fff7ed','#fed7aa','#ea580c']
];
function capturePool() {
 const form=$('#pool-form');
 linkedPool={name:form.elements.namedItem('奖池名称').value,format:form.elements.namedItem('展示方式').value,start:form.elements.namedItem('开始时间').value,end:form.elements.namedItem('结束时间').value,
 prizes:poolEntries().map(e=>({id:e.id,name:e.name,gifts:e.row?$$('.reward-row',e.row).map(r=>({amount:Number($('[data-amount]',r).value),life:$('[data-life]',r).value,duration:$('[data-duration-value]',r).value,unit:$('[data-duration-unit]',r).value})):[]}))};
 sampleCount=0;outcomeId='';demoFormat=linkedPool.format;
}
function poolPreview() {
 const form=$('#pool-form');
 if(!form.reportValidity()||!validateCustom(form))return;
 capturePool();poolDraft=form.cloneNode(true);copyFormValues(form,poolDraft);
 state.lotteryScene='normal';location.hash='lottery';
}
function currentPool() {
 if(linkedPool&&!sampleCount)return linkedPool;
 const count=sampleCount||3;
 const names=['月光赠金','星辉赠金','银月大奖'];
 return {name:sampleCount?`${count} 奖项布局样例`:'月光礼盒',format:demoFormat,start:'2026-11-01T00:00',end:'2026-12-05T23:59',prizes:Array.from({length:count},(_,i)=>({id:`sample-${i}`,name:names[i]||`星愿赠金 ${i+1}`,gifts:[{amount:[2,5,100][i]||i+1,life:'limited',duration:i===2?7:2+i,unit:i===2?'天':'小时'},...(i===1?[{amount:1,life:'permanent'}]:[])]}))};
}
function giftText(g) {return `${money(g.amount)} ${g.life==='permanent'?'永久赠金（不过期）':`限时赠金（有效期：${g.duration}${g.unit}）`}`;}
function visiblePrizes(pool) {
 const prizes=pool.prizes.slice();
 if(state.lotteryScene==='empty'&&!prizes.some(p=>p.id==='empty'))prizes.push({id:'empty',name:'谢谢惠顾',gifts:[]});
 return prizes;
}
function giftBox(i) {
 const c=referencePalette[i%referencePalette.length];
 return `<svg viewBox="0 0 120 120" aria-hidden="true"><path d="M20 45 L60 65 L100 45 L100 85 L60 105 L20 85 Z" fill="${c[2]}"/><path d="M60 65 L100 45 L100 85 L60 105 Z" fill="${c[2]}"/><path d="M20 45 L60 65 L60 105 L20 85 Z" fill="${c[1]}"/><path d="M54 62 L66 68 L66 102 L54 96 Z" fill="#facc15"/><g class="gift-lid"><path d="M15 42 L60 20 L105 42 L60 62 Z" fill="${c[1]}"/><path d="M15 42 L60 62 L60 68 L15 48 Z" fill="${c[2]}"/><path d="M60 62 L105 42 L105 48 L60 68 Z" fill="${c[2]}"/><path d="M55 22 L65 17 L65 64 L55 60 Z" fill="#fde047"/><circle cx="60" cy="22" r="6" fill="#eab308"/></g></svg>`;
}
function lottery() {
 const pool=currentPool();const scene=state.lotteryScene;const prizes=visiblePrizes(pool);
 const blocked=['none','ended','upcoming'].includes(scene)||state.draws<=0;
 const status=scene==='ended'?'已结束':scene==='upcoming'?'尚未开始':'开放中';
 const reason=scene==='ended'?'本期已结束':scene==='upcoming'?'尚未开放':blocked?'暂无可用机会':'使用 1 次机会抽奖';
 if(!prizes.some(p=>p.id===outcomeId))outcomeId=prizes[0]?.id||'';
 return title('抽奖','开启盲盒或转动转盘，查看本次奖励。',link('pool-edit','返回奖池配置'))+
 review(`P1.8 · 按你提供的转盘和盲盒静态稿制作，抽奖区域固定参考配色，暂不跟随顶部外观样例。<div class="grid">${field('展示方式',select([['wheel','转盘'],['box','盲盒']],'id="draw-format"',demoFormat))}${field('配置来源',select([['0',linkedPool?'使用后台演示配置':'默认 3 奖项'],['3','3 奖项样例'],['6','6 奖项样例'],['12','12 奖项样例']],'id="draw-sample"',String(sampleCount)))}${field('演示场景',select([['normal','正常开奖'],['empty','未中奖结果样例'],['none','没有机会'],['upcoming','尚未开放'],['ended','已结束'],['error','提交前网络失败']],'id="lottery-scene"',scene))}${field('本次演示结果',select(prizes.map(p=>[p.id,p.name]),'id="draw-outcome"',outcomeId))}</div><small>以上是审阅工具，不出现在正式用户页。结果按所选样例确定，不使用随机算法；“未中奖”样例临时加入空奖，不改变后台草稿。</small>`)+
 `<section class="lottery-reference"><div class="lottery-heading"><div><span class="lottery-eyebrow">SILVERMOON · 幸运时刻</span><h2>${esc(pool.name)}</h2><p>${esc(displayDate(pool.start))} — ${esc(displayDate(pool.end))}</p></div><div><span class="pill">${status}</span><p>可用机会 <strong>${scene==='none'?0:state.draws}</strong> 次</p></div></div>
 <div class="draw-stage" aria-busy="false">${demoFormat==='wheel'?`<div class="wheel-rim"><div class="rim-lights">${Array.from({length:24},(_,i)=>`<i style="--angle:${i*15}deg"></i>`).join('')}</div><canvas id="prize-wheel" width="720" height="720" role="img" aria-label="奖项转盘，完整奖项见下方列表"></canvas><span class="wheel-pin"></span><button class="wheel-go" data-draw ${blocked?'disabled':''} aria-label="使用一次机会转动转盘">GO<span>抽奖</span></button></div>`:`<div class="single-box-stage"><button class="mystery-box" data-draw ${blocked?'disabled':''} aria-label="使用一次机会开启盲盒"><span class="box-reveal" aria-hidden="true"></span><span class="box-art">${giftBox(0)}</span><strong>开启盲盒</strong><span>剩余 ${scene==='none'?0:state.draws} 次机会</span></button></div>`}
 <p class="draw-status" role="status">${blocked?reason:demoFormat==='wheel'?'点击 GO，开启幸运时刻':'点击开启盲盒，揭晓本次惊喜'}</p></div>
 <div class="lottery-prize-list"><h3>本期奖项</h3>${prizes.some(p=>p.gifts.length>1)?'<p class="prize-list-help">抽中组合奖时，其中的赠金一起发放。</p>':''}<div class="reference-prizes">${prizes.map((p,i)=>`<article class="reference-prize" style="--prize-light:${referencePalette[i%6][0]};--prize-mid:${referencePalette[i%6][1]};--prize-ink:${referencePalette[i%6][2]}"><div class="prize-title-line"><span class="prize-index">${String(i+1).padStart(2,'0')}</span><h4>${esc(p.name)}</h4>${p.gifts.length>1?'<span class="combo-badge">组合奖</span>':''}</div>${p.gifts.length?p.gifts.map(g=>`<p class="prize-gift-line"><strong>${esc(money(g.amount))}</strong><span>${g.life==='permanent'?'永久赠金':`限时赠金 · 有效期：${esc(g.duration)}${esc(g.unit)}`}</span></p>`).join(''):'<p>本次未中奖，无赠金到账</p>'}</article>`).join('')}</div></div><p class="stage-note">每次使用 1 次机会，中奖赠金自动到账，无需领取。完整赠金金额与有效期以上方清单为准。</p></section>`+
 review('转盘格子仅用于展示，不代表中奖概率；奖项多时以编号对应完整清单。页面不展示后台权重。此为静态样例，机会截止与暂停联动仍待 N03 确认。')+
 card('机会来源（独立样例）',table(['来源','获得','说明'],[['充值福利','2 次','符合充值活动条件后获得'],['十一月签到礼','1 次','符合签到活动条件后获得']]))+
 card('本次浏览的抽奖记录',state.drawn.length?table(['奖池','结果','奖励'],state.drawn.map(r=>[esc(r.pool),esc(r.result),esc(r.gift)])):'<p class="muted">暂无记录，完成一次演示开奖后可查看。</p>')+
 `<div class="flow-links">${link('gifts','查看赠金明细')}<button data-reset-draws>重置抽奖演示（3 次机会）</button></div>`;
}
function paintWheel() {
 const canvas=$('#prize-wheel');if(!canvas)return;
 const ctx=canvas.getContext('2d');const prizes=visiblePrizes(currentPool());const arc=Math.PI*2/prizes.length;
 prizes.forEach((p,i)=>{
  const color=referencePalette[i%6];const gradient=ctx.createRadialGradient(360,360,0,360,360,354);gradient.addColorStop(0,'#ffffff');gradient.addColorStop(1,color[1]);
  ctx.beginPath();ctx.moveTo(360,360);ctx.arc(360,360,354,i*arc,(i+1)*arc);ctx.closePath();ctx.fillStyle=gradient;ctx.fill();ctx.strokeStyle='#ffffff';ctx.lineWidth=3;ctx.stroke();
  ctx.save();ctx.translate(360,360);ctx.rotate(i*arc+arc/2);ctx.fillStyle=color[2];ctx.textAlign='center';ctx.font='bold 23px sans-serif';
  const short=prizes.length>8||p.name.length>7;ctx.fillText(short?`奖项 ${i+1}`:p.name,245,-7,145);
  ctx.font='18px sans-serif';ctx.fillText(p.gifts.length>1?'组合赠金':p.gifts.length?money(p.gifts[0].amount):'谢谢惠顾',245,24,130);ctx.restore();
 });
}
function cancelDrawAnimation() {
 drawEpoch++;drawTimers.forEach(clearTimeout);drawTimers=[];drawBusy=false;
}
function startDraw(button) {
 if(drawBusy||button.disabled||state.draws<1||['none','ended','upcoming'].includes(state.lotteryScene))return;
 if(state.lotteryScene==='error'){modal('抽奖暂未完成',note('本场景模拟提交前网络失败：机会尚未扣除。若服务器已形成结果，正式实现应恢复原结果，不能重新开奖。'));return;}
 const pool=currentPool();const prizes=visiblePrizes(pool);const prize=prizes.find(p=>p.id===(state.lotteryScene==='empty'?'empty':outcomeId))||prizes[0];if(!prize)return;
 drawBusy=true;const epoch=drawEpoch;state.draws--;
 // Outcome is stored before animation so navigation cannot lose an already completed demo draw.
 state.drawn.unshift({pool:pool.name,result:prize.name,gift:prize.gifts.length?prize.gifts.map(giftText).join(' ＋ '):'无赠金到账'});
 $$('.review-only select,[data-draw],[data-reset-draws]').forEach(el=>el.disabled=true);
 $('.draw-stage').setAttribute('aria-busy','true');$('.draw-status').textContent='正在揭晓…';
 const reduced=matchMedia('(prefers-reduced-motion: reduce)').matches;
 const duration=reduced?50:demoFormat==='wheel'?4200:2200;
 const canvas=$('#prize-wheel');
 if(canvas){const index=prizes.indexOf(prize);const stop=(270-(index*360/prizes.length+180/prizes.length)+360)%360;canvas.style.transition=`transform ${duration}ms cubic-bezier(.12,.72,.12,1)`;canvas.style.transform=`rotate(${1800+stop}deg)`;}
 else {button.classList.add('charging');drawTimers.push(setTimeout(()=>{if(epoch!==drawEpoch)return;button.classList.remove('charging');button.classList.add('opened');$('.box-reveal',button).textContent=prize.name;},reduced?10:1200));}
 drawTimers.push(setTimeout(()=>{
  if(epoch!==drawEpoch||currentRoute!=='lottery')return;
  render();modal(prize.gifts.length?'恭喜中奖':'谢谢惠顾',`<div class="draw-result"><span class="result-spark">✦</span><h3>${esc(prize.name)}</h3>${prize.gifts.map(g=>`<p>${esc(giftText(g))}</p>`).join('')||'<p>本次未中奖，无赠金到账。</p>'}<p>已使用 1 次机会，剩余 ${state.draws} 次。</p>${prize.gifts.length?'<p>以上赠金已在本次抽奖演示中记为到账，无需领取。</p>':''}<small>静态演示，不修改真实钱包。</small>${state.draws>0?'<div class="actions"><button class="primary" data-draw-again>再抽一次</button></div>':''}</div>`);
 },duration+80));
}
function signIn() {
 const scene=state.profileScene;const active=!['none','upcoming','ended'].includes(scene);const done=state.checkin||scene==='signed';const ineligible=scene==='ineligible';
 let calendar='<div class="calendar">'+['一','二','三','四','五','六','日'].map(d=>`<span>${d}</span>`).join('')+'<span></span>'.repeat(6);
 for(let d=1;d<=30;d++){
  const outside=d<15;const checked=!outside&&((d>=16&&d<=21)||(d===22&&done)||(d===15&&state.makeup));const canMakeup=active&&d===15&&!state.makeup;
  calendar+=`<button class="${outside?'outside-period':checked?'checked':canMakeup?'missed':''} ${d===22?'today':''}" data-day="${d}" ${canMakeup?'':'disabled'}>${d}<small>${outside?'活动外':checked?'已签':canMakeup?'补签':d===22?'今天':'—'}</small></button>`;
 }
 calendar+='</div>';
 const content=scene==='none'?'<div class="empty">当前没有可参与的签到活动，开放后可在这里签到。</div>':scene==='upcoming'?'<div class="empty"><h3>十二月签到礼 · 尚未开始</h3><p>2026年12月1日—12月15日</p><p>每日赠金 ¥1.00，有效期：3天。</p><button disabled>12 月 1 日开放签到</button></div>':scene==='ended'?'<div class="empty"><h3>十月签到礼 · 已结束</h3><p>2026年10月15日—10月31日</p><p>本活动已结束，不能继续签到或补签。过往奖励可在“活动奖励”中查询。</p><button disabled>活动已结束</button></div>':`<div class="title-row"><div><h3>十一月签到礼</h3><small>2026年11月15日—11月30日</small></div>${badge(done?'今日已签到':'今日未签到',done?'success':'')}</div><div class="grid three"><div>每日奖励<div class="reward-amount">¥1.00</div><small>有效期：3天</small></div><div>已连续签到<div class="reward-amount">${(done?7:6)+(state.makeup?1:0)} 天</div><small>连续签到7天，额外赠送限时赠金 ¥2.00（有效期：7天）</small></div><div>本活动剩余补签<div class="reward-amount">${state.makeup?'0':'1'} 次</div><small>总上限 2 次，按本活动累计</small></div></div>${note(ineligible?'今天累计实付充值未达到 ¥10，暂不能签到；11 月 15 日当时已达标，仍可补签。':state.makeup?'补签已修复连续记录；连续第 7 天奖励只发一次。':done?'今日已获得每日 ¥1.00 ＋ 连续额外 ¥2.00，合计 ¥3.00。':'今天签到：每日 ¥1.00 ＋ 连续第 7 天额外 ¥2.00，合计 ¥3.00。')}<button class="primary" data-checkin ${done||ineligible?'disabled':''}>${done?'今日已签到':'签到'}</button><h3>2026 年 11 月</h3>${calendar}<p class="muted">最近 10 天内可补签，且必须处于本活动期间、符合漏签当天条件。活动前日期不计断签；次数按活动统计，跨月不重置。</p>${scene==='multi'?note(`另有“周末签到礼”可叠加：每日 ¥0.50，本活动剩余补签 ${state.makeup?'1':'2'} 次。一次操作统一判断，各活动独立记次数和奖励。`):''}`;
 return title('签到','查看当前活动，签到或补签；过往奖励统一在活动奖励中查询。')+review(`固定演示日期 2026-11-22。${select([['normal','进行中 · 可签到'],['signed','进行中 · 今日已签到'],['ineligible','进行中 · 当日条件未满足'],['multi','多个活动同时进行'],['upcoming','活动未开始'],['ended','活动已结束'],['none','当前没有活动']],'id="profile-scene"',scene)}`)+card('每日签到',content+`<div class="flow-links">${link('my-rewards?source=checkin','查看签到奖励记录')}</div>`);
}
document.addEventListener('change',event=>{
 const t=event.target;
 if(t.id==='draw-format'){demoFormat=t.value;render();}
 if(t.id==='draw-sample'){sampleCount=Number(t.value);if(!sampleCount&&linkedPool)demoFormat=linkedPool.format;outcomeId='';render();}
 if(t.id==='draw-outcome')outcomeId=t.value;
});
document.addEventListener('click',event=>{
 if(event.target.closest('[data-draw-again]')){$('#dialog').close();startDraw($('[data-draw]'));}
 if(event.target.closest('[data-reset-draws]')){state.draws=3;state.drawn=[];render();}
});
