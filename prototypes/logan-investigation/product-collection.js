'use strict';
const P=LoganProduct;
function productState(i=issue()){return i.product||(i.product={});}
const productOldCapture=startAcquisitionFromWorkspace;
startAcquisitionFromWorkspace=function(id){
 productOldCapture(id);
 const form=$('#modal form');
 const extra=document.createElement('section');extra.className='product-card';
 extra.innerHTML=`<span class="eyebrow">采集能力 · 演练配置</span><label for="capture-profile">设备采集能力</label><select id="capture-profile" onchange="refreshCaptureScope()"><option value="precise">支持指定时段及类型（合成能力）</option><option value="day">仅支持按日采集混合日志</option><option value="unsupported">SDK 不支持远程采集</option><option value="web-offline">Web 页面未运行，无法接收</option></select><div id="capture-scope"></div>`;
 form.querySelector('label').before(extra);
 for(const id of ['new-capture-begin','new-capture-end','new-capture-issue'])$('#'+id).addEventListener('change',refreshCaptureScope);
 refreshCaptureScope();
};
function refreshCaptureScope(){
 const mode=$('#capture-profile').value,box=$('#capture-scope');
 document.querySelectorAll('input[name="capture-type"]').forEach(el=>el.disabled=mode==='day');
 try{const range=P.captureScope($('#new-capture-begin').value,$('#new-capture-end').value,mode);
 box.innerHTML=`<dl class="product-facts"><dt>期望日志范围</dt><dd>${esc($('#new-capture-begin').value.replace('T',' '))} — ${esc($('#new-capture-end').value.replace('T',' '))}</dd><dt>实际采集范围</dt><dd>${esc(range.begin.replace('T',' '))} — ${esc(range.end.replace('T',' '))}</dd><dt>文件类型</dt><dd>${mode==='day'?'按日混合文件，解析后筛选类型':'选定的日志类型'}</dd></dl>${range.expanded?'<label class="check notice"><input id="capture-expanded" type="checkbox">我已确认实际采集范围扩大至整日，包含故障时段之外的内容</label>':''}<small>以上为能力演练，不代表当前 SDK 或渠道已接入。</small>`;
 }catch(e){box.innerHTML=`<div class="notice">${esc(e.message)}</div>${btn('改用手动导入',"closeModal();openLogImport()")}`;}
}
const productOriginalSubmit=submitInlineCollection;
submitInlineCollection=function(event,forceNew=false){
 if(!$('#capture-profile'))return productOriginalSubmit(event);
 event.preventDefault();
 try{
 const profile=$('#capture-profile').value,begin=$('#new-capture-begin').value,end=$('#new-capture-end').value,scope=P.captureScope(begin,end,profile);
 if(scope.expanded&&!$('#capture-expanded')?.checked)throw Error('请先确认实际采集范围扩大至整日');
 const i=state.issues.find(i=>i.id===$('#new-capture-issue').value),device=$('#new-capture-device').value.trim();
 const types=profile==='day'?['混合日志']:[...document.querySelectorAll('input[name="capture-type"]:checked')].map(e=>e.value);
 if(!device||!types.length)throw Error('请填写设备并选择日志类型');
 const form={device,begin,end,types,budget:$('#capture-budget').value,ttl:$('#capture-ttl').value,notify:$('#capture-notify').checked};
 const similar=i.requests.find(r=>!M.terminal.includes(r.status)&&r.device===device&&r.begin===begin&&r.end===end&&r.types.join()===types.join());
 if(similar&&!forceNew){$('#capture-error').innerHTML=`已有相同范围的进行中请求 ${esc(similar.id)}，请查看后决定是否继续。${btn('查看现有请求',`closeModal();openSourceCollection('${i.id}/${similar.id}')`)}${btn('仍新建独立请求',"submitInlineCollection(event,true)")}`;return;}
 let result;
 if(forceNew&&similar){const temporary={...i,requests:[]};result=M.create(state,temporary,form);i.requests.push(result.request);result.request.similarTo=similar.id;}else result=M.create(state,i,form);
 const r=result.request;
 r.product={profile,scope,requestedTypes:types.slice(),deliveryReceipt:'supported'};
 r.events.push({time:new Date().toLocaleTimeString('zh-CN'),text:profile==='day'?'已确认按日混合文件采集；请求范围与实际范围分别保留':'已保存所选设备采集能力快照（演练）'});
 state.active=i.id;workUI.collectionKey=i.id+'/'+r.id;workUI.collectionMode='push';workUI.collectionStatus='';workUI.collectionQuery='';
 const origin=deskCollectOrigin;deskCollectOrigin=null;closeModal();persistWork();
 if(origin){desk.tool='collect';workUI.issueMode='collect';page=origin;render();}else go('collectioncenter');
 }catch(err){$('#capture-error').textContent=err.message;}
};
const productAvailableIndices=availableIndices;
availableIndices=function(t=currentTask()){return t?.types?.includes('混合日志')?recordsForTask(t).map((_,n)=>n):productAvailableIndices(t);};
const productAdvance=M.advance;
M.advance=function(r){
 if(!M.terminal.includes(r.status)&&r.step===1&&r.scenario==='noreceipt'){
  r.step=2;r.status='等待 SDK 响应';r.events.push({time:new Date().toLocaleTimeString('zh-CN'),text:'渠道不提供设备送达回执：送达状态未知，继续等待 SDK 实际响应'});return;
 }
 productAdvance(r);
};
const productRetry=M.retry;
M.retry=function(s,i,r){let result=productRetry(s,i,r);if(result&&!result.duplicate&&r.product)result.request.product=JSON.parse(JSON.stringify(r.product));return result;};
const productCollectionMarkup=inlineCollection;
inlineCollection=function(x){
 const r=x.request;let html=productCollectionMarkup(x);
 html=html.replace('<option value="success"',`<option value="noreceipt" ${r.scenario==='noreceipt'?'selected':''}>渠道无送达回执，SDK 后续响应</option><option value="success"`);
 if(r.scenario==='noreceipt')html=html.replace(/(<small>设备送达<\/small><b>)[^<]+/,'$1未知 · 渠道无回执');
 const scope=r.product?.scope;
 const note=scope?`<section class="product-card"><h3>采集范围与结果覆盖</h3><p>期望：${esc(r.begin.replace('T',' '))} — ${esc(r.end.replace('T',' '))}</p><p>实际：${esc(scope.begin.replace('T',' '))} — ${esc(scope.end.replace('T',' '))}${scope.expanded?' · 已确认扩大范围':''}</p><small>${r.product.profile==='day'?'混合文件解析后分类；不承诺端侧分钟裁剪。':'能力为本地演练配置。'} 可读文件 ${r.files.filter(f=>f.status==='可查').length} / ${r.files.length}；样例事件仍使用固定演示时间。</small></section>`:'';
 const late=M.terminal.includes(r.status)&&r.status!=='完成'?`<details class="product-card"><summary>演练迟到回执</summary><p>终态后只追加事件；不重新激活请求，不自动替换调查证据。</p>${btn('模拟收到迟到回执',`productLateReceipt('${x.key}')`,'',r.events.some(e=>e.kind==='late-demo'))}</details>`:'';
 const canceled=r.status==='已取消'?'<div class="notice">已停止后续本地推进。已经送达设备的指令可能仍执行；已有文件与记录保留。</div>':'';
 return note+canceled+html+late;
};
function productLateReceipt(key){const x=collectionSources().find(x=>x.key===key);P.lateReceipt(x.request);save();render();}
function productNotifications(){return collectionSources().flatMap(x=>x.request.events.map((e,n)=>({id:x.key+'/'+n,key:x.key,issue:x.issue,text:e.text,time:e.time})).filter(e=>e.text.startsWith('生成本地站内通知'))).reverse();}
function productShowNotifications(){const read=state.productNoticeRead||{},items=productNotifications();openModal('采集结果提醒',`<p>设备采集指令与结果提醒独立。以下只展示已有本地结果事件，不发送外部消息。</p>${items.map(e=>`<section class="product-card"><b>${esc(e.issue.id)} · ${esc(e.issue.title)}</b><p>${esc(e.text)} · ${esc(e.time)}</p><div class="toolbar">${badge(read[e.id]?'已读':'未读')}${btn('查看采集结果',`productOpenNotice('${e.id}','${e.key}')`)}${btn('标记已读',`productReadNotice('${e.id}')`,'',!!read[e.id])}</div></section>`).join('')||'<div class="empty">尚无采集结果提醒。</div>'}${btn('返回','closeModal()')}`,true);}
function productReadNotice(id){state.productNoticeRead=state.productNoticeRead||{};state.productNoticeRead[id]=true;save();render();productShowNotifications();}
function productOpenNotice(id,key){state.productNoticeRead=state.productNoticeRead||{};state.productNoticeRead[id]=true;save();closeModal();openSourceCollection(key);}
const productNoticeRender=render;
render=function(){productNoticeRender();let button=$('#product-notices');if(!button){button=document.createElement('button');button.id='product-notices';button.onclick=productShowNotifications;$('#global-status').after(button);}const unread=productNotifications().filter(e=>!state.productNoticeRead?.[e.id]).length;button.textContent='结果提醒'+(unread?' · '+unread:'');};
