import {beijingInput,beijingISO,unavailableReason} from './static/reader-state.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';
import {createNavigation,readRoute,routeHash} from './static/reader-state.mjs';
const source=readFileSync(new URL('./static/app.js',import.meta.url),'utf8');
const extract=(start,end='\nfunction ')=>source.slice(source.indexOf(start),source.indexOf(end,source.indexOf(start)+start.length));
test('browser history round trip includes window, exact selection and scroll without retaining log text',async()=>{
 const location={hash:'#project=p&page=reader'},entries=[];
 const history={replaceState(data,_,hash){entries[entries.length?entries.length-1:0]={data,hash};location.hash=hash;},pushState(data,_,hash){entries.push({data,hash});location.hash=hash;}};
 const state={...readRoute(location.hash),filters:{},readingView:{loaded:400,selected:['e399'],scrolls:{events:900},pageScroll:100},token:'secret',events:[{content:'not-for-history'}]};let restored;
 const nav=createNavigation({history,location,getState:()=>state,restore:async s=>{restored=s;}});nav.sync();state.page='issues';nav.sync();location.hash=entries[0].hash;await nav.restore(entries[0].data);
 assert.equal(restored.readingView?.loaded,400);assert.deepEqual(restored.readingView.selected,['e399']);assert.equal(restored.readingView.scrolls.events,900);assert.doesNotMatch(JSON.stringify(entries),/secret|not-for-history/);
});
test('most recent manual issue progress is shown instead of default text or an older AI answer',()=>{
 const issue={id:'i',assetIds:['a']};
 const c={all:key=>key==='updates'?[{issueId:'i',mode:'manual',summary:'下一步核对服务侧请求',createdAt:'2026-10-26T10:00:00Z'}]:[],latestAnswer:()=>null};
 vm.createContext(c);vm.runInContext(extract('function nextStep('),c);
 assert.match(c.nextStep(issue),/核对服务侧请求/);
});
const walk=n=>n&&typeof n==='object'?[n,...(n.children||[]).flatMap(walk)]:[];
function view(){const el=(tag,attrs={},...children)=>({tag,attrs,children:children.flat(Infinity)});return {el,state:{tool:'evidence'},all:()=>[],issue:()=>null,asset:()=>({id:'a'}),heading:()=>null,note:x=>x,button:(title,click)=>el('button',{click},title),field:(name,title,value,type,options)=>el('field',{name,value,options},title),form:(fields,title,submit)=>el('form',{submit},fields,title),timeField:(name,title)=>el('field',{name,value:''},title),currentEvidence:()=>[],expired:()=>false};}
test('verification purpose switch rebuilds result choices and title while preserving observations',()=>{
 const c=view();let shown;c.modal=(title,content)=>shown={title,content};c.values=()=>({purpose:'hypothesis',method:'keep steps',observed:'keep observations',result:'pass',hypothesisId:'h'});
 vm.createContext(c);vm.runInContext(extract('function verificationForm(','\nconst draftAutosavers'),c);c.verificationForm('fix');
 const wrapper=walk(shown.content).find(n=>typeof n.attrs?.onChange==='function');assert.ok(wrapper,'purpose selector must update its dependent fields');
 wrapper.attrs.onChange({target:{name:'purpose',form:{querySelectorAll:()=>[]}}});
 assert.equal(shown.title,'记录假设验证');const fields=walk(shown.content).filter(n=>n.tag==='field');assert.equal(fields.find(n=>n.attrs.name==='observed').attrs.value,'keep observations');
 assert.deepEqual(Array.from(fields.find(n=>n.attrs.name==='result').attrs.options,o=>o.value),['uncertain','supported','refuted']);
});
test('independent reading offers an explicit association separate from opening another issue',async()=>{
 const c=view();let linked='',opened='';c.all=()=>[{id:'i',title:'Existing'}];c.linkCurrentReading=async id=>linked=id;c.openIssue=async id=>opened=id;
 vm.createContext(c);vm.runInContext(extract('function toolPanel('),c);const tree=walk(c.toolPanel());const link=tree.find(n=>n.tag==='button'&&n.children.includes('关联当前日志到：Existing'));
 assert.ok(link,'association must have its own explicit action');await link.attrs.click();assert.equal(linked,'i');assert.equal(opened,'');
});

