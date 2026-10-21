const test=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),D=require('./desk-model');
function setup(){
 const i={id:'I1',requests:[],investigation:{rounds:[]}},j={id:'I2',requests:[],investigation:{rounds:[]}};
 const a={key:'I1/C1',issue:i,request:{id:'C1',task:'T1',files:[{status:'可查'}]}},b={key:'I1/C2',issue:i,request:{id:'C2',task:'T2',files:[{status:'可查'}]}},c={key:'I2/C3',issue:j,request:{id:'C3',task:'T3',files:[{status:'可查'}]}};
 const src=fs.readFileSync(__dirname+'/desk.js','utf8').split('// Respect older routes')[0];
 const ctx={LoganDesk:D,state:{issues:[i,j],active:'I1'},views:{issues:()=>''},workUI:{logKey:a.key},page:'workbench',taskId:'T1',requestId:'C1',draftHandoff:null,selected:4,query:'',level:'全部',contextMode:false,sourceMode:'matched',issueDetail:true,location:{hash:'workbench'},M:{tasks:()=>[]},save:()=>{},persistWork:()=>{},fullLogSources:()=>[a,b,c],collectionSources:()=>[a,b,c],issue:()=>ctx.state.active==='I1'?i:j,currentTask:()=>({id:ctx.taskId}),persistHandoff:()=>{ctx.persistCount++},persistCount:0,go:r=>{ctx.page=r},selectAssetRun:()=>{},saveChatTicket:()=>{},submitInlineCollection:()=>{},closeModal:()=>{},runInlineCollection:()=>{},retryInlineCollection:()=>{},renderLogs:()=>{},render:()=>{}};
 for(const m of src.matchAll(/^([a-zA-Z]\w*)=function/gm))if(!(m[1] in ctx))ctx[m[1]]=()=>{};
 vm.createContext(ctx);vm.runInContext(src,ctx);vm.runInContext('render=()=>{};deskSave=()=>{};',ctx);return ctx;
}
test('历史源码引用定位原任务而不是正在阅读的新批次',()=>{const c=setup();vm.runInContext("desk.issueKeys.I1='I1/C2';taskId='T1';go('source')",c);assert.equal(c.deskSource().key,'I1/C1');assert.equal(c.page,'workbench');});
test('查看旧日志引用保持调查页面和精确任务',()=>{const c=setup();vm.runInContext("desk.issueKeys.I1='I1/C2';taskId='T1';go('logs')",c);assert.equal(c.deskSource().request.task,'T1');assert.equal(c.page,'workbench');});
test('补采创建后回到同一调查且不替换当前日志',()=>{const c=setup();vm.runInContext("desk.issueKeys.I1='I1/C1';deskCollectOrigin='workbench';go('collectioncenter')",c);assert.equal(c.page,'workbench');assert.equal(c.deskSource().request.task,'T1');assert.equal(vm.runInContext('desk.tool',c),'collect');});
test('保存完成后不重新生成已删除的回写草稿',()=>{const c=setup();vm.runInContext("deskSaveOrigin='workbench';draftHandoff={round:'R1'};go('issues')",c);assert.equal(c.persistCount,0);assert.equal(c.draftHandoff,null);assert.equal(vm.runInContext('desk.tool',c),'result');});
test('跨工单调查不因上一个工单日志选择而串场',()=>{const c=setup();vm.runInContext("desk.issueKeys.I2='I1/C1';state.active='I2'",c);assert.equal(c.deskSource().request.task,'T3');});
