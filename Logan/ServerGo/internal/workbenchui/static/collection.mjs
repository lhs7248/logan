// Preserve the approved draft-first flow; configured internal-service requests
// and signed callback facts remain distinct from device delivery and log receipt.
import {collectionActive,collectionStatusLabel,collectionProgress,createCollectionMutations} from './collection-state.mjs';
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
 const view=()=>{const key=state().project;if(!ui.has(key))ui.set(key,{mode:'push',selected:'',query:'',historyOpen:new Map()});return ui.get(key);};
 const drafts=()=>state().workspace.collectionDrafts||[];
 const collections=()=>state().workspace.collections||[];
 const objects=()=>[...drafts(),...collections()].sort((a,b)=>String(a.createdAt||'').localeCompare(String(b.createdAt||'')));
 const configured=()=>Boolean(state().workspace.capabilities?.collections?.configured);
 const canSend=()=>configured()&&state().session?.role!=='viewer';
 const mutations=createCollectionMutations({api,scope:()=>state().project+'\0'+state().token});
 const selected=()=>objects().find(d=>d.id===view().selected)||objects().at(-1);
 async function load(){const project=state().project,token=state().token;const [draftRows,requests]=await Promise.all([api('/collection-drafts'),api('/collections')]);if(project===state().project&&token===state().token){state().workspace.collectionDrafts=draftRows;state().workspace.collections=requests;}return draftRows;}
 const change=async update=>{Object.assign(view(),update);await refresh();};
 const rows=(items,kind='draft')=>items.length?items.map(d=>{const b=button('',()=>change({selected:d.id}),'rail-item '+(selected()?.id===d.id?'chosen':''));b.append(el('b',{},d.title||(d.currentAttemptId?'未命名回捞请求':'未命名回捞草稿')),pill(d.availability==='expired'?'已到期':d.currentAttemptId?collectionStatusLabel(d.status):collections().some(c=>c.draftId===d.id)?'参数草稿 · 已发起':'未发送草稿'),el('small',{},`${collectionTargetLabels[d.targetType]||'目标待填写'} · ${d.targetValue||'未知'} · ${d.issueId?'已关联工单':'未关联工单'}`));return b;}):empty(kind==='failed'?'没有失败的真实回捞请求。未发送草稿不记为失败。':'暂无回捞请求或草稿。可先填写推送参数并保存草稿。');
 function page(){const v=view();let items=objects().filter(r=>!v.query||JSON.stringify([r.id,r.title,r.targetValue,r.issueId]).toLowerCase().includes(v.query.toLowerCase()));if(v.mode==='retry')items=items.filter(r=>r.currentAttemptId&&['dispatch_unknown','push_failed','timed_out','cancelled'].includes(r.status));const d=items.find(r=>r.id===v.selected)||items.at(-1);if(d)v.selected=d.id;return el('div',{},
  el('div',{class:'desk-page-heading'},el('div',{},el('span',{class:'eyebrow'},'LOG RETRIEVAL'),el('h1',{},'日志回捞'),note('从工单或手动输入取得阿里云推送参数，触发客户端上传日志。平台收到并更新状态后，回捞成功。'))),
  el('div',{class:'toolbar'},button('发起日志回捞',()=>wizard(),'primary'),button('上传日志文件',upload)),
  note(configured()?'已接入内部推送服务 V1。受理和推送回调分别追踪；推送回调不代表客户端收到或日志回捞成功。':'内部推送服务尚未配置，可保存未发送草稿、导入已有日志并继续人工调查。'),
  el('div',{class:'work-tabs',role:'tablist','aria-label':'回捞追踪视图'},[['push','推送与设备响应'],['files','文件与处理结果'],['retry','失败与重试']].map(([id,title])=>{const b=button(title,()=>change({mode:id}),v.mode===id?'tab-selected':'');b.setAttribute('role','tab');b.setAttribute('aria-selected',String(v.mode===id));return b;})),
  el('div',{class:'master-workspace'},el('div',{class:'object-rail'},el('label',{},`回捞请求与草稿 · ${objects().length}`),form([field('collectionQuery','请求、目标、工单',v.query)],'筛选请求',async values=>change({query:values.collectionQuery})),rows(items,v.mode==='retry'?'failed':'draft')),el('div',{class:'work-pane'},v.mode==='retry'&&!d?el('section',{class:'panel'},el('h3',{},'失败与重试'),empty('尚无失败、超时或结果未知的真实请求。未发送草稿不会记为失败。'),button('填写新的回捞草稿',()=>wizard())):detail(d,v.mode))));}

 function detail(d,mode='push'){
  if(d?.currentAttemptId)return requestDetail(d,mode);
  if(!d)return el('section',{class:'panel'},el('h3',{},'尚未发起回捞'),empty('先填写应用和阿里云推送目标，可独立保存或关联工单。'),chain(),button('填写推送参数',()=>wizard(),'primary'));
  const expired=d.availability==='expired';return el('div',{},el('section',{class:'capture-detail-head'},el('span',{class:'eyebrow'},'阿里云回捞 · 草稿'),el('h2',{},d.title||'未命名回捞草稿'),note('Android 测试应用 · '+app),note(`${collectionTargetLabels[d.targetType]||'目标'}：${d.targetValue||'尚未填写'} · ${d.issueId?'已关联工单':'未关联工单'}`),pill(expired?'草稿已到期':'未发送')),
   el('section',{class:'product-card'},el('h3',{},'回捞进度'),chain(),el('div',{class:'notice'},configured()?'此条仍是未发送草稿。确认发起后才创建正式请求，服务受理和推送回调分开显示。':'内部推送服务尚未配置。草稿保存不等于已发送；渠道受理、客户端接收、上传和平台接收均无真实回执。'),el('div',{class:'toolbar'},expired?button('重新填写草稿',()=>wizard(d.issueId)):button('继续编辑草稿',()=>wizard(d.issueId,d),'primary'),button('手动导入补充日志',async()=>{await openIssue(d.issueId||'');upload();}),d.issueId?button('返回关联调查',()=>openIssue(d.issueId)):null)),
   mode==='files'?el('section',{class:'product-card'},el('h3',{},'返回文件与处理结果'),empty('尚未收到此回捞请求的文件。手动导入文件不冒充推送回捞结果。')):null,
   el('details',{class:'panel'},el('summary',{},'推送参数与记录'),note(`参数来源：操作人明确填写；保存时间：${d.updatedAt||d.createdAt||'未知'}`),note('故障线索：'+(d.begin?localTime(d.begin).replace('T',' ')+' — '+localTime(d.end).replace('T',' '):'未填写')),note('消息类型、命令字、载荷字段及通知扩展参数待实际协议确认。包名不能代替阿里云服务端应用配置。'),note('草稿编号：'+d.id)));
 }
 function requestDetail(c,mode){
  const requestView=view();
  const historyOpen=requestView.historyOpen.has(c.id)?requestView.historyOpen.get(c.id):mode==='retry';
  const attempts=(state().workspace.pushAttempts||[]).filter(a=>a.collectionId===c.id);
  const attempt=attempts.find(a=>a.id===c.currentAttemptId)||{};
  const events=(state().workspace.pushEvents||[]).filter(e=>e.collectionId===c.id||e.requestId===c.id);
  const mayRetry=canSend()&&['dispatch_unknown','push_failed','timed_out','cancelled'].includes(c.status)&&Number(attempt.number||0)<5;
  return el('div',{},el('section',{class:'capture-detail-head'},el('span',{class:'eyebrow'},'内部推送服务 · 正式请求'),el('h2',{},c.title||'日志回捞请求'),pill(collectionStatusLabel(c.status)),note(`${collectionTargetLabels[c.targetType]||'目标'} · ${c.targetValue||'未知'} · ${c.issueId?'已关联工单':'未关联工单'}`),note('请求编号：'+c.id)),
   el('section',{class:'product-card'},el('h3',{},'回捞进度'),el('ol',{class:'ali-chain'},collectionProgress(c,attempt).map(([title,status])=>el('li',{},el('strong',{},title),el('span',{},status))))),
   el('section',{class:'product-card'},el('h3',{},'当前发送尝试'),note(`第 ${attempt.number||attempts.length||1} 次 · ${attempt.id||c.currentAttemptId}`),note('等待截止（北京时间）：'+(localTime(c.deadlineAt||attempt.deadlineAt).replace('T',' ')||'未知')),note('内部服务任务号：'+(attempt.providerRequestId||'尚未获得')),note('结果未知时不会自动重发；重试可能使先前已送达的指令再次触达客户端。'),el('div',{class:'toolbar'},button('刷新推送状态',refresh),mayRetry?button('确认并创建新尝试',()=>mutationDialog(c,'retries'),'primary'):null,canSend()&&collectionActive(c)?button('取消后续处理',()=>mutationDialog(c,'cancel')):null,c.issueId?button('返回关联调查',()=>openIssue(c.issueId)):null)),
   el('section',{class:'product-card'},el('h3',{},mode==='files'?'返回文件与处理结果':'日志回传尚待确认'),empty('当前协议仅接收推送结果回调，尚未建立 App 上传与本请求的自动关联。已有上传保留在日志资料库。'),note('手动导入文件不会冒充此推送请求的回捞回执。'),button('手动导入补充日志',async()=>{await openIssue(c.issueId||'');upload();})),
   el('details',{class:'panel',open:historyOpen?true:undefined,onToggle:event=>requestView.historyOpen.set(c.id,event.currentTarget.open)},el('summary',{},'全部尝试与回调记录'),attempts.map(a=>el('article',{class:'card'},el('strong',{},`第 ${a.number} 次 · ${a.id}`),note(`提交：${a.dispatchStatus||'unknown'} · 推送：${a.pushStatus||'unknown'}`))),events.length?events.map(e=>el('article',{class:'card'},el('strong',{},e.eventType||e.type||'处理记录'),note(`发生（北京时间）：${localTime(e.occurredAt||e.createdAt).replace('T',' ')||'未知'} · 接收（北京时间）：${localTime(e.receivedAt||e.createdAt).replace('T',' ')||'未知'}`),note(`尝试：${e.attemptId||'未知'} · 处理：${e.outcome||'已记录'}${e.reasonCode?' · '+e.reasonCode:''}`))):empty('尚无签名回调记录；不能据此推断发送失败。')));
 }
 function mutationDialog(c,action){
  const retry=action==='retries',mutationView=view();
  modal(retry?'确认重试推送':'确认取消后续处理',form([note(retry?'新尝试会保留全部旧记录。旧指令可能已经发送，重复推送不能保证客户端只收到一次。':'取消只停止平台后续处理，不能撤回已交付的指令，客户端仍可能上传日志。'),field('reason','操作原因','','textarea'),el('label',{class:'check'},el('input',{name:'confirmed',type:'checkbox'}),'我已了解并确认本次操作')],retry?'创建新尝试':'确认取消',async(v,f)=>{
   if(!v.reason.trim()||!f.querySelector('[name=confirmed]').checked)throw Error('请填写原因并勾选确认。');
   const result=await mutations.submit(`/collections/${c.id}/${action}`,{confirmed:true,reason:v.reason.trim()},c.revision);
   mutationView.selected=result.id;if(retry)mutationView.mode='push';document.getElementById('dialog').close();await refresh();
  }));
 }

 function chain(){return el('ol',{class:'ali-chain'},[['内部推送服务','未提交'],['下游推送渠道','无结果回调'],['客户端接收','无接收证据'],['日志回传','未收到关联文件'],['回捞结果','尚未发起']].map(([title,status])=>el('li',{},el('strong',{},title),el('span',{},status))));}
 function tool(){const s=state(),i=(s.workspace.issues||[]).find(i=>i.id===s.issueId);const linked=drafts().filter(d=>d.issueId===s.issueId);return el('div',{},el('div',{class:'tool-heading'},el('span',{class:'eyebrow'},'COLLECT MORE EVIDENCE'),el('h2',{},'补充采集'),note('针对当前问题缺少的日志继续回捞。返回后先阅读，再选入新证据，继续调查。')),i?note(`工单设备线索（非推送目标）：${i.context?.device||'未知'}`):null,el('div',{class:'toolbar'},button('补充采集',()=>wizard(s.issueId),'primary'),button('上传已有日志',upload)),el('div',{class:'notice'},configured()?'通过内部推送服务发起，只有签名回调能更新推送结果。日志上传与本次请求尚无自动关联，不会把工单设备字段当作推送 deviceId。':'内部推送服务尚未配置。可保存明确推送参数或上传已有日志，不会把工单设备字段当作推送 deviceId。'),...collections().filter(c=>c.issueId===s.issueId).map(c=>el('section',{class:'product-card'},el('strong',{},c.title||'回捞请求'),pill(collectionStatusLabel(c.status)),button('追踪此请求',()=>{view().selected=c.id;state().page='collection';return refresh();}))),linked.length?el('div',{},el('h3',{},'此问题的回捞草稿'),linked.map(d=>el('section',{class:'product-card'},el('strong',{},d.title||'未命名回捞草稿'),pill(d.availability==='expired'?'已到期':'未发送'),note(collectionTargetLabels[d.targetType]+' · '+(d.targetValue||'待填写')),button('继续填写',()=>wizard(s.issueId,d))))):empty('尚无关联的回捞草稿。'),el('h3',{},'人工补充材料'),(s.workspace.assets||[]).filter(a=>a.issueId===s.issueId).map(a=>el('section',{class:'product-card'},el('strong',{},a.name),note('手动导入 · 与回捞回执分开记录'),button('打开材料',async()=>{const runs=(s.workspace.parseRuns||[]).filter(r=>r.assetId===a.id);const r=[...runs].reverse().find(r=>['succeeded','partial'].includes(r.status))||runs.at(-1);if(r)await selectRun(r.id);else modal('材料待解析',el('div',{},assetCard(a)));}))) );}
 async function wizard(issueId='',existing=null){
  const project=state().project,token=state().token;let saved=existing?await api('/collection-drafts/'+existing.id):null;if(saved?.availability==='expired')saved=null;
  let draft=collectionPayload(saved||{issueId}),step=1,submission=null,confirmedPayload=null,preparing=false;
  const locked=()=>{if(submission||preparing)throw Error('提交参数已锁定；只能重试原提交，或关闭后检查服务端请求。');};
  function current(f){const v=Object.fromEntries(new FormData(f));return {...draft,...v,bindingConfirmed:Boolean(f.querySelector('[name=bindingConfirmed]')?.checked??draft.bindingConfirmed)};}
  function show(){if(state().project!==project)throw Error('项目已切换，请在当前项目重新打开回捞参数。');let fields=[];
   if(step===1){fields=[note('从工单带入已明确保存的阿里云参数，或手动填写推送目标。'),field('app','应用',app,'text',[{value:app,label:'Android 测试应用 · '+app}]),field('targetType','推送目标类型',draft.targetType,'text',Object.entries(collectionTargetLabels).map(([value,label])=>({value,label}))),field('targetValue',draft.targetType==='device'?'阿里云 deviceId':draft.targetType==='account'?'业务 userId':'已绑定的手机号（别名）',draft.targetValue),note(draft.targetType==='device'?'从 Debug 日志 NativePushAliyun 复制 deviceId，不使用普通业务设备 ID。':draft.targetType==='account'?'填写已绑定成功的业务 userId。':'填写已绑定成功的手机号，按别名寻址，不发送短信。'),draft.targetType!=='device'?el('label',{class:'check'},el('input',{name:'bindingConfirmed',type:'checkbox',checked:draft.bindingConfirmed}),`我已确认该${collectionTargetLabels[draft.targetType]}在当前应用绑定成功`):null,note('标签：当前初始化没有绑定业务标签，暂不使用。绑定状态未查询，由操作人核对。'),draft.issueId?el('div',{class:'notice'},'已关联当前工单；未从普通设备字段推断阿里云 deviceId，请明确填写。'):null];}
   if(step===2){fields=[el('p',{},`${collectionTargetLabels[draft.targetType]}：${draft.targetValue}`),note('以下信息作为调查上下文提交给内部服务，不在平台中臆造移动端命令。'),field('title','回捞用途（可选）',draft.title),field('issueId','关联工单（可选）',draft.issueId,'text',[{value:'',label:'暂不关联工单'},...(state().workspace.issues||[]).map(i=>({value:i.id,label:i.title}))]),el('details',{},el('summary',{},'故障时段（可选，作为排查线索）'),field('begin','开始 · 北京时间',localTime(draft.begin),'datetime-local'),field('end','结束 · 北京时间',localTime(draft.end),'datetime-local')),el('div',{class:'notice'},el('strong',{},'平台使用内部推送服务 V1 协议'),el('p',{},'内部服务负责下游消息适配，平台记录受理及签名回调。客户端收到和日志落地仍需各自证据。'),el('p',{},'通知扩展参数用于打开业务页面，需按场景确认，本次不代填。'))];}
   if(step===3){fields=[el('h3',{},'核对推送参数'),el('dl',{class:'product-facts'},[['应用','Android 测试应用'],['包名',draft.app],['目标类型',collectionTargetLabels[draft.targetType]],['目标值',draft.targetValue],['参数来源','操作人明确填写'],['关联工单',(state().workspace.issues||[]).find(i=>i.id===draft.issueId)?.title||'暂不关联']].flatMap(([k,v])=>[el('dt',{},k),el('dd',{},v)])),chain(),el('div',{class:'notice'},configured()?'确认后通过内部服务发起。推送回调只证明渠道受理，不等于客户端收到或日志回捞成功。':'当前仅保存未发送草稿。内部推送服务未配置，不能发起真实推送。'),!canSend()?el('button',{type:'button',disabled:true},configured()?'当前角色不能发起':'发起回捞（待接入）'):null];}
   let f;f=form(fields,step===3?(canSend()?'确认并发起回捞':'保存未发送草稿'):'下一步',async(v,formNode)=>{if(preparing)throw Error('提交参数已锁定，请等待草稿保存。');const next=collectionPayload(current(formNode));if(submission&&JSON.stringify(next)!==confirmedPayload)throw Error('提交参数已锁定，请关闭后检查服务端请求。');draft=next;validateCollectionStep(draft,step,saved);if(step===3){if(canSend())await send();else await save();return;}step++;show();});
   if(step===1)f.querySelector('[name=targetType]').addEventListener('change',event=>{locked();draft=collectionPayload({...current(f),targetType:event.target.value,targetValue:'',bindingConfirmed:false});show();});
   const submit=f.querySelector('button[type=submit]');
   const body=el('div',{class:'capture-body'},[...f.childNodes].filter(n=>n!==submit));
   const footer=el('div',{class:'capture-footer'},el('div',{},button('保存草稿并退出',async()=>{locked();draft=collectionPayload(current(f));await save();}),button('取消',()=>document.getElementById('dialog').close())),el('div',{},step>1?button('上一步',()=>{locked();draft=collectionPayload(current(f));step--;show();}):null,submit));
   if(submission||preparing){for(const control of footer.querySelectorAll('button')){if(control!==submit&&control.textContent!=='取消')control.disabled=true;}body.append(note('提交参数已锁定。可使用原提交重试并查询结果，或关闭后检查服务端请求；不要另建请求盲目重发。'));}
   f.replaceChildren(body,footer);f.className='capture-form';
   modal(issueId?'补充回捞 · 内部推送服务':'日志回捞 · 内部推送服务',el('div',{class:'capture-wizard'},el('ol',{class:'capture-steps'},['填写推送参数','关联排查信息','核对并发起'].map((title,n)=>el('li',{class:step===n+1?'active':'','aria-current':step===n+1?'step':undefined},`${n+1} ${title}`))),f));document.getElementById('dialog').className='capture-dialog';
  }
  async function persistDraft(){if(state().project!==project||state().token!==token)throw Error('项目或身份已切换，草稿未提交。');saved=await api(saved?'/collection-drafts/'+saved.id:'/collection-drafts',{method:saved?'PATCH':'POST',revision:saved?.revision,body:saved?collectionPatchPayload(draft,saved):collectionPayload(draft)});if(state().project!==project||state().token!==token)throw Error('项目或身份已切换，请在原项目检查草稿。');return saved;}
 async function save(){await persistDraft();view().selected=saved.id;await refresh();document.getElementById('dialog').close();}
 async function send(){if(!canSend())throw Error('内部推送服务未配置或当前角色不能发起。');if(preparing)throw Error('提交参数已锁定，请等待草稿保存。');if(!submission){preparing=true;confirmedPayload=JSON.stringify(draft);show();try{await persistDraft();submission=Object.freeze({draftId:saved.id,draftRevision:saved.revision});}finally{preparing=false;show();}}if(JSON.stringify(draft)!==confirmedPayload)throw Error('提交参数已锁定，请关闭后检查服务端请求。');const result=await mutations.submit('/collections',submission);view().selected=result.id;document.getElementById('dialog').close();await refresh();}
  show();
 }
 return {page,tool,wizard,load};
}