test('import keeps identity optional in advanced metadata and uses Beijing date pickers',()=>{
 const c=view();c.beijingInput=x=>x;c.timeField=(name,title)=>c.field(name,title,'','datetime-local');vm.createContext(c);vm.runInContext(extract('function uploadMetadataFields('),c);
 const tree=walk({children:c.uploadMetadataFields()});assert.ok(tree.some(n=>n.tag==='details'),'secondary metadata must be collapsible');
 for(const name of ['begin','end'])assert.equal(tree.find(n=>n.attrs?.name===name).attrs.value,'');
});
test('parse location is explicit and offers return to reading without removing investigation tabs',()=>{
 assert.match(source,/aria-current[^\n]*state.tool==='parse'/);
 assert.match(extract('function parsePanel('),/返回日志阅读/);
});
test('clearing filters discards old selection as well as attempt and event detail',()=>{
 const c=view();c.state={filters:{query:'old'},pageAttemptId:'a',selected:new Set(['e']),eventDetails:{event:{}},eventId:'e',source:{}};c.loadEvents=async()=>{};c.render=()=>{};c.form=(fields,title,submit)=>({children:fields,querySelector:()=>({setAttribute(){}}),append(n){this.children.push(n);}});vm.createContext(c);vm.runInContext(extract('function filterForm('),c);const f=c.filterForm();const clear=f.children.find(n=>n?.tag==='button'&&n.children.includes('清除'));return clear.attrs.click().then(()=>{assert.equal(c.state.selected.size,0);assert.equal(c.state.eventId,'');assert.equal(c.state.eventDetails,null);});
});

test('Beijing time editing round trips UTC, preserves unknown and rejects invalid dates',()=>{
 assert.equal(beijingInput('2026-10-22T17:03:24Z'),'2026-10-23T01:03:24');
 assert.equal(beijingISO('2026-10-23T01:03:24'),'2026-10-23T01:03:24+08:00');
 assert.equal(beijingISO(''),'');assert.equal(beijingInput(''),'');assert.throws(()=>beijingISO('2026-02-30T01:00'));assert.throws(()=>beijingISO('2026-10-26T10:00Z'));
});
test('unconfigured and unauthorized provider operations are gated without blocking manual workflow',()=>{
 assert.match(unavailableReason({model:{configured:false}},['model'],'发送本轮问题'),/人工回写/);
 assert.match(unavailableReason({source:{configured:true}},[],'查找源码'),/权限/);
 assert.equal(unavailableReason({model:{configured:true}},['model'],'发送本轮问题'),'');
 assert.equal(unavailableReason({},[],'保存人工片段'),'');
});

test('rapid identical evidence saves share one in-flight write and completed exact selection is reused',async()=>{
 const state={project:'p',token:'t',issueId:'i',runId:'r',selected:new Set(['e'])};let release,writes=0,evidence=[];const pending=new Promise(r=>release=r);
 const c={state,issue:()=>({id:'i'}),asset:()=>({name:'file'}),currentEvidence:()=>evidence,notify(){},api:async()=>{writes++;await pending;},refresh:async()=>{evidence=[{kind:'log',availability:'available',originRef:{runId:'r',eventIds:['e']}}];}};
 vm.createContext(c);vm.runInContext(extract('async function addLogEvidence(','\nasync function download'),c);const one=c.addLogEvidence(),two=c.addLogEvidence();assert.equal(writes,1);release();await Promise.all([one,two]);await c.addLogEvidence();assert.equal(writes,1);
});
test('boolean hidden=false exposes the hypothesis field rather than rendering a hidden attribute',()=>{
 const c={document:{createElement:()=>({hidden:false,setAttribute(k,v){if(k==='hidden')this.hidden=true;},append(){},addEventListener(){}})},Node:class{}};
 vm.createContext(c);vm.runInContext(extract('function el(','\nconst button'),c);assert.equal(c.el('div',{hidden:false}).hidden,false);assert.equal(c.el('div',{hidden:true}).hidden,true);
});

