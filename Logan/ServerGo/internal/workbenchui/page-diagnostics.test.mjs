import {eventSummary} from './static/reader-state.mjs';
import {filtersQuery} from './static/api.mjs';
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {pageDiagnostic,groupPageAttempts,filterPageAttempt} from './static/page-diagnostics.mjs';
const event=(eventName,extra={})=>({id:eventName,runId:'r',schema:'xlb.page-diagnostic',schemaVersion:1,pageAttemptId:'a',pageName:'MiniApp',eventName,stage:'page',outcome:'started',occurredAtMs:1000,source:'rn',...extra});
test('only the explicit versioned namespace is associated; plain JSON and malformed versions stay ordinary logs',()=>{
 assert.equal(pageDiagnostic({content:JSON.stringify({pageAttemptId:'a',eventName:'render_failed'})}),null);
 assert.equal(pageDiagnostic(event('page_open_started',{schemaVersion:2})),null);
 assert.equal(pageDiagnostic(event('unknown_event')),null);
 assert.equal(pageDiagnostic(event('page_open_started',{pageAttemptId:'a/secret'})),null);
 assert.equal(pageDiagnostic({content:JSON.stringify(event('page_open_started'))}),null,'raw JSON cannot bypass the server diagnostic validation');
});
test('group by run and attempt, preserve retry identity and original event references',()=>{
 const first=event('page_open_started'),retry=event('page_open_started',{id:'retry',pageAttemptId:'b',retryOf:'a'});
 const groups=groupPageAttempts([first,retry,event('page_open_started',{runId:'other'})],'r');
 assert.equal(groups.length,2);assert.equal(groups[0].events[0],first);assert.equal(groups[1].retryOf,'a');
 assert.equal(filterPageAttempt([first,retry], 'r','a').length,1);
 assert.equal(filterPageAttempt([first,retry], 'r','').length,2);
});
test('late replay ordering uses occurred time and same-source monotonic order; never compares native and RN clocks',()=>{
 const events=[event('content_ready',{id:'ready',occurredAtMs:2000,monotonicMs:20}),event('page_open_started',{occurredAtMs:1000,monotonicMs:10}),event('rn_mounted',{occurredAtMs:1500,monotonicMs:5000,source:'native'})];
 assert.deepEqual(groupPageAttempts(events,'r')[0].events.map(e=>e.id),['page_open_started','rn_mounted','ready']);
});
test('slow plus actual interactive is successful with a slow flag, and injected faults remain labeled',()=>{
 const [attempt]=groupPageAttempts([event('page_open_started'),event('page_slow',{outcome:'suspected',durationMs:5000,testInjected:true,scenario:'slow'}),event('content_ready',{outcome:'succeeded'}),event('page_interactive',{outcome:'succeeded'})],'r');
 assert.equal(attempt.status,'succeeded');assert.equal(attempt.slow,true);assert.equal(attempt.testInjected,true);assert.equal(attempt.gaps.length,0);
});
test('incomplete, suspected blank, abandoned and failed are distinct and ready does not imply interactive',()=>{
 const status=names=>groupPageAttempts(names.map(name=>event(name)),'r')[0];
 assert.equal(status(['page_open_started']).status,'incomplete');
 assert.equal(status(['page_open_started','content_ready']).status,'incomplete');
 assert.equal(status(['page_open_started','page_suspected_blank']).status,'suspected');
 assert.equal(status(['page_open_started','page_abandoned']).status,'abandoned');
 assert.equal(status(['page_open_started','render_failed']).status,'failed');
 assert.match(status(['page_open_started']).gaps.join(','),/content_ready/);
 assert.equal(status(['content_ready','page_interactive']).status,'succeeded');
 assert.match(status(['content_ready','page_interactive']).gaps.join(','),/page_open_started/);
});
const source=readFileSync(new URL('./static/app.js',import.meta.url),'utf8');
const extract=(start,end='\nfunction ')=>source.slice(source.indexOf(start),source.indexOf(end,source.indexOf(start)+start.length));
test('reader attempt selection resets when switching runs without losing other filters',async()=>{
 const state={project:'p',issueId:'i',runId:'old',pageAttemptId:'a',filters:{level:'ERROR'},selected:new Set()};
 const c={state,render(){},loadEvents:async()=>{}};vm.createContext(c);vm.runInContext(extract('async function selectRun(', '\nlet '),c);await c.selectRun('new');
 assert.equal(state.pageAttemptId,'');assert.equal(state.filters.level,'ERROR');
});
test('attempt selector links stages to existing event details and uses complete server coverage',async()=>{
 const events=[event('page_open_started'),event('page_suspected_blank',{testInjected:true})],state={runId:'r',events,pageAttemptId:'a',filters:{level:'WARN'},nextCursor:'next',selected:new Set(),pageAttempts:groupPageAttempts(events,'r')};
 const el=(tag,attrs={},...children)=>({tag,attrs,children:children.flat(Infinity)}),nodes=root=>root&&typeof root==='object'?[root,...(root.children||[]).flatMap(nodes)]:[];
 let opened;const c={eventSummary,task:fn=>fn(),loadEvents:async()=>{},state,el,groupPageAttempts,filterPageAttempt,pageDiagnostic,note:s=>el('p',{},s),button:(title,click)=>el('button',{click},title),openEvent:async e=>{opened=e;},render(){}};
 vm.createContext(c);vm.runInContext(extract('function pageDiagnosticsPanel('),c);
 const tree=c.pageDiagnosticsPanel(),flat=nodes(tree),text=JSON.stringify(tree);
 assert.match(text,/疑似白屏/);assert.match(text,/受控故障/);assert.match(text,/完整批次/);assert.match(text,/筛选/);
 await flat.find(n=>n.tag==='button'&&n.children.includes('MiniApp · 开始打开页面')).attrs.click();assert.equal(opened,events[0]);
 const selector=flat.find(n=>n.tag==='select');await selector.attrs.onChange({target:{value:''}});assert.equal(state.pageAttemptId,'');
});
test('reader filters only the selected attempt and evidence action cannot include hidden attempts',async()=>{
 const first=event('page_open_started'),other=event('page_open_started',{id:'other',pageAttemptId:'b'}),state={runId:'r',events:[first,other],pageAttemptId:'a',selected:new Set(),total:2};
 const el=(tag,attrs={},...children)=>({tag,attrs,children:children.flat(Infinity)}),nodes=root=>root&&typeof root==='object'?[root,...(root.children||[]).flatMap(nodes)]:[];
 let evidence;const c={state,el,run:()=>({status:'succeeded'}),readable:()=>true,histogram:()=>null,filterForm:()=>null,pageDiagnosticsPanel:()=>null,filterPageAttempt,eventRow:e=>el('event',{},e.id),eventDetails:()=>null,button:(title,click)=>el('button',{click},title),empty:s=>el('empty',{},s),addLogEvidence:async()=>{evidence=[...state.selected];}};
 vm.createContext(c);vm.runInContext(extract('function reader('),c);const flat=nodes(c.reader());assert.deepEqual(flat.filter(n=>n.tag==='event').flatMap(n=>n.children),[first.id]);
 await flat.find(n=>n.tag==='button'&&n.children.includes('保存全部匹配事件为证据')).attrs.click();assert.deepEqual(evidence,[first.id]);
 state.nextCursor='next';await assert.rejects(flat.find(n=>n.tag==='button'&&n.children.includes('保存全部匹配事件为证据')).attrs.click(),/未加载/);
});
test('implicit refresh run replacement resets attempt selection',async()=>{
 const state={project:'p',token:'t',page:'reader',runId:'old',pageAttemptId:'a',workspace:{},issueId:''};
 const workspace={parseRuns:[{id:'new',status:'succeeded'}]},c={state,connectionText:()=>'',Promise,Date,Map,filtersQuery,lastWorkspaceRefresh:0,all:key=>state.workspace[key]||[],api:async path=>path==='/workspace'?workspace:{},issue:()=>null,run:()=>state.workspace.parseRuns?.find(r=>r.id===state.runId),readable:r=>r?.status==='succeeded',expired:()=>false,draftAutosavers:new Map(),$:()=>({open:false}),loadEvents:async()=>{},render(){}};
 vm.createContext(c);vm.runInContext(extract('async function refresh(', '\nasync function loadEvents'),c);await c.refresh();assert.equal(state.runId,'new');assert.equal(state.pageAttemptId,'');
});
test('chronological terminal status prevents late completion from reviving abandoned or failed attempts',()=>{
 for(const terminal of ['page_abandoned','render_failed','bundle_load_failed']){
  const chronological=['page_open_started',terminal,'content_ready','page_interactive'].map((name,index)=>event(name,{occurredAtMs:1000+index}));
  // Replay delivery may be shuffled; lifecycle reduction follows occurrence time.
  const [attempt]=groupPageAttempts([chronological[3],chronological[0],chronological[2],chronological[1]],'r');
  assert.equal(attempt.status,terminal==='page_abandoned'?'abandoned':'failed',terminal);
  assert.equal(attempt.events.length,4,'late evidence remains inspectable');
 }
});
test('suspected blank can recover through real readiness, and a slow attempt can complete',()=>{
 for(const observation of ['page_suspected_blank','page_slow']){
  const events=['page_open_started',observation,'content_ready','page_interactive'].map((name,index)=>event(name,{occurredAtMs:1000+index}));
  const [attempt]=groupPageAttempts(events,'r');assert.equal(attempt.status,'succeeded');assert.equal(attempt.slow,observation==='page_slow');
 }
});
test('successful opening remains terminal while later explicit failures are separately observable',()=>{
 const events=['page_open_started','content_ready','page_interactive','render_failed','page_abandoned'].map((name,index)=>event(name,{occurredAtMs:1000+index}));
 const [attempt]=groupPageAttempts(events,'r');assert.equal(attempt.status,'succeeded');assert.equal(attempt.hasErrors,true);assert.equal(attempt.events.length,5);
 const [normal]=groupPageAttempts(events.slice(0,3),'r');assert.equal(normal.hasErrors,false);
});
