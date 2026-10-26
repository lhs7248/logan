import {pageDiagnostic} from './page-diagnostics.mjs';

export function filterIssues(issues=[],query='') {
 const needle=query.trim().toLowerCase();
 return issues.filter(i=>[i.id,i.title,i.context?.device,i.owner,i.description].join(' ').toLowerCase().includes(needle));
}

export function eventSummary(event) {
 const d=pageDiagnostic(event);if(!d)return null;
 const names={page_open_started:'开始打开页面',bundle_load_started:'开始加载 Bundle',bundle_load_succeeded:'Bundle 加载成功',bundle_load_failed:'Bundle 加载失败',rn_mounted:'RN 视图已挂载',content_ready:'业务内容已就绪',page_interactive:'页面已可交互',render_failed:'页面渲染失败',page_suspected_blank:'疑似白屏，尚未确认',page_slow:'页面加载慢',page_abandoned:'用户已离开页面'};
 return {title:`${d.pageName} · ${names[d.eventName]}`,detail:[d.errorCode,d.durationMs!==undefined?`事件报告耗时 ${d.durationMs} ms`:null,d.testInjected?'受控故障':null].filter(Boolean).join(' · '),attemptId:d.pageAttemptId};
}

export function readRoute(hash) {
 const p=new URLSearchParams(hash.replace(/^#/,''));
 return {project:p.get('project')||'',page:['reader','issues','collection'].includes(p.get('page'))?p.get('page'):'reader',tool:['source','ai','evidence','collect','verify','handoff','parse','result'].includes(p.get('tool'))?p.get('tool'):'evidence',issueId:p.get('issue')||'',runId:p.get('run')||'',parseAssetId:p.get('asset')||'',issueDetail:p.get('detail')==='1',pendingImportId:p.get('batch')||'',pageAttemptId:p.get('attempt')||'',eventId:p.get('event')||''};
}
export function routeHash(s) {
 return '#'+new URLSearchParams({project:s.project,page:s.page,tool:s.tool,issue:s.issueId,run:s.runId,asset:s.parseAssetId||'',detail:s.issueDetail?'1':'0',batch:s.pendingImportId||'',attempt:s.pageAttemptId||'',event:s.eventId||''});
}
// History contains navigation and operator filters only, never token/log bodies.
// Background refresh replaces the current entry; user changes create an entry.
function safeReadingView(view={}) {
 const number=v=>Number.isFinite(Number(v))?Math.max(0,Number(v)):0;
 return {loaded:Math.min(100000,Math.floor(number(view.loaded))),selected:Array.isArray(view.selected)?view.selected.filter(v=>typeof v==='string'&&v.length<=256).slice(0,100):[],scrolls:Object.fromEntries(['reader','events','tools','library'].map(k=>[k,number(view.scrolls?.[k])])),pageScroll:number(view.pageScroll),library:Boolean(view.library)};
}
export function createNavigation({history,location,getState,restore}) {
 let previous='',previousSnapshot='',restoring=0,lastRestore='';
 const snapshot=()=>{const s=getState();return {...readRoute(routeHash(s)),filters:{...s.filters},issueQuery:s.issueQuery||'',readingView:safeReadingView(s.readingView)};};
 const signature=s=>JSON.stringify({...s,readingView:undefined});
 return {
  capture(){if(previous&&!restoring){const s=snapshot();history.replaceState({loganNavigation:s},'',routeHash(s));previousSnapshot=JSON.stringify(s);}},
  sync(mode='push') {
   if(restoring)return;
   const s=snapshot(),key=signature(s),full=JSON.stringify(s);if(full===previousSnapshot)return;
   const method=previous&&key!==previous&&mode==='push'?'pushState':'replaceState';
   history[method]({loganNavigation:s},'',routeHash(s));previous=key;previousSnapshot=full;
  },
  async restore(entry) {
   const route=readRoute(location.hash),saved=entry?.loganNavigation;
   const same=saved&&routeHash(saved)===routeHash(route);
   const s={...route,filters:same?{...saved.filters}:{},issueQuery:same?saved.issueQuery||'':'',readingView:safeReadingView(same?saved.readingView:{})};
   const key=signature(s);
   if(restoring&&key===lastRestore)return;
   lastRestore=key;previous=key;previousSnapshot=JSON.stringify(s);restoring++;
   try {await restore(s);} finally {restoring--;}
  }
 };
}

// Operators enter wall-clock time in Beijing, independent of browser timezone.
export function beijingInput(value='') {
 if(!value||!Number.isFinite(Date.parse(value)))return '';
 return new Date(Date.parse(value)+8*3600000).toISOString().slice(0,19);
}
export function beijingISO(value='') {
 if(!value)return '';
 if(!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d{1,3})?)?$/.test(value))throw new Error('请选择有效的北京时间');
 const iso=value+'+08:00';
 if(!Number.isFinite(Date.parse(iso))||beijingInput(iso).slice(0,value.length)!==value)throw new Error('请选择有效的北京时间');
 return iso;
}
export function unavailableReason(capabilities,permissions,title) {
 const kind={'发送本轮问题':'model','查找源码':'source','读取差异':'source','登记映射':'source'}[title];
 if(!kind)return '';
 if(!capabilities?.[kind]?.configured)return kind==='model'?'模型未配置，请先整理证据、记录验证并人工回写。':'源码连接未配置，可添加未核验人工片段。';
 if(!permissions?.includes(kind))return '当前身份没有此操作权限，请联系管理员。';
 return '';
}
