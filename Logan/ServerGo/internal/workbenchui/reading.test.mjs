import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';
import {filtersQuery} from './static/api.mjs';
import {createNavigation,eventSummary,readRoute,routeHash} from './static/reader-state.mjs';

const source=readFileSync(new URL('./static/app.js',import.meta.url),'utf8');
function loadFunction(context,name,next='\nfunction '){
 const start=source.indexOf(name),end=source.indexOf(next,start+name.length);
 assert.ok(start>=0&&end>start);vm.createContext(context);vm.runInContext(source.slice(start,end),context);return context;
}
test('refresh retains more than one loaded page and the next cursor',async()=>{
 const rows=Array.from({length:450},(_,id)=>({id:'e'+id})),calls=[];
 const state={runId:'r',project:'p',token:'t',issueId:'i',filters:{},events:[],pageAttemptId:''};
 const c={state,Promise,filtersQuery,readable:()=>true,run:()=>({id:'r'}),api:async path=>{
  calls.push(path);const q=new URL('http://local'+path).searchParams;
  if(path.includes('/page-attempts'))return {attempts:[],filterHash:'hash'};
  if(path.includes('/histogram'))return {filterHash:'hash'};
  const start=Number(q.get('cursor')||0),end=Math.min(450,start+Number(q.get('limit')||200));
  return {events:rows.slice(start,end),total:450,nextCursor:end<450?String(end):'',filterHash:'hash'};
 }};
 loadFunction(c,'async function loadEvents(','\nasync function selectRun');
 await c.loadEvents();await c.loadEvents(true);assert.equal(state.events.length,400);
 await c.loadEvents();assert.equal(state.events.length,400);assert.equal(state.nextCursor,'400');
});
test('navigation records user steps, replaces passive updates and restores scoped filters without secrets',async()=>{
 const location={hash:'#project=p&page=reader'},entries=[];
 const history={pushState(data,_,hash){entries.push({data,hash,mode:'push'});location.hash=hash;},replaceState(data,_,hash){entries.push({data,hash,mode:'replace'});location.hash=hash;}};
 const state={...readRoute(location.hash),token:'never-in-history',filters:{query:'failure'}};let restored;
 const nav=createNavigation({history,location,getState:()=>state,restore:async s=>{restored=s;}});
 nav.sync();const original=entries.at(-1);
 state.page='issues';nav.sync();assert.equal(entries.at(-1).mode,'push');
 nav.sync('replace');assert.equal(entries.length,2);
 state.issueQuery='abc';nav.sync('replace');assert.equal(entries.at(-1).mode,'replace');
 location.hash=original.hash;await nav.restore(original.data);
 assert.equal(restored.page,'reader');assert.equal(restored.filters.query,'failure');
 assert.equal(JSON.stringify(entries).includes('never-in-history'),false);
 assert.equal(routeHash(readRoute('#page=unknown&tool=unknown')).includes('tool=evidence'),true);
});
test('normalized diagnostic summary is readable while foreign JSON and original content are unchanged',()=>{
 const event={schema:'xlb.page-diagnostic',schemaVersion:1,pageAttemptId:'a',pageName:'MiniApp',eventName:'bundle_load_failed',errorCode:'REMOTE_LOAD_FAILED',content:'original JSON'};
 assert.match(eventSummary(event).title,/Bundle 加载失败/);assert.match(eventSummary(event).detail,/REMOTE_LOAD_FAILED/);
 assert.equal(event.content,'original JSON');assert.equal(eventSummary({content:JSON.stringify(event)}),null);
});
test('rapid browser back/forward applies the newest route even while an older restore is pending',async()=>{
 const location={hash:'#project=p&page=issues'},calls=[];let release;
 const pending=new Promise(r=>release=r);
 const nav=createNavigation({history:{},location,getState:()=>({}),restore:async s=>{calls.push(s.page);if(s.page==='issues')await pending;}});
 const first=nav.restore({});location.hash='#project=p&page=collection';const second=nav.restore({});
 release();await Promise.all([first,second]);assert.deepEqual(calls,['issues','collection']);
});
test('issue search lives in state and is applied after every render',()=>{
 const el=(tag,attrs={},...children)=>({tag,attrs,children:children.flat(Infinity)});
 const c={state:{issueQuery:'needle'},el,all:()=>[{id:'a',title:'needle'},{id:'b',title:'other'}],issueCard:i=>el('article',{id:i.id}),button:()=>null,note:()=>null,empty:()=>null,render:()=>{},filterIssues:(rows,q)=>rows.filter(r=>r.title.includes(q)),document:{querySelectorAll:()=>[]}};
 loadFunction(c,'function issuesPage(');let tree=c.issuesPage();
 const walk=n=>n&&typeof n==='object'?[n,...n.children.flatMap(walk)]:[];
 assert.deepEqual(walk(tree).filter(n=>n.tag==='article').map(n=>n.attrs.id),['a']);
 walk(tree).find(n=>n.tag==='input').attrs.onInput({target:{value:'other'}});
 assert.equal(c.state.issueQuery,'other');tree=c.issuesPage();assert.deepEqual(walk(tree).filter(n=>n.tag==='article').map(n=>n.attrs.id),['b']);
});
test('unchanged background refresh revalidates authorization without refetching or rebuilding a stable reading window',async()=>{
 const workspace={issues:[{id:'i'}],parseRuns:[{id:'r',status:'succeeded'}]},session={role:'reviewer'};
 const state={project:'p',token:'t',issueId:'i',runId:'r',eventId:'',pageAttemptId:'',page:'reader',workspace,session,filters:{},events:Array.from({length:400},(_,id)=>({id}))};
 state.eventsReadKey=JSON.stringify(['p','t','r','']);let loads=0,renders=0;const calls=[];
 const c={state,connectionText:()=>'',Promise,Date,Map,lastWorkspaceRefresh:0,filtersQuery,all:k=>state.workspace[k]||[],api:async path=>{calls.push(path);return path==='/workspace'?workspace:session;},issue:()=>workspace.issues[0],run:()=>workspace.parseRuns[0],readable:()=>true,expired:()=>false,draftAutosavers:new Map(),$:()=>({open:false}),loadEvents:async()=>loads++,render:()=>renders++,notify:()=>{}};
 loadFunction(c,'async function refresh(','\nasync function loadEvents');await c.refresh({background:true});
 assert.equal(loads,0);assert.equal(renders,0);assert.equal(state.events.length,400);assert.deepEqual(calls,['/workspace','/session']);
});
