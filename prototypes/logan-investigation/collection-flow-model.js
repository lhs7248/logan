(function(root){
'use strict';
const clone=x=>JSON.parse(JSON.stringify(x));
const targets=[
 {id:'native-a17',project:'Logan 演示项目',app:'Native 演示应用',environment:'隔离演练',platform:'Native',device:'demo-A17',instance:'install-a17',latestVersion:'3.8.2',latestBuild:'38108',sdk:'Native 合成 SDK',profile:'day',kinds:['混合日志'],lastSeen:'2026-10-21 09:30 +08:00',capabilityAt:'2026-10-21 09:30 +08:00',source:'内置合成目标目录',sampleDay:'2026-10-19'},
 {id:'web-active',project:'Logan 演示项目',app:'Web 演示应用',environment:'隔离演练',platform:'Web',device:'browser-demo-01',instance:'session-web-01',latestVersion:'web-3.8.1',latestBuild:'web-38107',sdk:'Web 合成 SDK',profile:'precise',kinds:['应用日志','网络日志'],lastSeen:'2026-10-21 09:30 +08:00',capabilityAt:'2026-10-21 09:30 +08:00',source:'内置合成活动页面',sampleDay:'2026-10-20'},
 {id:'web-offline',project:'Logan 演示项目',app:'Web 演示应用',environment:'隔离演练',platform:'Web',device:'browser-demo-offline',instance:'session-offline',latestVersion:null,latestBuild:null,sdk:'Web 合成 SDK',profile:'web-offline',kinds:[],lastSeen:'未知 · 页面未运行',capabilityAt:'未知',source:'内置不可送达样例',sampleDay:'2026-10-20'}
];
const pushBindings={
 'native-a17':{id:'binding-native-a17',channel:'应用推送通道（合成）',recipient:'push-install-a17',ready:true,source:'应用 / 环境下的推送绑定样例'},
 'web-active':{id:'binding-web-01',channel:'Web 业务消息通道（合成）',recipient:'push-session-web-01',ready:true,source:'业务消息订阅样例；不代表浏览器原生 Web Push'},
 'web-offline':{id:null,channel:'未绑定可用消息通道',recipient:null,ready:false,source:'仅有历史会话，不能据此推送'}
};
function pushBinding(t){return clone(pushBindings[t?.id]||{ready:false,channel:'未知',recipient:null,source:'未找到推送绑定'});}
function requirePush(t){const binding=pushBinding(t);if(!binding.ready||!binding.id||!binding.recipient)throw Error('没有可用的推送绑定，不能发送回捞指令；请保存草稿或上传已有日志文件');return binding;}
function preparePush(r){const t=r.product.target,binding=requirePush(t);return {id:'PUSH-'+r.id,mode:'local-synthetic',binding,status:'待提交',submittedAt:null,expiresAt:null,receiptId:null,command:{type:'retrieve_logs',requestId:r.id,device:r.device,instance:t.instance,app:t.app,environment:t.environment,begin:r.product.scope.begin,end:r.product.scope.end,timeZone:'Asia/Shanghai',types:r.types.slice(),budgetMiB:Number(r.budget),ttlMinutes:Number(r.ttl)}};}
function beginPush(r){const p=r.product?.push;if(!p||r.step!==0||['已取消','推送失败'].includes(r.status)||p.submittedAt)return;p.submittedAt=new Date().toISOString();p.expiresAt=new Date(Date.parse(p.submittedAt)+Number(r.ttl)*60000).toISOString();p.status='提交中';r.events.push({time:new Date().toLocaleTimeString('zh-CN'),text:'模拟提交回捞推送 '+p.id+' → '+p.binding.channel+' / '+p.binding.recipient+'；携带指令 '+r.id});}
function recordPush(r){const p=r.product?.push;if(!p?.submittedAt)return;if(r.status==='推送失败'){p.status='通道拒绝';p.receiptId=null;}else if(r.step>=1){p.status='通道已接受';p.receiptId='receipt-'+p.id;}}
function dispatch(r,M){if(r.step!==0||M.terminal.includes(r.status)||r.product?.push?.submittedAt)return false;beginPush(r);M.advance(r);recordPush(r);return true;}
const target=id=>targets.find(t=>t.id===id);
const all=s=>(s.collectionRequests||[]).concat(s.issues.flatMap(i=>i.requests));
function find(s,id){return all(s).find(r=>r.id===id);}
function create(s,d,M,P){
 const t=target(d.targetId);if(!t)throw Error('请选择目录中可识别的目标；未知设备可保存草稿或手动导入');
 requirePush(t);const scope=P.captureScope(d.begin,d.end,t.profile),i=d.issueId?s.issues.find(i=>i.id===d.issueId):null;
 if(d.issueId&&!i)throw Error('关联工单不存在');if(i?.status==='已解决')throw Error('已解决工单请先重新调查，或选择暂不关联');
 if(scope.expanded&&!d.expanded)throw Error('请确认扩大至整日的预计采集范围');
 const kinds=t.profile==='day'?['混合日志']:(d.types||[]).filter(k=>t.kinds.includes(k));if(!kinds.length)throw Error('至少选择一种目标支持的日志');
 const duplicate=all(s).find(r=>!M.terminal.includes(r.status)&&r.product?.target?.id===t.id&&r.begin===d.begin&&r.end===d.end&&r.types.slice().sort().join()===kinds.slice().sort().join());
 if(duplicate&&!d.force)throw Error('已有相同目标与范围的进行中请求 '+duplicate.id+'，请返回列表查看或明确新建');
 const temp={requests:[],version:null,build:null},r=M.create(s,temp,{device:t.device,platform:t.platform,begin:d.begin,end:d.end,types:kinds,budget:d.budget||'32',ttl:d.ttl||'30',notify:d.notify!==false}).request;
 r.version=null;r.build=null;r.issueId=i?.id||null;
 r.product={flowVersion:2,stableKey:'collection/'+r.id,title:d.title?.trim()||'故障日志回捞',target:clone(t),profile:t.profile,scope,expected:i?{issueId:i.id,device:i.device,version:i.version||null,build:i.build||null}:null,requestedTypes:kinds,observed:null};
 r.product.push=preparePush(r);
 if(duplicate)r.similarTo=duplicate.id;
 (i?i.requests:(s.collectionRequests||(s.collectionRequests=[]))).push(r);return r;
}
function associate(s,id,issueId){const r=find(s,id),i=s.issues.find(i=>i.id===issueId);if(!r||!i)throw Error('请求或工单不存在');if(r.issueId===i.id)return r;if(r.issueId)throw Error('此请求已关联工单，不自动改绑');if(i.status==='已解决')throw Error('已解决工单须先重新调查');s.collectionRequests=s.collectionRequests.filter(x=>x.id!==id);i.requests.push(r);r.issueId=i.id;r.events.push({time:new Date().toLocaleTimeString('zh-CN'),text:'人工关联工单 '+i.id+'；原采集身份及内容保持不变'});return r;}
function observe(r,data){r.product.observed=clone(data);r.version=data.version||null;r.build=data.build||null;}
function coverage(r){const rows=r.task?(r.product?.observed?.records||[]).filter(row=>r.files.some(f=>f.status==='可查'&&(!f.name||f.name.startsWith('混合日志')||f.name.startsWith(/Network/.test(row.module)?'网络日志':'应用日志')))):[],times=rows.map(x=>x.time).filter(Boolean).sort();return {count:rows.length,begin:times[0]||null,end:times.at(-1)||null,continuous:false,label:rows.length?'已有可读事件；连续覆盖尚未确认':'尚无可读事件；覆盖未知'};}
root.LoganCollection={targets,target,all,find,create,associate,observe,coverage,pushBinding,requirePush,preparePush,beginPush,recordPush,dispatch};if(typeof module!=='undefined')module.exports=root.LoganCollection;
})(typeof globalThis!=='undefined'?globalThis:this);
