import {createDraftAutosaver,pick as pickDraft} from './drafts.mjs';
import {preflightFiles,uploadRequest} from './upload.mjs';
import {createAPI,idempotencyKey,supportedFormats,filtersQuery,evidenceDelta,evidenceChanges,readable,active,list} from './api.mjs';
const $=id=>document.getElementById(id);
const route=new URLSearchParams(location.hash.slice(1));
const state={token:sessionStorage.getItem('logan-workbench-token')||'',project:route.get('project')||'',session:null,workspace:{},page:route.get('page')||'reader',tool:route.get('tool')||'ai',issueId:route.get('issue')||'',runId:route.get('run')||'',eventId:'',events:[],total:0,nextCursor:'',histogram:null,filters:{},selected:new Set(),eventDetails:null,source:null,draft:null,handoffKeys:new Map(),handoffEdits:new Map(),pendingImportId:route.get('batch')||'',eventsLoading:false,busy:false};
if(!['reader','issues','collection'].includes(state.page))state.page='reader';
if(!['ai','source','evidence','verify','handoff'].includes(state.tool))state.tool='ai';
const api=createAPI({token:()=>state.token,project:()=>state.project});
const names={investigating:'调查中',awaiting_fix:'待修复',reopened:'重新调查',awaiting_verification:'待验证',resolved:'已解决',queued:'排队中',running:'处理中',succeeded:'完成',partial:'部分完成',failed:'失败',canceled:'已取消',completed:'完成',interrupted:'已中断',supported:'支持',refuted:'反驳',uncertain:'无法确定',pass:'通过',fail:'未通过',fact:'日志事实',hypothesis:'候选假设',inference:'推断',matched:'已匹配',candidate:'候选，尚未核验',missing:'缺少映射',unavailable:'当前不可用',available:'可用',pending:'待验证',created:'新建问题',edited:'编辑问题',handoff:'保存调查更新'};
const text=v=>v===null||v===undefined||v===''?'未知':String(v);
const label=v=>names[v]||text(v);
const all=key=>state.workspace[key]||[];
const issue=()=>all('issues').find(x=>x.id===state.issueId);
const run=()=>all('parseRuns').find(x=>x.id===state.runId);
const asset=()=>all('assets').find(x=>x.id===run()?.assetId);
const expired=object=>Boolean(object?.expiresAt&&Date.parse(object.expiresAt)<=Date.now());
const currentEvidence=()=>all('evidence').filter(x=>issue()?.evidenceIds?.includes(x.id));
function el(tag,attrs={},...children){const n=document.createElement(tag);for(const [k,v] of Object.entries(attrs)){if(k==='class')n.className=v;else if(k.startsWith('on'))n.addEventListener(k.slice(2).toLowerCase(),v);else if(k==='checked'||k==='disabled'||k==='selected')n[k]=Boolean(v);else if(v!==undefined)n.setAttribute(k,v);}for(const child of children.flat(Infinity)){if(child!==null&&child!==undefined)n.append(child instanceof Node?child:document.createTextNode(String(child)));}return n;}
const button=(title,fn,kind='',disabled=false)=>el('button',{type:'button',class:kind,disabled,onClick:()=>task(fn)},title);
const pill=value=>el('span',{class:'pill '+value},label(value));
const note=value=>el('p',{class:'muted'},value);
const empty=value=>el('div',{class:'empty'},value);
const heading=(title,...actions)=>el('div',{class:'section-head'},el('h2',{},title),el('div',{class:'actions'},actions));
const pre=value=>el('pre',{},typeof value==='string'?value:JSON.stringify(value,null,2));
function notify(message,error=false){$('notice').className=error?'error':'';$('notice').textContent=message;}
function clearContent(){state.workspace={};state.events=[];state.eventDetails=null;state.source=null;state.draft=null;state.selected.clear();state.handoffEdits.clear();state.handoffKeys.clear();for(const saver of draftAutosavers.values())saver.dispose();draftAutosavers.clear();if($('dialog').open)$('dialog').close();}
async function task(fn){try{await fn();}catch(e){
 if(e.status===401){clearContent();state.session=null;}
 else if(e.status===403||e.status===410){state.source=null;state.eventDetails=null;if($('dialog').open)$('dialog').close();try{await refresh();}catch{clearContent();state.session=null;}}
 render();notify(`${e.code?e.code+' · ':''}${e.message}${e.traceId?' · 追踪 '+e.traceId:''}`,true);
}}
function field(name,title,value='',type='text',options){const input=options?el('select',{name},options.map(o=>el('option',{value:typeof o==='string'?o:o.value,selected:(typeof o==='string'?o:o.value)===String(value)},typeof o==='string'?label(o):o.label))):el(type==='textarea'?'textarea':'input',{name,type:type==='textarea'?undefined:type,value:type==='textarea'?undefined:value},type==='textarea'?value:null);return el('label',{},el('span',{},title),input);}
function values(form){return Object.fromEntries(new FormData(form));}
function form(fields,title,submit){const f=el('form',{'data-form':title},fields,el('button',{type:'submit',class:'primary'},title));if(title==='生成 / 更新预览')f.addEventListener('input',()=>rememberHandoff(values(f)));f.addEventListener('submit',e=>{e.preventDefault();task(async()=>{const b=f.querySelector('button[type=submit]');b.disabled=true;try{await submit(values(f),f);}finally{b.disabled=false;}});});return f;}
function modal(title,content){$('dialog-content').replaceChildren(heading(title,button('关闭',()=>{$('dialog').close();})),content);$('dialog').showModal();}
let lastWorkspaceRefresh=0;
async function refresh(){
 const project=state.project,token=state.token;
 const [workspace,session]=await Promise.all([api('/workspace'),api('/session')]);
 if(project!==state.project||token!==state.token)return;
 const previousEvidence=new Map(all('evidence').map(e=>[e.id,e.availability]));const priorSourcePermission=state.session?.permissions?.includes('source');state.workspace=workspace;state.session=session;lastWorkspaceRefresh=Date.now();
 if(state.issueId&&!issue())state.issueId='';if(state.runId&&!run())state.runId='';
 if(state.draft){const fresh=all('handoffDrafts').find(d=>d.id===state.draft.id);state.draft=fresh||null;}
 const newlyRestricted=all('evidence').some(e=>(e.availability!=='available'||expired(e))&&previousEvidence.get(e.id)==='available');
 if((newlyRestricted||priorSourcePermission&&!session.permissions?.includes('source'))&&$('dialog').open)$('dialog').close();
 for(const draft of all('handoffDrafts').filter(d=>['restricted','expired','unavailable'].includes(d.availability))){const key=project+'/'+draft.issueId;state.handoffEdits.delete(draft.issueId);draftAutosavers.get(key)?.dispose();draftAutosavers.delete(key);}
 if(readable(run())&&!expired(run())){await loadEvents();}else{state.events=[];state.histogram=null;state.eventDetails=null;}
 let freshSource=state.source;if(state.source?.id){try{freshSource=await api(`/source-fragments/${state.source.id}`);}catch{freshSource=null;}}
 let freshDetails=state.eventDetails;
 if(state.eventDetails){
  try{const [context,related]=await Promise.all([api(`/events/${state.eventId}/context`),api(`/events/${state.eventId}/related`)]);const event=list(context,'events').find(e=>e.id===state.eventId);freshDetails=event?{event,context,related}:null;}catch{freshDetails=null;}
 }
 if(project!==state.project||token!==state.token)return;
 state.source=freshSource;state.eventDetails=freshDetails;render();
}
async function loadEvents(append=false){const project=state.project;const selectedRun=state.runId;if(!readable(run())){state.events=[];state.histogram=null;return;}const query=filtersQuery(state.filters);const suffix=query?query+'&':'';const [result,histogram]=await Promise.all([api(`/parse-runs/${state.runId}/events?${suffix}limit=200${append&&state.nextCursor?'&cursor='+encodeURIComponent(state.nextCursor):''}`),api(`/parse-runs/${state.runId}/histogram?${query}`)]);if(project!==state.project||selectedRun!==state.runId)return;if(result.filterHash!==histogram.filterHash)throw new Error('日志与时间分布范围不一致，请重新筛选');state.events=append?[...state.events,...result.events]:result.events;state.total=result.total;state.nextCursor=result.nextCursor;state.histogram=histogram;}
async function selectRun(id){state.runId=id;state.eventId='';state.selected.clear();state.eventDetails=null;state.events=[];state.histogram=null;state.eventsLoading=true;render();try{await loadEvents();}finally{state.eventsLoading=false;render();}}
let renderScope='';
function inputKey(n){return `${n.form?.dataset.form||''}:${n.name}:${['checkbox','radio'].includes(n.type)?n.value:''}`;}
function preserveRender(fn){
 const scope=[state.project,state.issueId,state.page,state.tool].join('/');
 const sameScope=renderScope===scope;renderScope=scope;const root=$('app');
 const scrolls=[...root.querySelectorAll('[data-scroll]')].map(n=>[n.dataset.scroll,n.scrollTop]);
 const inputs=sameScope?[...root.querySelectorAll('input[name],textarea[name],select[name]')].map(n=>[inputKey(n),n.value,n.checked]):[];
 const focused=document.activeElement?.name?inputKey(document.activeElement):null;
 const selection=focused?[document.activeElement.selectionStart,document.activeElement.selectionEnd]:null;
 fn();
 const nodes=[...root.querySelectorAll('[name]')];
 for(const [key,value,checked]of inputs){const n=nodes.find(n=>inputKey(n)===key);if(n&&n.name!=='project'&&n.type!=='file'){n.value=value;if(n.type==='checkbox')n.checked=checked;}}
 for(const [id,top]of scrolls){const n=[...root.querySelectorAll('[data-scroll]')].find(n=>n.dataset.scroll===id);if(n)n.scrollTop=top;}
 if(focused){const n=nodes.find(n=>inputKey(n)===focused);n?.focus({preventScroll:true});if(n?.setSelectionRange&&selection[0]!==null)n.setSelectionRange(...selection);}
}
function render(){if(state.session){history.replaceState(null,'','#'+new URLSearchParams({project:state.project,page:state.page,tool:state.tool,issue:state.issueId,run:state.runId,batch:state.pendingImportId||''}));}if(!state.session){renderLogin();return;}preserveRender(()=>{$('app').replaceChildren(shell());});}
function renderLogin(){const login=form([field('token','访问令牌',state.token,'password'),field('project','项目 ID',state.project)],'连接工作台',async v=>{state.token=v.token.trim();state.project=v.project.trim();if(!state.token||!state.project)throw new Error('请输入访问令牌与项目 ID');const session=await api('/session');state.session=session;state.project=session.projectId||state.project||session.projects?.[0]?.id||'';sessionStorage.setItem('logan-workbench-token',state.token);await refresh();if(readable(run())){await loadEvents();render();}});$('app').replaceChildren(el('main',{class:'login'},el('div',{class:'brand'},'L / LOGAN'),el('h1',{},'从现场日志，到可核对的结论。'),note('使用管理员提供的令牌连接授权项目。令牌仅保存在本次浏览器会话；调查材料保存在服务器。'),login));}
function shell(){return el('div',{class:'shell'},el('header',{},el('div',{class:'brand'},'L / LOGAN',el('small',{},'调查工作台')),el('nav',{},[['reader','日志阅读'],['issues','问题调查'],['collection','日志回捞']].map(([id,title])=>button(title,()=>{state.page=id;render();},state.page===id?'active':''))),el('div',{class:'session'},el('select',{name:'project',onChange:async e=>task(async()=>{const nextProject=e.target.value;clearContent();state.project=nextProject;state.pendingImportId='';state.session=await api('/session');state.issueId='';state.runId='';state.selected.clear();state.events=[];state.draft=null;state.source=null;state.eventDetails=null;await refresh();})},(state.session.projects||[]).map(p=>el('option',{value:p.id,selected:p.id===state.project},p.name+(p.demo?' · 演示':'')))),el('span',{},text(state.session.role)||'角色待核对'),button('刷新',refresh),button('退出',()=>{sessionStorage.removeItem('logan-workbench-token');state.token='';state.session=null;clearContent();render();}))),el('div',{class:'scope'},el('strong',{},issue()?.title||'独立阅读'),el('span',{},issue()?['app','platform','environment','device','version','build'].map(k=>`${k}: ${text(issue().context?.[k])}`).join(' / '):'选择日志阅读，或关联问题开始调查'),issue()?el('span',{},`${text(issue().context?.begin)} → ${text(issue().context?.end)} · 证据 ${currentEvidence().length} · 范围版本 ${issue().contextRevision}`):null),state.page==='collection'?collection():state.page==='issues'&&!issue()?issuesPage():workbench());}
function issuesPage(){const f=form([field('issueQuery','搜索标题 / 描述 / 负责人')],'搜索',v=>{const needle=v.issueQuery.toLowerCase();const cards=all('issues').filter(i=>`${i.title} ${i.description} ${i.owner}`.toLowerCase().includes(needle));$('issue-list').replaceChildren(...cards.map(issueCard));});return el('main',{class:'page'},heading('问题调查',button('新建问题',()=>editIssue(), 'primary')),f,el('div',{id:'issue-list',class:'cards'},all('issues').length?all('issues').map(issueCard):empty('还没有问题。新建问题，保存现场范围与调查记录。')));}
function issueCard(i){return el('article',{class:'card'},el('div',{class:'section-head'},el('h3',{},i.title),pill(i.status)),note(`${text(i.owner)} · ${text(i.priority)} · 证据 ${i.evidenceIds?.length||0}`),el('p',{},i.description||'未填写现象'),button('进入调查',()=>{state.issueId=i.id;state.page='issues';state.draft=null;render();}));}
function editIssue(existing){const c=existing?.context||{};modal(existing?'编辑问题':'新建问题',form([field('title','标题',existing?.title||''),field('description','现象与影响',existing?.description||'','textarea'),field('owner','负责人',existing?.owner||''),field('priority','优先级',existing?.priority||'P2','text',['P0','P1','P2','P3']),...['app','platform','environment','device','version','build','begin','end'].map(k=>field(k,k,c[k]||''))],existing?'保存修改':'创建问题',async v=>{if(!v.title.trim())throw new Error('请输入问题标题');const context={};for(const k of ['app','platform','environment','device','version','build','begin','end']){context[k]=v[k];delete v[k];}const result=await api(existing?`/issues/${existing.id}`:'/issues',{method:existing?'PATCH':'POST',body:{...v,context},revision:existing?.revision});state.issueId=result.id;state.page='issues';$('dialog').close();await refresh();}));}
function workbench(){return el('main',{class:'workbench'},el('aside',{class:'library','data-scroll':'library'},heading('日志资产',button('导入',upload,'primary')),state.pendingImportId?button('核对上次上传批次',recoverPendingImport):null,form([field('assetQuery','筛选文件 / 设备 / 构建',state.assetQuery||'')],'筛选资产',v=>{state.assetQuery=v.assetQuery;render();}),button('新建问题',()=>editIssue()),issue()?el('div',{class:'issue-summary'},pill(issue().status),button('编辑问题',()=>editIssue(issue())),button('问题列表',()=>{state.issueId='';state.page='issues';render();})):null,all('assets').length?all('assets').filter(a=>!state.assetQuery||[a.name,a.device,a.build,a.version,a.platform,a.origin,...all('parseRuns').filter(r=>r.assetId===a.id).map(r=>label(r.status))].join(' ').toLowerCase().includes(state.assetQuery.toLowerCase())).map(assetCard):empty('导入 JSONL、JSON 数组或 UTF-8 明文日志。'),heading('历史日志（只读）'),capabilities().legacy?.available?button('读取已授权旧任务',legacyTasks):note('旧日志项目映射 / 只读连接尚未配置。新工作台不转换或覆盖历史 ID。'),heading('站内提醒'),all('notifications').length?all('notifications').map(n=>el('article',{class:'notification'},el('p',{},n.message||n.title||n.id),button('查看结果',async()=>{const turn=all('turns').find(t=>t.id===n.targetId);const targetRun=all('parseRuns').find(r=>r.id===n.targetId);const targetIssue=all('issues').find(i=>i.id===n.targetId);if(turn){state.issueId=turn.issueId;state.tool='ai';state.page='issues';render();}else if(targetRun){state.page='reader';await selectRun(targetRun.id);}else if(targetIssue){state.issueId=targetIssue.id;state.page='issues';render();}else throw new Error('原结果当前不可用，请刷新或检查访问权限');}),button(n.read?'已读':'标记已读',async()=>{await api(`/notifications/${n.id}/read`,{method:'POST',body:{}});if(n.issueId)state.issueId=n.issueId;await refresh();}))):note('暂无提醒')),reader(),el('aside',{class:'investigation','data-scroll':'tools'},el('div',{class:'tool-tabs'},[['ai','AI 排查'],['source','源码'],['evidence','证据'],['verify','验证'],['handoff','回写']].map(([id,title])=>button(title,()=>{state.tool=id;render();},state.tool===id?'active':''))),!issue()?el('div',{class:'tool-body'},heading('关联调查'),note('日志可独立阅读。选择问题后，证据、提问和验证将保存在该问题中。'),el('div',{class:'stack'},all('issues').map(i=>button(i.title,()=>{state.issueId=i.id;render();}))),button('新建问题',()=>editIssue(),'primary')):el('div',{class:'tool-body'},({ai:aiPanel,source:sourcePanel,evidence:evidencePanel,verify:verificationPanel,handoff:handoffPanel}[state.tool])())));}
function assetCard(a){const runs=all('parseRuns').filter(r=>r.assetId===a.id);return el('article',{class:'asset '+(asset()?.id===a.id?'chosen':'')},el('strong',{},a.name),note(`${text(a.platform)} · 设备 ${text(a.device)} · 构建 ${text(a.build)} · ${a.size} B`),note(`${text(a.origin)} · ${a.id}`),expired(a)?note('原文件已到期：下载、重新解析与新证据不可用。'):null,el('div',{class:'actions'},button('解析 / 历史',()=>parseAsset(a)),expired(a)?null:button('原文件',()=>download(`/assets/${a.id}/raw`,a.name)),issue()?button(a.issueId===issue().id?'已关联':'关联问题',async()=>{await api(`/issues/${issue().id}/log-links`,{method:'POST',body:{assetId:a.id}});await refresh();}):null),runs.map(r=>button(`${label(r.status)} · ${r.format} · ${r.mode==='strict'?'严格':'跳过坏记录'} · ${r.eventCount||0} 条`,()=>selectRun(r.id),state.runId===r.id?'run active':'run')));}
function upload(){
 const clientBatchId=state.pendingImportId||idempotencyKey();state.pendingImportId=clientBatchId;const locationParams=new URLSearchParams(location.hash.slice(1));locationParams.set('batch',clientBatchId);history.replaceState(null,'','#'+locationParams);
 const token=state.token,project=state.project,issueId=state.issueId;
 const request=createAPI({token:()=>token,project:()=>project});
 const input=field('files','日志文件（可多选）','','file');input.querySelector('input').multiple=true;
 const progress=el('progress',{max:1,value:0,'aria-label':'实际上传字节进度'});
 const status=note('先检查文件字节、大小和协议，再传输。上传完成后仍须单独解析。');
 const recovery=el('div');let controller=null;
 const cancel=button('取消上传传输',()=>{controller?.abort();});cancel.disabled=true;
 const fields=[button('新建独立上传批次',()=>{state.pendingImportId='';upload();}),note('未知身份请留空。协议文件只在对应解析连接已配置时可选。'),note('服务端导入上限：'+JSON.stringify(capabilities().limits||{})),capabilities().protocols?.web?.configured?note(capabilities().protocols.web.encryptedConfigured?'Web v0 / 加密 v1 解码已配置。':'Web 仅配置 v0 明文封包；加密 v1 尚不可用。'):null,input,field('declaredFormat','原文件协议声明','auto','text',parseFormats()),field('platform','平台','','text',[{value:'',label:'未知'},'android','ios','web']),...['app','environment','device','version','build','begin','end'].map(k=>field(k,k)),field('origin','来源说明','manual_upload'),note('批次号 '+clientBatchId),status,progress,cancel,recovery];
 const f=form(fields,'检查并上传原文件',async(v,formNode)=>{
  const files=[...formNode.querySelector('[name=files]').files];
  const report=await preflightFiles(files,capabilities().limits,v.declaredFormat);
  status.textContent=`预检 ${report.files.length} 个文件 / ${report.total} B。`+report.files.map(x=>x.warning?`${x.name}: ${x.warning}`:'').filter(Boolean).join('；');
  const existing=list(await request('/imports?clientBatchId='+encodeURIComponent(clientBatchId)),'imports');
  if(existing.length){await acceptImport(existing.at(-1));return;}
  delete v.files;const data=new FormData();data.append('metadata',JSON.stringify({...v,issueId,clientBatchId}));for(const file of files)data.append('files',file);
  controller=new AbortController();cancel.disabled=false;progress.removeAttribute('value');recovery.replaceChildren();
  try{
   const result=await uploadRequest({body:data,token,project,key:idempotencyKey(),signal:controller.signal,onProgress:bytes=>{
    if(bytes.total){progress.max=bytes.total;progress.value=bytes.loaded;status.textContent=`正在上传传输 ${bytes.loaded} / ${bytes.total} B（含表单信息）；${bytes.loaded===bytes.total?'等待服务端完整性校验与保存。':''}`;}
    else{progress.removeAttribute('value');status.textContent=`已传输 ${bytes.loaded} B，总量暂不可计算。`;}
   }});
   await acceptImport(result);
  }catch(error){
   status.textContent=error.message+'。正在核对服务端批次…';
   try{const batches=list(await request('/imports?clientBatchId='+encodeURIComponent(clientBatchId)),'imports');if(batches.length){await acceptImport(batches.at(-1));return;}status.textContent=error.message+'。尚未查到已保存批次，不能据此认定服务端已取消。';}catch(lookupError){status.textContent=error.message+'；核对失败：'+lookupError.message;}
   recovery.replaceChildren(button('再次核对服务端批次',async()=>{const batches=list(await request('/imports?clientBatchId='+encodeURIComponent(clientBatchId)),'imports');if(batches.length)await acceptImport(batches.at(-1));else notify('尚未查到该批次。可继续核对，或沿用同一批次号重试上传；服务端会阻止重复保存。');}));
   throw error;
  }finally{cancel.disabled=true;controller=null;}
 });
 async function acceptImport(result){
  if(state.project!==project){status.textContent='原项目已保存该批次；请切回原项目查看。';return;}
  state.pendingImportId='';$('dialog').close();await refresh();notify(`服务端已保存 ${result.assets.length} 个原文件，请选择解析方式。`);if(result.assets[0])parseAsset(result.assets[0]);
 }
 modal('导入日志',f);
}
async function recoverPendingImport(){
 const batches=list(await api('/imports?clientBatchId='+encodeURIComponent(state.pendingImportId)),'imports');
 if(!batches.length){modal('上传批次待核对',el('div',{},note('尚未查到已保存结果。断开传输不保证服务端未提交，请保留该批次号继续核对或重试。'),pre(state.pendingImportId),button('沿用批次号重新上传',upload)));return;}
 state.pendingImportId='';await refresh();modal('服务端上传结果',el('div',{},pre(batches.at(-1)),button('继续导入其他文件',upload)));
}
function parseAsset(a){const history=all('parseRuns').filter(r=>r.assetId===a.id);modal('解析 · '+a.name,el('div',{},expired(a)?note('原文件已到期，请重新上传后创建新的资产与解析批次。'):form([field('format','格式',a.declaredFormat||'auto','text',parseFormats()),field('mode','记录错误处理','strict','text',[{value:'strict',label:'严格：遇到坏记录整批失败'},{value:'skip_invalid_records',label:'跳过坏记录：保留有效记录'}])],'新建解析批次',async v=>{const r=await api(`/assets/${a.id}/parse-runs`,{method:'POST',body:v});state.runId=r.id;$('dialog').close();await refresh();await loadEvents();render();}),heading('历史批次'),parseComparisonForm(a,history),history.map(r=>el('article',{class:'card'},pill(r.status),note(`${r.id} · ${r.createdAt} · 有效 ${r.eventCount||0} / 错误 ${r.errorCount||0}`),pre(r.errors||[]),active(r)?button('取消批次',async()=>{await api(`/parse-runs/${r.id}/cancel`,{method:'POST',body:{}});await refresh();parseAsset(a);}):button('查看批次',async()=>{$('dialog').close();await selectRun(r.id);})))));}
function parseComparisonForm(a,history){
 if(history.length<2)return note('同一原文件至少两个已结束批次后可比较。');
 const options=history.filter(r=>!active(r)).map(r=>({value:r.id,label:`${r.id} · ${label(r.status)} · ${r.format} / ${r.mode}`}));
 return form([field('leftRun','左侧解析批次',options[0]?.value||'','text',options),field('rightRun','右侧解析批次',options.at(-1)?.value||'','text',options)],'比较解析版本',async v=>{
  if(!v.leftRun||!v.rightRun||v.leftRun===v.rightRun)throw new Error('请选择两个不同的已结束批次');
  const result=await api(`/parse-runs/${encodeURIComponent(v.leftRun)}/comparison?otherRunId=${encodeURIComponent(v.rightRun)}`);
  modal('同一原文件 · 解析版本比较',el('div',{},note(`${a.name} · ${a.id}`),el('p',{},`新增 ${result.addedCount} / 移出 ${result.removedCount} / 变化 ${result.changedCount} / 相同 ${result.unchangedCount}`),note(result.truncated?'差异列表已截断，汇总数量保留完整范围。':'差异列表未截断。'),heading('解析状态与错误'),pre({left:result.left,right:result.right}),heading('事件身份差异'),pre(result.differences),button('阅读左侧批次',async()=>{$('dialog').close();await selectRun(v.leftRun);}),button('阅读右侧批次',async()=>{$('dialog').close();await selectRun(v.rightRun);})));
 });
}

