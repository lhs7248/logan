// Approved Aliyun parameter flow. Drafts are server-backed; this module never
// invokes /collections and never manufactures a channel/device/upload receipt.
export const collectionTargetLabels={device:'设备',account:'账号',alias:'别名'};
const app='com.hxl.xlb.test';
const iso=value=>!value?'':/Z$|[+-]\d\d:\d\d$/.test(value)?value:value.length===16?value+':00+08:00':value+'+08:00';
export function collectionPayload(d={}){return {app:d.app||app,targetType:d.targetType||'device',targetValue:d.targetValue||'',bindingConfirmed:Boolean(d.bindingConfirmed),issueId:d.issueId||'',title:d.title||'',begin:iso(d.begin),end:iso(d.end)};}
export function collectionPatchPayload(d,saved){const payload=collectionPayload(d);if(saved&&d.targetType===saved.targetType&&d.targetValue===saved.targetValue&&d.targetValue?.includes('[REDACTED')){delete payload.targetValue;delete payload.bindingConfirmed;}return payload;}
export function validateCollectionStep(d,step,saved=null){
 const preservedMask=Boolean(saved?.id&&saved.targetType===d.targetType&&saved.targetValue===d.targetValue&&d.targetValue?.includes('[REDACTED')&&saved.bindingConfirmed&&d.bindingConfirmed);
 if(!Object.hasOwn(collectionTargetLabels,d.targetType))throw Error('标签尚未绑定，暂不使用；请选择设备、账号或别名。');
 if(!d.targetValue?.trim()||d.targetValue.includes('[REDACTED')&&!preservedMask)throw Error(d.targetType==='device'?'请填写 NativePushAliyun 日志中的阿里云 deviceId。':'请填写明确的推送目标；已脱敏的目标需重新输入。');
 if(d.targetType!=='device'&&!d.bindingConfirmed)throw Error('请确认该账号或别名已在当前应用绑定成功。');
 if(step>=2){const p=collectionPayload(d);if(Boolean(p.begin)!==Boolean(p.end))throw Error('请完整填写故障时段的开始和结束时间，或全部留空。');if(p.begin&&(!Number.isFinite(Date.parse(p.begin))||!Number.isFinite(Date.parse(p.end))||Date.parse(p.end)<=Date.parse(p.begin)))throw Error('故障结束时间必须晚于开始时间。');}
}
const localTime=value=>value?new Date(value).toLocaleString('sv-SE',{timeZone:'Asia/Shanghai'}).replace(' ','T').slice(0,16):'';
export function createCollectionUI({el,button,field,form,heading,note,pill,empty,modal,api,refresh,getState,upload,selectRun,openIssue,assetCard}){
 const ui=new Map();
 const state=()=>getState();
 const view=()=>{const key=state().project;if(!ui.has(key))ui.set(key,{mode:'push',selected:'',query:''});return ui.get(key);};
 const drafts=()=>state().workspace.collectionDrafts||[];
 const selected=()=>drafts().find(d=>d.id===view().selected)||drafts().at(-1);
 async function load(){const project=state().project;const result=await api('/collection-drafts');if(project===state().project)state().workspace.collectionDrafts=result;return result;}
 const change=async update=>{Object.assign(view(),update);await refresh();};
 const rows=(items,kind='draft')=>items.length?items.map(d=>{const b=button('',()=>change({selected:d.id}),'rail-item '+(selected()?.id===d.id?'chosen':''));b.append(el('b',{},d.title||'未命名回捞草稿'),pill(d.availability==='expired'?'已到期':'未发送草稿'),el('small',{},`${collectionTargetLabels[d.targetType]||'目标待填写'} · ${d.targetValue||'未知'} · ${d.issueId?'已关联工单':'未关联工单'}`));return b;}):empty(kind==='failed'?'没有失败的真实回捞请求。未发送草稿不记为失败。':'暂无回捞请求或草稿。可先填写推送参数并保存草稿。');
 function page(){const v=view(),d=selected();const items=drafts().filter(r=>!v.query||JSON.stringify([r.title,r.targetValue,r.issueId]).toLowerCase().includes(v.query.toLowerCase()));return el('div',{},
  el('div',{class:'desk-page-heading'},el('div',{},el('span',{class:'eyebrow'},'LOG RETRIEVAL'),el('h1',{},'日志回捞'),note('从工单或手动输入取得阿里云推送参数，触发客户端上传日志。平台收到并更新状态后，回捞成功。'))),
  el('div',{class:'toolbar'},button('发起日志回捞',()=>wizard(),'primary'),button('上传日志文件',upload)),
  note('回捞：通过阿里云推送触发客户端上传。上传文件：导入已有日志。补充回捞：从工单继续发起。当前阿里云依赖暂缓接入，可保存未发送草稿。'),
  el('div',{class:'work-tabs',role:'tablist','aria-label':'回捞追踪视图'},[['push','推送与设备响应'],['files','文件与处理结果'],['retry','失败与重试']].map(([id,title])=>{const b=button(title,()=>change({mode:id}),v.mode===id?'tab-selected':'');b.setAttribute('role','tab');b.setAttribute('aria-selected',String(v.mode===id));return b;})),
  el('div',{class:'master-workspace'},el('div',{class:'object-rail'},el('label',{},`回捞请求与草稿 · ${drafts().length}`),form([field('collectionQuery','请求、目标、工单',v.query)],'筛选请求',async values=>change({query:values.collectionQuery})),rows(v.mode==='retry'?[]:items,v.mode==='retry'?'failed':'draft')),el('div',{class:'work-pane'},v.mode==='retry'?el('section',{class:'panel'},el('h3',{},'失败与重试'),empty('尚无实际发送或失败的请求。'),note('真实接入后，此处按失败阶段展示原因，仅重试未满足部分并保留父请求。当前不会将未发送草稿标记为失败，也不会生成重试回执。'),button('填写新的回捞草稿',()=>wizard())):detail(d,v.mode))));}
 function detail(d,mode='push'){
  if(!d)return el('section',{class:'panel'},el('h3',{},'尚未发起回捞'),empty('先填写应用和阿里云推送目标，可独立保存或关联工单。'),chain(),button('填写推送参数',()=>wizard(),'primary'));
  const expired=d.availability==='expired';return el('div',{},el('section',{class:'capture-detail-head'},el('span',{class:'eyebrow'},'阿里云回捞 · 草稿'),el('h2',{},d.title||'未命名回捞草稿'),note('Android 测试应用 · '+app),note(`${collectionTargetLabels[d.targetType]||'目标'}：${d.targetValue||'尚未填写'} · ${d.issueId?'已关联工单':'未关联工单'}`),pill(expired?'草稿已到期':'未发送')),
   el('section',{class:'product-card'},el('h3',{},'回捞进度'),chain(),el('div',{class:'notice'},'阿里云推送及客户端依赖暂未接入。草稿保存不等于已发送；渠道受理、客户端接收、上传和平台接收均无真实回执。'),el('div',{class:'toolbar'},expired?button('重新填写草稿',()=>wizard(d.issueId)):button('继续编辑草稿',()=>wizard(d.issueId,d),'primary'),button('手动导入补充日志',async()=>{await openIssue(d.issueId||'');upload();}),d.issueId?button('返回关联调查',()=>openIssue(d.issueId)):null)),
   mode==='files'?el('section',{class:'product-card'},el('h3',{},'返回文件与处理结果'),empty('尚未收到此回捞请求的文件。手动导入文件不冒充推送回捞结果。')):null,
   el('details',{class:'panel'},el('summary',{},'推送参数与记录'),note(`参数来源：操作人明确填写；保存时间：${d.updatedAt||d.createdAt||'未知'}`),note('故障线索：'+(d.begin?localTime(d.begin).replace('T',' ')+' — '+localTime(d.end).replace('T',' '):'未填写')),note('消息类型、命令字、载荷字段及通知扩展参数待实际协议确认。包名不能代替阿里云服务端应用配置。'),note('草稿编号：'+d.id)));
 }
 function chain(){return el('ol',{class:'ali-chain'},[['阿里云推送','未发送'],['客户端接收','无接收证据'],['客户端上传','尚未上传'],['平台接收与状态更新','未收到日志'],['回捞结果','尚未发起']].map(([title,status])=>el('li',{},el('strong',{},title),el('span',{},status))));}
 function tool(){const s=state(),i=(s.workspace.issues||[]).find(i=>i.id===s.issueId);const linked=drafts().filter(d=>d.issueId===s.issueId);return el('div',{},el('div',{class:'tool-heading'},el('span',{class:'eyebrow'},'COLLECT MORE EVIDENCE'),el('h2',{},'补充采集'),note('针对当前问题缺少的日志继续回捞。返回后先阅读，再选入新证据，继续调查。')),i?note(`工单设备线索（非推送目标）：${i.context?.device||'未知'}`):null,el('div',{class:'toolbar'},button('补充采集',()=>wizard(s.issueId),'primary'),button('上传已有日志',upload)),el('div',{class:'notice'},'阿里云依赖暂缓接入。可以保存明确推送参数，或通过上传日志补充当前调查。不会把工单普通设备字段当作阿里云 deviceId。'),linked.length?el('div',{},el('h3',{},'此问题的回捞草稿'),linked.map(d=>el('section',{class:'product-card'},el('strong',{},d.title||'未命名回捞草稿'),pill(d.availability==='expired'?'已到期':'未发送'),note(collectionTargetLabels[d.targetType]+' · '+(d.targetValue||'待填写')),button('继续填写',()=>wizard(s.issueId,d))))):empty('尚无关联的回捞草稿。'),el('h3',{},'人工补充材料'),(s.workspace.assets||[]).filter(a=>a.issueId===s.issueId).map(a=>el('section',{class:'product-card'},el('strong',{},a.name),note('手动导入 · 与回捞回执分开记录'),button('打开材料',async()=>{const runs=(s.workspace.parseRuns||[]).filter(r=>r.assetId===a.id);const r=[...runs].reverse().find(r=>['succeeded','partial'].includes(r.status))||runs.at(-1);if(r)await selectRun(r.id);else modal('材料待解析',el('div',{},assetCard(a)));}))) );}
 async function wizard(issueId='',existing=null){
  const project=state().project;let saved=existing?await api('/collection-drafts/'+existing.id):null;if(saved?.availability==='expired')saved=null;
  let draft=collectionPayload(saved||{issueId}),step=1;
  function current(f){const v=Object.fromEntries(new FormData(f));return {...draft,...v,bindingConfirmed:Boolean(f.querySelector('[name=bindingConfirmed]')?.checked??draft.bindingConfirmed)};}
  function show(){if(state().project!==project)throw Error('项目已切换，请在当前项目重新打开回捞参数。');let fields=[];
   if(step===1){fields=[note('从工单带入已明确保存的阿里云参数，或手动填写推送目标。'),field('app','应用',app,'text',[{value:app,label:'Android 测试应用 · '+app}]),field('targetType','推送目标类型',draft.targetType,'text',Object.entries(collectionTargetLabels).map(([value,label])=>({value,label}))),field('targetValue',draft.targetType==='device'?'阿里云 deviceId':draft.targetType==='account'?'业务 userId':'已绑定的手机号（别名）',draft.targetValue),note(draft.targetType==='device'?'从 Debug 日志 NativePushAliyun 复制 deviceId，不使用普通业务设备 ID。':draft.targetType==='account'?'填写已绑定成功的业务 userId。':'填写已绑定成功的手机号，按别名寻址，不发送短信。'),draft.targetType!=='device'?el('label',{class:'check'},el('input',{name:'bindingConfirmed',type:'checkbox',checked:draft.bindingConfirmed}),`我已确认该${collectionTargetLabels[draft.targetType]}在当前应用绑定成功`):null,note('标签：当前初始化没有绑定业务标签，暂不使用。绑定状态未查询，由操作人核对。'),draft.issueId?el('div',{class:'notice'},'已关联当前工单；未从普通设备字段推断阿里云 deviceId，请明确填写。'):null];}
   if(step===2){fields=[el('p',{},`${collectionTargetLabels[draft.targetType]}：${draft.targetValue}`),note('以下信息用于记录回捞用途和关联调查，尚未映射成客户端指令。'),field('title','回捞用途（可选）',draft.title),field('issueId','关联工单（可选）',draft.issueId,'text',[{value:'',label:'暂不关联工单'},...(state().workspace.issues||[]).map(i=>({value:i.id,label:i.title}))]),el('details',{},el('summary',{},'故障时段（可选，作为排查线索）'),field('begin','开始 · 北京时间',localTime(draft.begin),'datetime-local'),field('end','结束 · 北京时间',localTime(draft.end),'datetime-local')),el('div',{class:'notice'},el('strong',{},'回捞流程已明确，具体消息协议待对接'),el('p',{},'客户端接收推送后上传日志，平台收到后更新回捞状态。消息类型、命令字和载荷字段待提供，不自动生成。'),el('p',{},'通知扩展参数用于打开业务页面，需按场景确认，本次不代填。'))];}
   if(step===3){fields=[el('h3',{},'核对推送参数'),el('dl',{class:'product-facts'},[['应用','Android 测试应用'],['包名',draft.app],['目标类型',collectionTargetLabels[draft.targetType]],['目标值',draft.targetValue],['参数来源','操作人明确填写'],['关联工单',(state().workspace.issues||[]).find(i=>i.id===draft.issueId)?.title||'暂不关联']].flatMap(([k,v])=>[el('dt',{},k),el('dd',{},v)])),chain(),el('div',{class:'notice'},'当前仅保存未发送草稿。阿里云及客户端依赖暂缓接入，无法发起真实推送；没有创建回捞请求、命令或回执。'),el('button',{type:'button',disabled:true},'发起回捞（待接入）')];}
   let f;f=form(fields,step===3?'保存未发送草稿':'下一步',async(v,formNode)=>{draft=collectionPayload(current(formNode));validateCollectionStep(draft,step,saved);if(step===3){await save();return;}step++;show();});
   if(step===1)f.querySelector('[name=targetType]').addEventListener('change',event=>{draft=collectionPayload({...current(f),targetType:event.target.value,targetValue:'',bindingConfirmed:false});show();});
   const submit=f.querySelector('button[type=submit]');
   const body=el('div',{class:'capture-body'},[...f.childNodes].filter(n=>n!==submit));
   const footer=el('div',{class:'capture-footer'},el('div',{},button('保存草稿并退出',async()=>{draft=collectionPayload(current(f));await save();}),button('取消',()=>document.getElementById('dialog').close())),el('div',{},step>1?button('上一步',()=>{draft=collectionPayload(current(f));step--;show();}):null,submit));
   f.replaceChildren(body,footer);f.className='capture-form';
   modal(issueId?'补充回捞 · 阿里云推送':'日志回捞 · 阿里云推送',el('div',{class:'capture-wizard'},el('ol',{class:'capture-steps'},['填写推送参数','关联排查信息','核对并发起'].map((title,n)=>el('li',{class:step===n+1?'active':'','aria-current':step===n+1?'step':undefined},`${n+1} ${title}`))),f));document.getElementById('dialog').className='capture-dialog';
  }
  async function save(){if(state().project!==project)throw Error('项目已切换，草稿未提交。');saved=await api(saved?'/collection-drafts/'+saved.id:'/collection-drafts',{method:saved?'PATCH':'POST',revision:saved?.revision,body:saved?collectionPatchPayload(draft,saved):collectionPayload(draft)});view().selected=saved.id;await refresh();document.getElementById('dialog').close();}
  show();
 }
 return {page,tool,wizard,load};
}