test('an unparsed asset remains addressable across reload and browser history',()=>{
 const s=readRoute('#project=p&page=reader&tool=parse&asset=unparsed');assert.equal(s.parseAssetId,'unparsed');
 assert.match(routeHash(s),/asset=unparsed/);
});

test('time ranges reject reversed order without borrowing a timezone from the browser',()=>{
 const c={beijingISO,Date};vm.createContext(c);vm.runInContext(extract('function normalizeTimes('),c);
 assert.throws(()=>c.normalizeTimes({begin:'2026-10-26T10:05',end:'2026-10-26T10:00'},['begin','end']),/不能晚于/);
 assert.equal(c.normalizeTimes({start:'2026-10-26T10:00',end:''},['start','end']).start,'2026-10-26T10:00+08:00');
});

test('failed workspace sync keeps the readable window and exposes a disconnected status',async()=>{
 const state={project:'p',token:'t',events:Array.from({length:400},(_,id)=>({id})),workspace:{},session:{},connection:{status:'connected',lastSyncedAt:'old'}};
 const c={state,Promise,api:async()=>{throw new TypeError('network unavailable');},$:()=>({textContent:''})};
 vm.createContext(c);vm.runInContext(extract('async function refresh(','\nasync function loadEvents'),c);
 await assert.rejects(c.refresh(),/network unavailable/);assert.equal(state.connection.status,'error');assert.equal(state.connection.lastSyncedAt,'old');assert.equal(state.events.length,400);assert.ok(state.session);
});
test('connection badge distinguishes last sync from unsaved drafts and transient disconnects',()=>{
 const c={state:{connection:{status:'error'}},formatClock:()=> '10:00:00'};vm.createContext(c);vm.runInContext(extract('function connectionText('),c);
 assert.match(c.connectionText(),/同步失败/);c.state.connection={status:'connected',lastSyncedAt:'now'};assert.equal(c.connectionText(),'已连接 · 最近同步 10:00:00');assert.doesNotMatch(c.connectionText(),/服务器保存/);
});
test('rebuilding a modal starts at the header rather than retaining another form scroll',()=>{
 const dialog={scrollTop:500,showModal(){}},c={$:id=>id==='dialog'?dialog:{replaceChildren(){}},heading(){},button(){}};vm.createContext(c);vm.runInContext(extract('function modal(','\nlet lastWorkspaceRefresh'),c);c.modal('new',{});assert.equal(dialog.scrollTop,0);
});

test('history revalidates selections outside filters and rejects unavailable or foreign-run event IDs',async()=>{
 const state={project:'p',token:'t',issueId:'i',runId:'r',filters:{level:'INFO'},selected:new Set(),events:[],pendingReadingView:{selected:['hidden-error','unavailable','foreign']}};
 const c={state,Promise,list:(x,k)=>x[k]||[],filtersQuery:()=> 'level=INFO',readable:()=>true,run:()=>({id:'r'}),api:async path=>{
  if(path.includes('/page-attempts'))return {attempts:[]};if(path.includes('/histogram'))return {filterHash:'h'};
  if(path.includes('/context')){if(path.includes('unavailable'))throw new Error('denied');return {events:[{id:path.includes('foreign')?'foreign':'hidden-error',runId:path.includes('foreign')?'other':'r'}]};}
  return {events:[{id:'visible',runId:'r'}],total:1,nextCursor:'',filterHash:'h'};
 }};vm.createContext(c);vm.runInContext(extract('async function loadEvents(','\nasync function selectRun'),c);await c.loadEvents();assert.deepEqual([...state.selected],['hidden-error']);
});
test('selection enforces the existing 100-event evidence limit before silently dropping history IDs',()=>{
 const c={state:{selected:new Set(Array.from({length:100},(_,i)=>'e'+i))}};vm.createContext(c);vm.runInContext(extract('function setEventSelection('),c);
 assert.throws(()=>c.setEventSelection('new',true),/100/);assert.equal(c.state.selected.size,100);c.setEventSelection('e0',false);c.setEventSelection('new',true);assert.equal(c.state.selected.size,100);
});
