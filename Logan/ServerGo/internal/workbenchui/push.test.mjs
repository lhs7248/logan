import test from 'node:test';
import assert from 'node:assert/strict';
import {collectionActive, collectionStatusLabel, collectionProgress, createCollectionMutations} from './static/collection-state.mjs';
import {createCollectionUI} from './static/collection.mjs';

function component() {
 const calls=[];let modalContent;let refreshed=0;
 const state={project:'p',token:'synthetic',session:{role:'investigator'},issueId:'existing-investigation',runId:'existing-run',workspace:{
  capabilities:{collections:{configured:true}},issues:[],assets:[],
  collectionDrafts:[{id:'draft',title:'Preserved draft',createdAt:'2026-10-26T00:00:00Z'}],
  collections:[{id:'request',title:'Persisted request',currentAttemptId:'attempt',revision:7,deadlineAt:'2026-10-26T00:00:00Z',status:'dispatch_unknown',createdAt:'2026-10-26T00:01:00Z'}],
  pushAttempts:[{id:'attempt',collectionId:'request',number:1,dispatchStatus:'unknown',pushStatus:'unknown'}],pushEvents:[{collectionId:'request',eventType:'push_failed',occurredAt:'2026-10-26T00:01:00Z',receivedAt:'2026-10-26T00:02:00Z'}]}};
 const el=(tag,attrs={},...children)=>({tag,attrs,children:children.flat(Infinity).filter(x=>x!==null&&x!==undefined),append(...nodes){this.children.push(...nodes);},setAttribute(name,value){this.attrs[name]=value;}});
 const ui=createCollectionUI({el,button:(title,click)=>el('button',{click},title),field:()=>null,form:(fields,title,submit)=>el('form',{title,submit},fields),heading:()=>null,note:s=>el('p',{},s),pill:s=>s,empty:s=>s,modal:(title,content)=>{modalContent=content;},api:async(path,options)=>{calls.push({path,...options});return {id:'request'};},refresh:async()=>{refreshed++;},getState:()=>state,upload:()=>{},selectRun:()=>{},openIssue:()=>{},assetCard:()=>null});
 const walk=node=>typeof node==='object'?[node,...(node.children||[]).flatMap(walk)]:[node];
 return {ui,state,calls,walk,modal:()=>modalContent,refreshes:()=>refreshed};
}

test('actual tracking component keeps drafts and renders persisted stages without losing investigation selection',async()=>{
 const c=component(),tree=c.ui.page(),nodes=c.walk(tree),text=nodes.filter(x=>typeof x==='string').join(' ');
 assert.equal(nodes.find(n=>n?.tag==='details').attrs.open,undefined);
 assert.match(text,/Preserved draft/);assert.match(text,/Persisted request/);
 assert.match(text,/等待截止（北京时间）：2026-10-26 08:00/);assert.match(text,/发生（北京时间）：2026-10-26 08:01 · 接收（北京时间）：2026-10-26 08:02/);
 assert.match(text,/发送结果未知/);assert.match(text,/无客户端接收证据/);
 assert.match(text,/尚未建立 App 上传与本请求的自动关联/);
 await nodes.find(n=>n?.tag==='button'&&n.children[0]==='刷新推送状态').attrs.click();
 assert.equal(c.refreshes(),1);assert.equal(c.state.issueId,'existing-investigation');assert.equal(c.state.runId,'existing-run');
});
test('retry component requires confirmation and sends actual revisioned mutation instead of changing local status',async()=>{
 const c=component();await c.walk(c.ui.page()).find(n=>n?.tag==='button'&&n.children[0]==='失败与重试').attrs.click();const nodes=c.walk(c.ui.page());
 nodes.find(n=>n?.tag==='button'&&n.children[0]==='确认并创建新尝试').attrs.click();
 const submit=c.modal().attrs.submit;
 await assert.rejects(submit({reason:'retry'}, {querySelector:()=>({checked:false})}),/勾选确认/);
 assert.equal(c.calls.length,0);
 const previous=globalThis.document;
 globalThis.document={getElementById:()=>({close(){}})};
 try {await submit({reason:'explicit retry'}, {querySelector:()=>({checked:true})});}
 finally {if(previous===undefined)delete globalThis.document;else globalThis.document=previous;}
 assert.equal(c.calls[0].path,'/collections/request/retries');
 assert.equal(c.calls[0].revision,7);assert.equal(c.calls[0].body.confirmed,true);assert.ok(c.calls[0].key);
 assert.equal(c.state.workspace.collections[0].status,'dispatch_unknown');
 const after=c.walk(c.ui.page());assert.equal(after.find(n=>n?.tag==='button'&&n.children[0]==='推送与设备响应').attrs['aria-selected'],'true');assert.match(after.filter(n=>typeof n==='string').join(' '),/请求编号：request/);
 assert.equal(c.refreshes(),2);
});

