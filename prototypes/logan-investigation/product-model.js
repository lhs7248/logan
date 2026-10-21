(function(root){
'use strict';
const clone=x=>JSON.parse(JSON.stringify(x));
function captureScope(begin,end,profile){
 if(!begin||!end||!Number.isFinite(Date.parse(begin+'+08:00'))||!Number.isFinite(Date.parse(end+'+08:00'))||begin>=end)throw Error('请填写有效时间，结束须晚于开始');
 if(['unsupported','web-offline'].includes(profile))throw Error('当前设备不支持远程采集；可手动导入日志，或等待 Web 页面恢复');
 if(profile!=='day')return {begin,end,expanded:false};
 const b=begin.slice(0,10)+'T00:00';
 const e=end.endsWith('T00:00')?end:new Date(Date.parse(end.slice(0,10)+'T00:00Z')+86400000).toISOString().slice(0,10)+'T00:00';
 return {begin:b,end:e,expanded:b!==begin||e!==end};
}
function availability(i,e){return (e.kind==='source'&&i.product?.sourceDenied?'denied':null)||i.product?.unavailableTasks?.[e.task]||i.product?.availability?.[e.id]||'available';}
function token(i){return JSON.stringify({owner:i.owner,status:i.status,description:i.description,title:i.title,device:i.device,version:i.version,build:i.build,begin:i.begin,end:i.end,revision:i.product?.revision||0,context:i.investigation?.revision||0,fixes:i.product?.fixes||[],availability:i.product?.availability||{},unavailableTasks:i.product?.unavailableTasks||{},sourceDenied:!!i.product?.sourceDenied});}
function validateFix(v){if(!['build','scenario','expected','actual','actor'].every(k=>v[k]?.trim()))throw Error('请填写构建、场景、预期、实际观察和执行人');if(!['pass','fail','inconclusive'].includes(v.outcome))throw Error('请选择有效的修复验证结论');if(!v.evidence?.length)throw Error('至少关联一项可用证据；没有证据时先补充现场材料');}
function validateHandoff(i,d){
 if(!['调查中','待修复','待验证','已解决','重新调查'].includes(d.status))throw Error('请选择有效的工单状态');
 if(d.status==='重新调查'&&!['已解决','重新调查'].includes(i.status))throw Error('只有已解决的工单可重新调查');
 if(d.scopeToken!==token(i))throw Error('工单、证据或验证已变化。请重新预览并核对，编辑内容会保留。');
 if(!d.owner?.trim()||!d.summary?.trim()||!d.confirmed)throw Error('请填写负责人和调查结果，并核对预览内容');
 if(d.mode==='ai'&&!i.investigation?.rounds.some(r=>r.id===d.round&&r.status==='done'))throw Error('请选择已完成的 AI 轮次');
 if((d.evidence||[]).some(e=>availability(i,e)!=='available'))throw Error('草稿包含不可用证据，请恢复演练状态，或在人工更新中排除不可用证据');
 if(i.status==='已解决'&&d.status!=='重新调查')throw Error('已解决的工单只能重新调查，原结论保留');
 if(d.status==='已解决'){
  if(i.status!=='待验证')throw Error('请先保存为待验证，再记录并复核修复结果');
  const fix=i.product?.fixes?.at(-1);
  if(!fix||fix.outcome!=='pass')throw Error('关闭工单需要最新一条通过的修复验证，假设支持不能代替');
  if(!fix.evidence?.length||fix.evidence.some(e=>availability(i,e)!=='available'))throw Error('修复验证证据不可用，请补齐证据后重新验证');
 }
}
function commitHandoff(i,d){
 const existing=i.updates.find(u=>u.id===d.id);if(existing)return existing;
 validateHandoff(i,d);
 const update={id:d.id,time:new Date().toLocaleString('zh-CN'),status:d.status,owner:d.owner.trim(),summary:d.summary.trim(),verified:d.status==='已解决',evidence:(d.evidence||[]).map(e=>e.label+' / '+e.task),investigation:{...(clone(d.snapshot||{})),mode:d.mode,round:d.round||null,body:d.summary,evidence:clone(d.evidence||[]),verifications:clone(d.snapshot?.verifications||[]),fixVerifications:clone(i.product?.fixes||[])}};
 i.updates.unshift(update);i.owner=update.owner;i.status=d.status;i.verified=update.verified;i.product=i.product||{};i.product.revision=(i.product.revision||0)+1;return update;
}
function lateReceipt(r){if(r.events.some(e=>e.kind==='late-demo'))return false;r.events.push({kind:'late-demo',time:new Date().toLocaleTimeString('zh-CN'),text:'演练：终态后收到 SDK 回执，已记录迟到事件；不改变请求终态，不自动关联文件。'});return true;}
root.LoganProduct={captureScope,availability,token,validateFix,validateHandoff,commitHandoff,lateReceipt};
if(typeof module!=='undefined')module.exports=root.LoganProduct;
})(typeof globalThis!=='undefined'?globalThis:this);