function reader(){return el('section',{class:'reader','data-scroll':'reader'},heading(asset()?.name||'日志阅读',run()?pill(run().status):null,run()?button('筛选结果下载',()=>exportData('events')):null),run()?note(`批次 ${run().id} · 有效 ${run().eventCount||0} · 错误 ${run().errorCount||0} · 构建 ${text(asset()?.build)}`):note('从左侧选择解析批次。原文件、解析批次和证据分别保留身份。'),run()&&!readable(run())?el('div',{},empty(active(run())?'解析处理中，完成后发布可读事件。':'当前批次没有可读事件，请查看错误并新建解析批次。'),active(run())?button('取消解析',async()=>{await api(`/parse-runs/${run().id}/cancel`,{method:'POST',body:{}});await refresh();}):null,pre(run().errors||[])):run()?el('div',{class:'reader-content'},filterForm(),histogram(),el('div',{class:'section-head'},note(`匹配 ${state.total} 条 · 已显示 ${state.events.length} 条 · 已选 ${state.selected.size} 条`),issue()?button('选中事件加入证据',addLogEvidence):null),el('div',{class:'events','data-scroll':'events'},state.eventsLoading?empty('正在读取匹配事件与时间分布…'):state.events.length?state.events.map(eventRow):empty('没有匹配事件。调整关键词、级别、模块或时间范围。'),state.nextCursor?button('加载更多',async()=>{await loadEvents(true);render();}):null),eventDetails()):empty('先导入一份日志，从现场事件开始调查。'));}
function filterForm(){return form([el('div',{class:'filter-grid'},field('query','关键词',state.filters.query||''),field('level','级别',state.filters.level||'','text',[{value:'',label:'全部级别'},'TRACE','DEBUG','INFO','WARN','ERROR','FATAL']),field('module','模块',state.filters.module||''),field('type','日志类型 / logType',state.filters.type||''),field('unknownTime','时间身份',state.filters.unknownTime||'','text',[{value:'',label:'全部事件'},{value:'only',label:'仅时间未知'},{value:'exclude',label:'排除时间未知'}]),field('start','开始时间 ISO',state.filters.start||''),field('end','结束时间 ISO',state.filters.end||''))],'应用筛选',async v=>{state.filters=v;await loadEvents();render();});}
function histogram(){const h=state.histogram;if(!h)return null;const buckets=h.buckets||[];const max=Math.max(1,...buckets.map(b=>b.count));return el('div',{class:'histogram',role:'img','aria-label':`筛选后时间分布，共 ${h.total} 条，时间未知 ${h.unknownTime||0} 条`},el('div',{class:'bars'},buckets.map(b=>el('div',{class:'bucket',title:`${b.time}: ${b.count}`},el('meter',{min:0,max,value:b.count},b.count),el('small',{},b.time?b.time.slice(11,16):'未知')))),note(`时间分布与当前筛选一致 · 时间未知 ${h.unknownTime||0} 条`));}
function eventRow(e){return el('article',{class:'event '+(state.eventId===e.id?'chosen':'')},el('label',{class:'event-check'},el('input',{type:'checkbox',checked:state.selected.has(e.id),'aria-label':'选择事件 '+e.id,onChange:event=>{event.target.checked?state.selected.add(e.id):state.selected.delete(e.id);render();}})),el('button',{type:'button',class:'event-body',onClick:()=>task(async()=>{state.eventId=e.id;state.source=null;const [context,related]=await Promise.all([api(`/events/${e.id}/context`),api(`/events/${e.id}/related`)]);state.eventDetails={event:e,context,related};render();})},el('span',{class:'event-meta'},el('time',{},text(e.time)),pill(e.level),el('span',{},text(e.module)),el('span',{},'类型 '+text(e.type??e.logType)),el('small',{},`行 ${e.line} · ${e.id}`)),el('span',{class:'event-text'},e.content)));}
function eventDetails(){const d=state.eventDetails;if(!d)return null;return el('div',{class:'event-detail'},heading('选中事件',button('关闭详情',()=>{state.eventDetails=null;render();})),pre(d.event),heading('原始相邻上下文'),...list(d.context,'events').map(e=>el('p',{class:'mono'},`${text(e.time)} ${e.content}`)),heading('关联请求候选'),note('候选关联不等于同一因果链；缺失身份保持未知。'),pre(d.related),button('查找对应源码',async()=>{state.tool='source';render();}));}
async function addLogEvidence(){if(!issue())throw new Error('请先选择问题');if(!state.selected.size)throw new Error('请勾选事件');if(state.selected.size>100)throw new Error('每条日志证据最多选择 100 条事件，请缩小范围');await api(`/issues/${issue().id}/evidence`,{method:'POST',body:{kind:'log',label:`${asset()?.name} · ${state.selected.size} 条`,originRef:{runId:state.runId,eventIds:[...state.selected]}}});await refresh();notify('事件已加入调查证据。');}
async function download(path,name){const blob=await api(path,{download:true});const url=URL.createObjectURL(blob);const a=el('a',{href:url,download:name});document.body.append(a);a.click();a.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);}
async function exportData(kind){const result=await api('/exports',{method:'POST',body:{kind,runId:state.runId||undefined,issueId:state.issueId||undefined,...state.filters}});await download(`/exports/${result.id}/content`,kind==='events'?'filtered-events.json':'investigation.json');}
function parseFormats(){return supportedFormats(capabilities()).map(value=>value==='native'?{value,label:'Native SDK 协议'}:value==='web'?{value,label:'Web SDK 协议'}:value);}
function capabilities(){return state.workspace.capabilities||{};}
function aiPanel(){const turns=all('turns').filter(t=>t.issueId===issue().id);const latest=turns.at(-1);const delta=evidenceChanges(latest?.snapshot?.evidence||[],currentEvidence(),all('verifications').filter(v=>v.issueId===issue().id));return el('div',{},heading('围绕证据提问'),capabilities().model?.configured===false?el('p',{class:'connector-note'},'模型未配置：可以整理证据、记录验证并人工回写。'):null,note('发送时固定本轮证据快照。阅读筛选不会更改历史轮次；模型未配置时不会生成替代回答。'),el('details',{},el('summary',{},`本轮范围 · ${currentEvidence().length} 条证据 · 新增 ${delta.added.length} / 移出 ${delta.removed.length} / 变化 ${delta.changed.length} / 失效 ${delta.expired.length} / 不可用 ${delta.unavailable.length} / 新反证 ${delta.contradicted.length}`),currentEvidence().map(e=>el('p',{},`${e.id} · ${e.label} · ${e.verified?'已核验':'未核验'} · ${text(e.availability)}`)),pre(delta)),form([field('question','问题 / 追问','','textarea')],'发送本轮问题',async v=>{if(!v.question.trim())throw new Error('请输入排查问题');await api(`/issues/${issue().id}/turns`,{method:'POST',body:{question:v.question,parentTurnId:latest?.status==='completed'?latest.id:undefined,contextRevision:issue().contextRevision}});await refresh();}),turns.length?turns.slice().reverse().map(turnCard):empty('还没有调查轮次。先选择日志证据，再描述要核对的问题。'),el('details',{},el('summary',{},'连接能力'),pre(capabilities())));}
function turnCard(t){const answer=t.answer;return el('article',{class:'turn card'},heading(t.question,pill(t.status)),note(`${t.id} · ${t.createdAt}`),t.availability&&t.availability!=='available'?note('本轮依赖材料当前不可用，相关正文已隐藏。'):null,t.error?el('p',{class:'error-text'},typeof t.error==='string'?t.error:JSON.stringify(t.error)):null,answer?el('div',{},el('p',{},answer.summary),...(answer.claims||[]).map(c=>el('section',{class:'claim'},el('strong',{},label(c.type)),el('p',{},c.text),c.hypothesisId?note(`${c.hypothesisId} · v${text(c.hypothesisVersion)}`):null,el('div',{class:'citations'},(c.citations||[]).map(citation=>{const id=typeof citation==='string'?citation:citation.evidenceId||citation.id;return button('依据 '+text(id),async()=>{const current=await api(`/turns/${t.id}`);const e=current.snapshot?.evidence?.find(e=>e.id===id);modal('本轮引用 · '+text(id),e&&e.availability==='available'&&!expired(e)?pre(e):empty('引用内容当前不可用；保留历史证据身份。'));});})))),heading('证据缺口'),...(answer.unknowns||[]).map(x=>el('p',{},typeof x==='string'?x:JSON.stringify(x))),heading('下一步'),...(answer.nextSteps||[]).map(x=>el('p',{},typeof x==='string'?x:JSON.stringify(x)))):null,button('读取本轮当前可用快照',async()=>{const fresh=await api(`/turns/${t.id}`);modal('本轮固定快照',el('div',{},pre(fresh.snapshot),pre(fresh.retrievalManifest||[])));}),el('div',{class:'actions'},active(t)?button('停止生成',async()=>{await api(`/turns/${t.id}/cancel`,{method:'POST',body:{}});await refresh();}):button('按原快照重试',async()=>{await api(`/turns/${t.id}/retries`,{method:'POST',body:{}});await refresh();}),button('继续追问',()=>{state.tool='ai';render();document.querySelector('[name=question]')?.focus();})) );}
function evidencePanel(){const ids=issue().evidenceIds||[];const historical=all('evidence').filter(e=>e.issueId===issue().id);return el('div',{},heading('调查证据',button('导出调查',()=>exportData('investigation'))),note('日志、源码与人工材料保留原始出处。移出当前范围不会重写旧轮次快照。'),button('添加人工材料',manualEvidence,'primary'),historical.length?historical.map(e=>el('article',{class:'card'},el('strong',{},e.label||e.kind),note(`${e.id} · ${e.verified?'已核验':'人工 / 未核验'} · ${text(e.availability)}`),pre(e.originRef),button('读取当前可用材料',async()=>{await refresh();const fresh=all('evidence').find(x=>x.id===e.id);modal('材料正文',fresh&&fresh.availability==='available'&&!expired(fresh)?pre(fresh.content):empty('材料已到期或当前无权读取；历史身份保留。'));}),button(ids.includes(e.id)?'移出本轮范围':'加入本轮范围',async()=>{await api(`/issues/${issue().id}/context`,{method:'PATCH',revision:issue().contextRevision,body:ids.includes(e.id)?{add:[],remove:[e.id]}:{add:[e.id],remove:[]}});await refresh();}))):empty('选择日志事件加入证据，或粘贴人工材料。'));}
function manualEvidence(){modal('添加人工材料',form([note('人工粘贴的日志或源码始终标为未核验，不据此确认构建匹配。'),field('kind','类型','manual','text',[{value:'manual',label:'人工说明 / 片段'}]),field('label','材料标题'),field('content','材料正文','','textarea')],'保存材料',async v=>{if(!v.content.trim())throw new Error('请输入材料正文');await api(`/issues/${issue().id}/evidence`,{method:'POST',body:v});$('dialog').close();await refresh();}));}
function sourcePanel(){const e=state.eventDetails?.event;const mappings=all('sourceMappings');return el('div',{},heading('现场源码'),capabilities().source?.configured===false?el('p',{class:'connector-note'},'源码连接未配置。可添加明确标注的未核验人工片段。'):null,note(e?`事件 ${e.id} · 原资产构建 ${text(asset()?.build)}`:'先在阅读区选中事件。不会从问题上下文借用构建身份。'),form([field('mappingId','配置的构建映射','','text',[{value:'',label:'由服务匹配'},...mappings.map(m=>({value:m.id,label:`${m.app} / ${m.platform} / ${m.build} / ${m.commit}`}))]),field('path','文件路径（如事件缺失）',e?.file||''),field('start','开始行',e?.sourceLine||'','number'),field('end','结束行','','number')],'查找源码',async v=>{if(!e)throw new Error('请先选中日志事件');state.source=null;state.source=await api('/source-resolutions',{method:'POST',body:{eventId:e.id,mappingId:v.mappingId||undefined,path:v.path||undefined,start:v.start?Number(v.start):undefined,end:v.end?Number(v.end):undefined}});render();}),state.source?el('article',{class:'card'},pill(state.source.status),note(`${text(state.source.commit)} · ${text(state.source.path)}:${text(state.source.start)}–${text(state.source.end)}`),note(state.source.id?'片段引用 '+state.source.id:''),pre(state.source.content||state.source),state.source.id?button('加入源码证据',async()=>{await api(`/issues/${issue().id}/evidence`,{method:'POST',body:{kind:'source',label:`${state.source.path}:${state.source.start}`,originRef:{fragmentId:state.source.id,mappingId:state.source.mappingId,path:state.source.path,start:state.source.start,end:state.source.end}}});await refresh();}):null):empty('源码需要可信构建映射及仓库权限。未配置时可记录人工片段。'),button('添加未核验片段',manualEvidence),heading('版本比较'),form([field('leftRef','左侧片段 ID / 引用'),field('rightRef','右侧片段 ID / 引用')],'读取差异',async v=>{const diff=await api('/source-diffs?'+new URLSearchParams(v));modal('源码差异',pre(diff));}),state.session.role==='admin'?el('details',{},el('summary',{},'管理员：登记不可变构建映射'),form(['app','platform','build','repoId','commit'].map(k=>field(k,k)),'登记映射',async v=>{await api('/source-mappings',{method:'POST',body:v});await refresh();})):null);}
function verificationPanel(){
 const history=all('verifications').filter(v=>v.issueId===issue().id);
 const hypotheses=all('hypotheses').filter(h=>h.issueId===issue().id);
 return el('div',{},heading('假设与验证'),
  note('假设具有独立 ID 和版本。反驳与支持记录进入下一轮证据；假设支持不等于修复通过。'),
  form([field('hypothesisText','人工候选假设','','textarea')],'记录候选假设',async v=>{if(!v.hypothesisText.trim())throw new Error('请输入候选假设');await api(`/issues/${issue().id}/hypotheses`,{method:'POST',body:{text:v.hypothesisText}});await refresh();}),
  hypotheses.map(h=>el('article',{class:'card'},el('strong',{},h.text),note(`${h.id} · v${h.version}`),pill(h.status||'uncertain'))),
  heading('记录验证'),
  form([
   field('purpose','验证目的','hypothesis','text',[{value:'hypothesis',label:'假设验证'},{value:'fix',label:'修复验证'}]),
   field('hypothesisId','验证哪条假设','','text',[{value:'',label:'选择当前假设（修复验证不需要）'},...hypotheses.map(h=>({value:h.id,label:`v${h.version} · ${h.text}`}))]),
   field('method','步骤 / 方法','','textarea'),field('expected','预期结果','','textarea'),field('observed','实际观察','','textarea'),field('build','验证构建'),field('scenario','验证场景'),
   field('result','结果','uncertain','text',[{value:'uncertain',label:'假设：无法确定'},{value:'supported',label:'假设：支持'},{value:'refuted',label:'假设：反驳'},{value:'pass',label:'修复：通过'},{value:'fail',label:'修复：未通过'}]),
   el('fieldset',{},el('legend',{},'引用本轮可用证据'),currentEvidence().filter(e=>e.availability==='available'&&!expired(e)).map(e=>el('label',{class:'checkbox'},el('input',{type:'checkbox',name:'verificationEvidence',value:e.id}),e.label||e.id)))
  ],'保存验证',async(v,f)=>{
   const evidenceIds=[...f.querySelectorAll('[name=verificationEvidence]:checked')].map(n=>n.value);delete v.verificationEvidence;
   if(v.purpose==='hypothesis'&&!['supported','refuted','uncertain'].includes(v.result)||v.purpose==='fix'&&!['pass','fail'].includes(v.result))throw new Error('结果类型与验证目的不一致');
   const hypothesis=hypotheses.find(h=>h.id===v.hypothesisId);
   if(v.purpose==='hypothesis'&&!hypothesis)throw new Error('请先选择已记录的假设版本');
   await api(`/issues/${issue().id}/verifications`,{method:'POST',body:{...v,hypothesisId:v.purpose==='hypothesis'?hypothesis.id:undefined,hypothesisVersion:v.purpose==='hypothesis'?hypothesis.version:undefined,evidenceIds}});await refresh();
  }),
  heading('验证历史'),history.slice().reverse().map(v=>el('article',{class:'card'},heading(v.purpose==='fix'?'修复验证':'假设验证',pill(v.result)),v.hypothesisId?note(`${v.hypothesisId} · v${text(v.hypothesisVersion)}`):null,note(`${text(v.build)} · ${text(v.scenario)} · 执行人 ${text(v.actor)} · ${v.createdAt}`),el('p',{},'步骤：'+text(v.method)),el('p',{},'预期：'+text(v.expected)),el('p',{},'实际：'+text(v.observed)),note('证据：'+(v.evidenceIds||[]).join(', '))))
 );
}
const draftAutosavers=new Map();
function draftAutosaver(issueRecord){
 const key=state.project+'/'+issueRecord.id;
 let saver=draftAutosavers.get(key);
 const current=all('handoffDrafts').filter(d=>d.editable&&d.issueId===issueRecord.id).at(-1);
 if(!saver){
  const token=state.token,project=state.project,id=issueRecord.id;
  const request=createAPI({token:()=>token,project:()=>project});
  saver=createDraftAutosaver({draft:current,create:body=>request(`/issues/${id}/handoff-drafts`,{method:'POST',body}),patch:(draft,body)=>request(`/handoff-drafts/${draft.id}`,{method:'PATCH',revision:draft.revision,body}),onState:saveState=>{
   if(state.project===project&&state.issueId===id){const node=$('draft-save-state');if(node)node.textContent=saveState.saving?'正在保存草稿…':saveState.error?'草稿尚未保存：'+saveState.error.message:saveState.dirty?'草稿有未保存编辑':saveState.draft?'草稿已保存至服务器 · v'+saveState.draft.revision:'尚未编辑草稿';}
  }});draftAutosavers.set(key,saver);
 }else saver.updateDraft(current);
 return saver;
}
function rememberHandoff(value){state.handoffEdits.set(state.issueId,value);if(issue())draftAutosaver(issue()).change(value);}
function handoffPanel(){
 const currentIssue=issue();const saver=draftAutosaver(currentIssue);
 const turns=all('turns').filter(t=>t.issueId===currentIssue.id&&t.status==='completed');
 const updates=all('updates').filter(u=>u.issueId===currentIssue.id);
 const draft=state.draft?.issueId===currentIssue.id&&!state.draft.editable?state.draft:all('handoffDrafts').filter(d=>!d.editable&&d.issueId===currentIssue.id&&!d.usedAt).at(-1);
 const edits=state.handoffEdits.get(currentIssue.id)||saver.state().values||draft||{owner:currentIssue.owner,status:currentIssue.status};
 const stale=draft&&(draft.issueRevision!==currentIssue.revision||draft.contextRevision!==currentIssue.contextRevision||draft.verificationRevision!==currentIssue.verificationRevision||JSON.stringify(pickDraft(draft))!==JSON.stringify(pickDraft(edits)));
 const unavailable=draft&&(['restricted','expired','unavailable'].includes(draft.availability)||expired(draft)||(draft.snapshot?.evidence||[]).some(e=>e.availability&&e.availability!=='available'||expired(e)));
 const blockedReason=unavailable?'预览引用的材料已过期或当前无权读取，不能确认回写。请补充可用证据后重新预览。':stale?'问题、证据、验证或编辑内容已变化，请重新预览后确认。':'';
 const saveState=saver.state();
 return el('div',{},heading('内部工单回写'),note('编辑草稿自动保存到服务器。生成预览后，仍需核对引用并明确确认回写。'),
  el('p',{class:'muted',id:'draft-save-state','aria-live':'polite'},saveState.saving?'正在保存草稿…':saveState.error?'草稿尚未保存：'+saveState.error.message:saveState.dirty?'草稿有未保存编辑':saveState.draft?'草稿已保存至服务器 · v'+saveState.draft.revision:'尚未编辑草稿'),
  form([
   field('mode','草稿来源',edits.mode||'manual','text',[{value:'manual',label:'人工整理'},{value:'ai',label:'已完成 AI 轮次'}]),
   field('turnId','完成轮次',edits.turnId||'','text',[{value:'',label:'不使用 AI'},...turns.map(t=>({value:t.id,label:t.question}))]),
   field('summary','调查摘要与下一步',edits.summary||'','textarea'),field('owner','负责人',edits.owner??currentIssue.owner??''),
   field('status','建议状态',edits.status||currentIssue.status,'text',['investigating','awaiting_fix','awaiting_verification','resolved','reopened'])
  ],'生成 / 更新预览',async v=>{
   if(!v.summary.trim())throw new Error('请输入调查摘要');if(v.mode==='ai'&&!v.turnId)throw new Error('请选择已完成 AI 轮次');
   rememberHandoff(v);await saver.flush();
   state.draft=await api(`/issues/${currentIssue.id}/handoff-previews`,{method:'POST',body:{...v,turnId:v.mode==='ai'?v.turnId:undefined,issueRevision:currentIssue.revision,contextRevision:currentIssue.contextRevision,verificationRevision:currentIssue.verificationRevision}});render();
  }),
  el('div',{class:'actions'},button('立即保存草稿',async()=>{const f=document.querySelector('[data-form="生成 / 更新预览"]');if(f)rememberHandoff(values(f));await saver.flush();notify('编辑草稿已保存至服务器。');}),button('从所选完成轮次整理摘要',()=>{
   const selected=document.querySelector('[name=turnId]')?.value;const turn=turns.find(t=>t.id===selected);if(!turn)throw new Error('请先选择已完成的 AI 轮次');
   const summary=[turn.answer?.summary,...(turn.answer?.claims||[]).map(c=>`${label(c.type)}：${c.text}`),...(turn.answer?.unknowns||[]).map(x=>'缺口：'+text(x)),...(turn.answer?.nextSteps||[]).map(x=>'下一步：'+text(x))].filter(Boolean).join('\n');
   const node=document.querySelector('[name=summary]');if(node){node.value=summary;document.querySelector('[name=mode]').value='ai';rememberHandoff(values(node.form));}
  })),
  draft?el('article',{class:'draft'},heading('待确认预览',unavailable?pill('材料当前不可用'):stale?pill('草稿已过期'):pill('待人工核对')),el('p',{},draft.summary),note(`状态 ${label(draft.status)} · 负责人 ${text(draft.owner)} · 版本 ${draft.version}`),button('重新读取预览快照',async()=>{const fresh=await api(`/handoff-drafts/${draft.id}`);modal('当前预览快照',pre(fresh));}),note(blockedReason||'编辑内容、证据、验证或问题版本变化后须重新预览。'),button('已核对，确认回写',async()=>{
   const f=document.querySelector('[data-form="生成 / 更新预览"]');const latest=f?values(f):edits;
   if(blockedReason)throw new Error(blockedReason);
   if(stale||JSON.stringify(pickDraft(draft))!==JSON.stringify(pickDraft(latest)))throw new Error('草稿已变化，请保留编辑内容重新预览');
   await api(`/issues/${currentIssue.id}/handoffs`,{method:'POST',revision:currentIssue.revision,key:state.handoffKeys.get(draft.id)||(()=>{const key=idempotencyKey();state.handoffKeys.set(draft.id,key);return key;})(),body:{draftId:draft.id,version:draft.version,hash:draft.hash,confirmed:true}});
   state.draft=null;const blank={mode:'manual',turnId:'',summary:'',owner:draft.owner,status:draft.status};state.handoffEdits.set(currentIssue.id,blank);saver.change(blank);try{await saver.flush();}catch{}await refresh();notify('内部工单已更新，历史快照已保存。');
  },'primary',Boolean(blockedReason))):null,
  heading('不可变更新历史'),updates.length?updates.slice().reverse().map(u=>el('article',{class:'card'},pill(u.status),el('p',{},u.summary),note(`${text(u.owner)} · ${text(u.mode)} · ${u.createdAt}`),button('读取当前可用快照',async()=>{await refresh();const current=all('updates').find(x=>x.id===u.id);modal('更新快照',current?pre(current.snapshot):empty('当前记录不可访问。'));}))):empty('还没有工单更新。'),
  heading('问题变更历史'),all('issueHistory').filter(h=>h.issueId===currentIssue.id).slice().reverse().map(issueHistoryCard)
 );
}
function issueHistoryCard(history){
 const before=history.before||{},after=history.after||{};
 const changes=[];
 const fields={title:'标题',description:'问题现象',owner:'负责人',priority:'优先级',status:'状态'};
 const compact=value=>{const result=text(value);return result.length>90?result.slice(0,90)+'…':result;};
 const blank=value=>value===undefined||value===null||value==='';
 for(const [key,name]of Object.entries(fields)){
  if(!(blank(before[key])&&blank(after[key]))&&JSON.stringify(before[key])!==JSON.stringify(after[key]))changes.push(`${name}：${before[key]===undefined?'未填写':compact(key==='status'?label(before[key]):before[key])} → ${compact(key==='status'?label(after[key]):after[key])}`);
 }
 const contextLabels={app:'应用',platform:'平台',environment:'环境',device:'设备',version:'版本',build:'构建',begin:'开始时间',end:'结束时间'};
 for(const [key,name]of Object.entries(contextLabels)){
  if(!(blank(before.context?.[key])&&blank(after.context?.[key]))&&JSON.stringify(before.context?.[key])!==JSON.stringify(after.context?.[key]))changes.push(`${name}：${compact(before.context?.[key])} → ${compact(after.context?.[key])}`);
 }
 for(const [key,name]of [['assetIds','关联日志'],['evidenceIds','证据范围']]){
  if(JSON.stringify(before[key]||[])!==JSON.stringify(after[key]||[]))changes.push(`${name}：${before[key]?.length||0} → ${after[key]?.length||0} 项`);
 }
 const reason=history.reason==='Issue created'?'记录初始问题范围':history.reason;
 return el('article',{class:'card'},heading(label(history.action),after.status?pill(after.status):null),note(`${text(history.createdAt)} · ${text(history.actor)} · 版本 ${text(history.revision)}`),changes.length?el('ul',{},changes.map(change=>el('li',{},change))):note(history.availability&&history.availability!=='available'?'记录正文当前不可用。':'未改变问题字段。'),reason?el('p',{},'原因 / 说明：'+compact(reason)):note('未填写变更原因。'),el('details',{},el('summary',{},'原始记录详情'),pre(history)));
}