test('internal acceptance and signed push success never imply device delivery or log receipt',()=>{
 const before=collectionProgress({status:'waiting_push'},{dispatchStatus:'accepted',pushStatus:'unknown'});
 assert.match(before[0][1],/已受理/);
 assert.match(before[1][1],/无.*回调/);
 const after=collectionProgress({status:'waiting_logs'},{dispatchStatus:'accepted',pushStatus:'succeeded'});
 assert.match(after[1][1],/渠道已受理/);
 assert.match(after[2][1],/无.*接收证据/);
 assert.match(after[3][1],/尚未关联/);
 assert.match(after[4][1],/不能判定回捞成功/);
});
test('unknown, failed, expired and cancelled requests keep distinct states and known push facts',()=>{
 assert.match(collectionStatusLabel('dispatch_unknown'),/未知/);
 assert.match(collectionStatusLabel('push_failed'),/失败/);
 assert.match(collectionStatusLabel('timed_out'),/超时/);
 const cancelled=collectionProgress({status:'cancelled'},{pushStatus:'succeeded'});
 assert.match(cancelled[1][1],/渠道已受理/);
 assert.match(cancelled[4][1],/设备仍可能上传/);
 assert.equal(collectionActive({status:'waiting_logs'}),true);
 assert.equal(collectionActive({status:'dispatch_unknown'}),true);
 assert.equal(collectionActive({status:'cancelled'}),false);
 assert.equal(collectionActive({status:'draft'}),false);
});
test('lost reply checks the durable operation and recovers without a second POST',async()=>{
 const calls=[];
 const api=async(path,options={})=>{
  calls.push({path,...options});
  if(options.method==='POST')throw new TypeError('network interruption');
  return {resourceId:'collection-a',result:{id:'collection-a',status:'waiting_push'}};
 };
 const actions=createCollectionMutations({api,scope:()=> 'project-a',newKey:()=> 'stable-key'});
 const result=await actions.submit('/collections',{draftId:'draft-a',draftRevision:1});
 assert.equal(result.id,'collection-a');
 assert.equal(calls.filter(c=>c.method==='POST').length,1);
 assert.match(calls[1].path,/\/operations\?key=stable-key/);
});
test('unresolved reply preserves a key for explicit retry, without automatic resend',async()=>{
 const calls=[];let fail=true;let keys=0;
 const api=async(path,options={})=>{
  calls.push({path,...options});
  if(!options.method)throw Object.assign(new Error('not found'),{status:404});
  if(fail)throw new TypeError('network interruption');
  return {id:'collection-a'};
 };
 const actions=createCollectionMutations({api,scope:()=> 'p',newKey:()=> 'key-'+(++keys)});
 await assert.rejects(actions.submit('/collections',{draftId:'d',draftRevision:1}),/结果未知/);
 assert.equal(calls.filter(c=>c.method==='POST').length,1);
 fail=false;
 await actions.submit('/collections',{draftId:'d',draftRevision:1});
 assert.deepEqual(calls.filter(c=>c.method==='POST').map(c=>c.key),['key-1','key-1']);
});
test('scope changes reject late results and never share mutation identities between projects',async()=>{
 let scope='p';let resolve;const keys=[];
 const actions=createCollectionMutations({api:async(p,o)=>{keys.push(o.key);return await new Promise(r=>{resolve=r;});},scope:()=>scope,newKey:()=>String(keys.length)});
 const first=actions.submit('/collections',{draftId:'d',draftRevision:1});scope='other';resolve({id:'old'});
 await assert.rejects(first,/项目或身份已切换/);
 const second=actions.submit('/collections',{draftId:'d',draftRevision:1});resolve({id:'new'});await second;
 assert.notEqual(keys[0],keys[1]);
});
test('known revision conflicts remain visible and never initiate reconciliation or another send',async()=>{
 let count=0;const conflict=Object.assign(new Error('revision changed'),{status:409});
 const actions=createCollectionMutations({api:async()=>{count++;throw conflict;},scope:()=> 'p',newKey:()=> 'key'});
 await assert.rejects(actions.submit('/collections/c/retries',{confirmed:true,reason:'manual retry'},3),e=>e===conflict);
 assert.equal(count,1);
});

