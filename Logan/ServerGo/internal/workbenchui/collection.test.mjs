import test from 'node:test';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
import {validateCollectionStep,collectionPayload,collectionTargetLabels,collectionPatchPayload} from './static/collection.mjs';
test('push wizard uses explicit target parameters and confirms account/alias binding',()=>{
 assert.deepEqual(Object.keys(collectionTargetLabels),['device','account','alias']);
 assert.throws(()=>validateCollectionStep({targetType:'device',targetValue:''},1),/deviceId/);
 assert.throws(()=>validateCollectionStep({targetType:'tag',targetValue:'x'},1),/标签/);
 assert.throws(()=>validateCollectionStep({targetType:'account',targetValue:'user',bindingConfirmed:false},1),/绑定/);
 assert.doesNotThrow(()=>validateCollectionStep({targetType:'alias',targetValue:'synthetic-alias',bindingConfirmed:true},1));
});
test('unsent draft payload preserves unknown identity and optional window without manufacturing commands',()=>{
 const p=collectionPayload({targetType:'device',targetValue:'explicit-aliyun-id',device:'ordinary-device',command:'invented',status:'completed',begin:'2026-10-22T09:00',end:'2026-10-22T09:05'});
 assert.equal(p.app,'com.hxl.xlb.test');assert.equal(p.targetValue,'explicit-aliyun-id');assert.equal(p.begin,'2026-10-22T09:00:00+08:00');assert.equal(p.end,'2026-10-22T09:05:00+08:00');
 assert.equal(p.command,undefined);assert.equal(p.device,undefined);assert.equal(p.status,undefined);
 const blank=collectionPayload({});assert.equal(blank.targetValue,'');assert.equal(blank.issueId,'');
 assert.throws(()=>validateCollectionStep({targetType:'device',targetValue:'id',begin:'2026-10-22T09:00',end:''},2),/完整/);
});

test('editing draft purpose preserves an unchanged masked target without submitting the mask',()=>{
 const saved={targetType:'alias',targetValue:'[REDACTED:phone]',bindingConfirmed:true};
 const patch=collectionPatchPayload({...saved,title:'补充应用日志'},saved);
 assert.equal(patch.targetValue,undefined);assert.equal(patch.bindingConfirmed,undefined);assert.equal(patch.title,'补充应用日志');
 assert.equal(collectionPatchPayload({...saved,targetValue:'new-explicit-alias'},saved).targetValue,'new-explicit-alias');
 assert.equal(collectionPatchPayload({...saved,targetType:'device',targetValue:''},saved).targetValue,'');
});

test('a bound saved masked target may continue editing without treating a changed mask as a target',()=>{
 const saved={id:'draft',targetType:'alias',targetValue:'[REDACTED_PHONE]',bindingConfirmed:true};
 assert.doesNotThrow(()=>validateCollectionStep({...saved,title:'只改用途'},1,saved));
 assert.doesNotThrow(()=>validateCollectionStep({...saved,title:'只改用途'},3,saved));
 assert.throws(()=>validateCollectionStep({...saved},1),/目标/);
 assert.throws(()=>validateCollectionStep({...saved,targetType:'account'},1,saved),/目标/);
 assert.throws(()=>validateCollectionStep({...saved,targetValue:'[REDACTED_OTHER]'},1,saved),/目标/);
 assert.throws(()=>validateCollectionStep({...saved,bindingConfirmed:false},1,saved),/目标|绑定/);
});

test('manual import from a retrieval draft opens the linked investigation before upload',async()=>{
 const source=readFileSync(new URL('./static/collection.mjs',import.meta.url),'utf8'),start=source.indexOf(' function detail('),end=source.indexOf('\n function chain(',start);
 const state={issueId:'old',runId:'old-run',source:{id:'old-fragment'}};let uploaded;
 const el=(tag,attrs={},...children)=>({tag,attrs,children:children.flat(Infinity)});
 const c={state:()=>state,app:'test',collectionTargetLabels,el,button:(title,click)=>({title,click}),note:value=>el('p',{},value),pill:value=>value,chain:()=>null,openIssue:async id=>{state.issueId=id;state.runId='';state.source=null;},upload:()=>{uploaded={...state};}};
 vm.createContext(c);vm.runInContext(source.slice(start,end),c);
 const nodes=n=>n&&typeof n==='object'?[n,...(n.children||[]).flatMap(nodes)]:[];
 await nodes(c.detail({id:'d',issueId:'linked',targetType:'device'})).find(n=>n.title==='手动导入补充日志').click();
 assert.equal(uploaded.issueId,'linked');assert.equal(uploaded.runId,'');assert.equal(uploaded.source,null);
});
