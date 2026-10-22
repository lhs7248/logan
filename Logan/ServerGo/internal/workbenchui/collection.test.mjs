import test from 'node:test';
import assert from 'node:assert/strict';
import {validateCollectionStep,collectionPayload,collectionTargetLabels} from './static/collection.mjs';
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