test('wizard locks edits after uncertain submission and retries unchanged intent with the original key',async()=>{
 const oldDocument=globalThis.document,oldFormData=globalThis.FormData;let content,fail=true;const calls=[];
 const walk=n=>n&&typeof n==='object'?[n,...(n.children||[]).flatMap(walk)]:[];
 const el=(tag,attrs={},...children)=>({tag,attrs,children:children.flat(Infinity).filter(Boolean),get childNodes(){return this.children;},get textContent(){return this.children.filter(n=>typeof n==='string').join('');},append(...v){this.children.push(...v);},querySelectorAll(s){return walk(this).filter(n=>n.tag===s);},replaceChildren(...v){this.children=v;},addEventListener(){},querySelector(s){return walk(this).find(n=>s==='button[type=submit]'?n.attrs.type==='submit':n.attrs.name===s.match(/name=([^\]]+)/)?.[1])||null;}});
 const state={project:'p',token:'t',session:{role:'investigator'},workspace:{capabilities:{collections:{configured:true}},issues:[]}};
 globalThis.document={getElementById:()=>({close(){}})};
 globalThis.FormData=class{constructor(f){this.v=walk(f).filter(n=>n.attrs.name).map(n=>[n.attrs.name,n.attrs.value||'']);}[Symbol.iterator](){return this.v[Symbol.iterator]();}};
 const ui=createCollectionUI({el,button:(s,click)=>el('button',{click},s),field:(name,label,value)=>el('input',{name,value}),form:(fields,s,submit)=>el('form',{submit},fields,el('button',{type:'submit'},s)),note:s=>el('p',{},s),modal:(s,c)=>{content=c;},api:async(path,o={})=>{calls.push({path,...o});if(path==='/collection-drafts')return {id:'draft-a',revision:1};if(!o.method)throw Object.assign(Error('absent'),{status:404});if(fail)throw TypeError('lost');return {id:'request-a'};},refresh:async()=>{},getState:()=>state});
 const form=()=>walk(content).find(n=>n.tag==='form'),button=s=>walk(content).find(n=>n.tag==='button'&&n.children[0]===s);
 try{await ui.wizard();form().querySelector('[name=targetValue]').attrs.value='target-a';const originalForm=form();await form().attrs.submit({},form());await form().attrs.submit({},form());await assert.rejects(form().attrs.submit({},form()),/结果未知/);
  assert.equal(button('上一步').disabled||button('上一步').attrs.disabled,true);
  assert.equal(button('保存草稿并退出').disabled||button('保存草稿并退出').attrs.disabled,true);
  await assert.rejects(async()=>button('上一步').attrs.click(),/已锁定/);
  originalForm.querySelector('[name=targetValue]').attrs.value='target-b';
  await assert.rejects(originalForm.attrs.submit({},originalForm),/已锁定/);
  fail=false;await form().attrs.submit({},form());const posts=calls.filter(c=>c.path==='/collections');
  assert.equal(calls.filter(c=>c.path==='/collection-drafts').length,1);assert.deepEqual(posts.map(c=>c.body),[{draftId:'draft-a',draftRevision:1},{draftId:'draft-a',draftRevision:1}]);assert.equal(posts[0].key,posts[1].key);
 }finally{globalThis.document=oldDocument;globalThis.FormData=oldFormData;}
});

test('request history expansion survives polling, explicit closure and project switches',async()=>{
 const c=component(),history=()=>c.walk(c.ui.page()).find(n=>n?.tag==='details');
 const first=history();assert.equal(first.attrs.open,undefined);
 first.attrs.onToggle?.({currentTarget:{open:true}});
 assert.equal(history().attrs.open,true);
 history().attrs.onToggle({currentTarget:{open:false}});
 await c.walk(c.ui.page()).find(n=>n?.tag==='button'&&n.children[0]==='失败与重试').attrs.click();
 assert.equal(history().attrs.open,undefined);
 const old=history();c.state.project='another-project';
 assert.equal(history().attrs.open,undefined);
 old.attrs.onToggle({currentTarget:{open:true}});
 assert.equal(history().attrs.open,undefined);
 c.state.project='p';assert.equal(history().attrs.open,true);
});
