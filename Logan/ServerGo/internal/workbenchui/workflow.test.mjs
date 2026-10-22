import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {spawnSync} from 'node:child_process';
import {evidenceChanges} from './static/api.mjs';
import {preflightFiles,uploadRequest} from './static/upload.mjs';
import {createDraftAutosaver} from './static/drafts.mjs';

test('browser ES module parser accepts all embedded modules',()=>{
 for(const path of ['app.js','api.mjs','upload.mjs','drafts.mjs','collection.mjs']){
  const input=readFileSync(new URL('./static/'+path,import.meta.url),'utf8');
  const result=spawnSync(process.execPath,['--input-type=module','--check'],{input,encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
 }
});
test('preflight checks actual UTF-8 bytes, empty files and configured batch bounds',async()=>{
 const file=new File(['{"a":1}\ninvalid'], 'sample.jsonl');
 const result=await preflightFiles([file],{fileBytes:100,batchBytes:100,files:2},'jsonl');
 assert.equal(result.total,file.size);assert.match(result.files[0].warning,/格式错误/);
 await assert.rejects(preflightFiles([new File([''],'empty')]),/空文件/);
 await assert.rejects(preflightFiles([file],{fileBytes:2}),/单文件上限/);
 await assert.rejects(preflightFiles([file,file],{batchBytes:file.size}),/单批上限/);
 await assert.rejects(preflightFiles([new File([new Uint8Array([255,254])],'bad')]),/UTF-8/);
 await assert.rejects(preflightFiles([new File([new Uint8Array([31,139])],'compressed')]),/对应协议/);
 const protocol=await preflightFiles([new File([new Uint8Array([255,0])],'sdk')],{},'native');
 assert.match(protocol.files[0].warning,/服务端/);
});
function fakeXHR(){return {upload:{},headers:{},open(method,url){this.method=method;this.url=url;},setRequestHeader(key,value){this.headers[key]=value;},send(body){this.body=body;},abort(){this.onabort();}};}
test('upload progress uses actual transfer bytes and abort does not claim server cancellation',async()=>{
 const xhr=fakeXHR();const controller=new AbortController();const progress=[];
 const result=uploadRequest({body:'data',token:'secret',project:'p',key:'batch-key',signal:controller.signal,onProgress:p=>progress.push(p),xhrFactory:()=>xhr});
 xhr.upload.onprogress({loaded:12,total:20,lengthComputable:true});
 assert.deepEqual(progress,[{loaded:12,total:20}]);assert.equal(xhr.headers.Authorization,'Bearer secret');
 controller.abort();await assert.rejects(result,e=>e.name==='AbortError'&&e.message.includes('可能已收到'));
});
test('upload success requires a real successful server envelope',async()=>{
 const xhr=fakeXHR();const promise=uploadRequest({body:'data',token:'t',project:'p',key:'k',xhrFactory:()=>xhr});
 xhr.status=201;xhr.responseText=JSON.stringify({data:{id:'batch',assets:[{id:'a'}]}});xhr.onload();
 assert.deepEqual(await promise,{id:'batch',assets:[{id:'a'}]});
});
test('editable draft autosave serializes revisioned writes and preserves edits arriving during request',async()=>{
 let release;const first=new Promise(resolve=>release=resolve);const writes=[];
 const saver=createDraftAutosaver({delay:100000,create:async body=>{writes.push(body);await first;return {id:'draft',revision:1,...body};},patch:async(draft,body)=>{writes.push({revision:draft.revision,...body});return {id:'draft',revision:draft.revision+1,...body};}});
 saver.change({summary:'first'});const pending=saver.flush();saver.change({summary:'second'});release();await pending;
 assert.equal(writes.length,2);assert.equal(writes[1].revision,1);assert.equal(saver.state().draft.summary,'second');assert.equal(saver.state().dirty,false);saver.dispose();
});
test('draft conflict retains local text and refreshed server revision permits explicit retry',async()=>{
 let fail=true;const saver=createDraftAutosaver({delay:100000,draft:{id:'d',revision:1,summary:'saved'},create:async()=>{},patch:async(draft,body)=>{if(fail)throw Error('REVISION_CONFLICT');return {id:'d',revision:draft.revision+1,...body};}});
 saver.change({summary:'keep local'});await assert.rejects(saver.flush(),/CONFLICT/);assert.equal(saver.state().values.summary,'keep local');assert.equal(saver.state().dirty,true);
 saver.updateDraft({id:'d',revision:2,summary:'other tab'});assert.equal(saver.state().values.summary,'keep local');fail=false;await saver.flush();assert.equal(saver.state().draft.revision,3);saver.dispose();
});
test('evidence delta distinguishes changed identity, expiration, permission loss and explicit refutations',()=>{
 const previous=[{id:'same',contentHash:'old',originRef:{runId:'r1'}},{id:'removed'},{id:'verification-old',originRef:{verificationId:'v1'}}];
 const current=[{id:'same',contentHash:'new',originRef:{runId:'r2'}},{id:'expired',availability:'expired'},{id:'restricted',availability:'restricted'}];
 const delta=evidenceChanges(previous,current,[{id:'v1',purpose:'hypothesis',result:'refuted'},{id:'v2',purpose:'hypothesis',result:'refuted',hypothesisId:'h',hypothesisVersion:2}],0);
 assert.deepEqual(delta.changed,['same']);assert.deepEqual(delta.expired,['expired']);assert.deepEqual(delta.unavailable,['restricted']);assert.deepEqual(delta.contradicted,[{verificationId:'v2',hypothesisId:'h',hypothesisVersion:2}]);
 assert.equal(previous[0].contentHash,'old');
});