window.addEventListener('beforeunload',event=>{if([...draftAutosavers.values()].some(s=>s.state().dirty||s.state().saving)){event.preventDefault();event.returnValue='';}});
async function legacyTasks(){
 const result=await api('/legacy/tasks');const tasks=list(result,'tasks');
 modal('已授权历史任务 · 只读',el('div',{},note('Native / Web 旧标识与工作台资产标识独立保存。仅显示服务端明确授权的项目映射。'),tasks.length?tasks.map(t=>el('article',{class:'card'},el('strong',{},`${text(t.legacyRef?.kind)} / ${text(t.legacyRef?.taskId)}`),note(`${text(t.appId)} · ${text(t.platform)} · ${text(t.deviceId)} · 构建 ${text(t.build)} · ${text(t.logDate)}`),pre(t),button('读取已知详情 ID',()=>legacyDetail(t)))):empty('没有已授权历史任务。')));
}
function legacyDetail(taskRecord){
 const ref=taskRecord.legacyRef;
 modal('历史任务详情 · '+text(ref?.taskId),form([note('输入该历史任务的原始详情 ID；不会转成工作台 eventId。'),field('detailId','原始详情 ID')],'读取原始详情',async v=>{
  if(!v.detailId.trim())throw new Error('请输入历史详情 ID');
  const detail=await api(`/legacy/tasks/${encodeURIComponent(ref.kind)}/${encodeURIComponent(ref.taskId)}/details/${encodeURIComponent(v.detailId.trim())}`);
  modal('历史详情 · 只读',el('div',{},pre(detail),issue()?button('作为人工材料加入当前调查',async()=>{await api(`/issues/${issue().id}/evidence`,{method:'POST',body:{kind:'manual',label:`历史 ${ref.kind} / ${ref.taskId} / ${v.detailId}`,originRef:{legacyRef:detail.legacyRef},content:JSON.stringify(detail)}});$('dialog').close();await refresh();}):null,note('历史材料作为未核验人工材料保存，并保留独立原始标识。')));
 }));
}

function collection(){return el('main',{class:'page'},heading('日志回捞'),el('article',{class:'deferred'},pill('待接入'),el('h2',{},'阿里云日志回捞暂未接入'),el('p',{},'正式工作台不会模拟推送或设备回执。阿里云接受、客户端收到、上传与平台接收需要真实接口确认。当前可通过手动导入继续阅读与调查。'),button('导入日志',upload,'primary')),heading('Web 演示材料'),note('样例仅在明确标记的演示项目中载入，不混入真实调查。'),button('查看服务端样例',async()=>{const fixtures=await api('/demo-fixtures');modal('演示材料',el('div',{},list(fixtures,'fixtures').map(f=>el('article',{class:'card'},el('h3',{},f.name||f.id),el('p',{},f.description||''),button('下载样例',()=>download(`/demo-fixtures/${f.id}/download`,f.name||'demo.jsonl')),button('载入演示项目',async()=>{await api(`/demo-fixtures/${f.id}/load`,{method:'POST',body:{}});$('dialog').close();state.page='reader';await refresh();}))))); }));}
render();
// Poll only real queued work. A failed refresh is visible; browser disconnects never
// turn running server operations into fabricated cancellation or completion.
let polling=false;
setInterval(async()=>{if(!state.session||polling||!all('parseRuns').some(active)&&!all('turns').some(active)&&Date.now()-lastWorkspaceRefresh<20000)return;polling=true;try{await refresh();}catch(e){notify(`更新进度失败：${e.message}`,true);}finally{polling=false;}},2000);
