(function(root){
'use strict';
const stages=['请求创建','推送接受','设备送达','设备确认','文件上传','解析索引'];
const terminal=['完成','部分成功','推送失败','设备响应超时','无可用日志','设备不支持','上传失败','解析失败','已取消'];
const now=()=>new Date().toLocaleTimeString('zh-CN',{hour12:false});
function initial(){return {version:2,active:'ISS-1042',issues:[{id:'ISS-1042',title:'登录后持续转圈',device:'demo-A17',version:'3.8.1',build:'38107',priority:'P1',owner:'待分配',status:'调查中',description:'10:42 点击登录后持续转圈。预期登录成功或显示失败，实际一直等待。',begin:'2026-10-19T10:40',end:'2026-10-19T10:45',evidence:[],requests:[],analysis:null,updates:[],verified:false}],sequence:208};}
function event(r,text){r.events.push({time:now(),text});}
function create(s,i,form,parent){
const related=parent||null;const duplicate=i.requests.find(r=>!terminal.includes(r.status)&&r.device===form.device&&r.begin===form.begin&&r.end===form.end&&r.types.join()===form.types.join());if(duplicate)return {request:duplicate,duplicate:true};
const r={id:'COL-'+s.sequence++,createdAt:new Date().toISOString(),parent:related,device:form.device,version:form.version||i.version,build:form.build||i.build,platform:form.platform||'Native',begin:form.begin,end:form.end,types:form.types,budget:form.budget,ttl:form.ttl,notify:form.notify,step:0,status:'待推送',scenario:'success',events:[],files:[],task:null};event(r,related?'创建关联重试，仅处理未完成文件；原请求保留':'回捞请求已创建，等待发送采集指令');i.requests.push(r);return {request:r,duplicate:false};}
function advance(r){if(terminal.includes(r.status))return;
if(r.step===0&&r.scenario==='pushfail'){r.status='推送失败';event(r,'推送通道拒绝：设备令牌已失效；设备未收到请求');return;}
r.step++;
const texts=['','推送服务已接受 · push-'+r.id+'；不代表设备收到','设备送达回执已收到；尚未同意采集','SDK 已确认采集指令；校验设备与日志时间范围','文件上传结束，开始完整性检查','索引已就绪'];
if(r.step===2&&r.scenario==='timeout'){r.status='设备响应超时';event(r,'请求有效期结束，未收到设备送达或 SDK 确认；在线状态未知');return;}
if(r.step===3&&r.scenario==='unsupported'){r.status='设备不支持';event(r,'SDK 明确返回不支持指定日志种类；未上传文件');return;}
if(r.step===3&&r.scenario==='empty'){r.status='无可用日志';event(r,'SDK 已响应，所选时间范围没有可回捞文件');return;}
if(r.step===4){const fail=['partial','uploadfail'].includes(r.scenario);r.files=r.types.map((name,n)=>({name:name+'.log',status:fail&&(n===r.types.length-1)?'上传失败':'已上传',size:n? '1.2 MiB':'4.6 MiB'}));if(r.scenario==='uploadfail'){r.files.forEach(f=>f.status='上传失败');r.status='上传失败';event(r,'文件上传失败；成功接收的对象未发布为可查任务');return;}}
if(r.step===5){if(r.scenario==='parsefail'){r.status='解析失败';r.files.forEach(f=>f.status='解析失败');event(r,'完整性检查失败，未发布索引；上传文件保留用于诊断');return;}r.files.forEach(f=>{if(f.status==='已上传')f.status='可查'});if(!r.files.some(f=>f.status==='可查')){r.status='上传失败';event(r,'没有成功文件，不创建可查任务');return;}r.task='TASK-'+r.id.slice(4);r.status=r.files.some(f=>f.status==='上传失败')?'部分成功':'完成';event(r,'发布 '+r.task+'，可查文件自动关联工单；失败文件不计为成功');return;}
r.status=['待推送','通道已接受','设备已送达','采集中','解析中'][r.step];event(r,texts[r.step]);}
function retry(s,i,r){const types=r.files.length?r.files.filter(f=>f.status!=='可查').map(f=>f.name.replace('.log','')):r.types;if(!types.length)return null;return create(s,i,{...r,types},r.id);}
function tasks(i){return i.requests.filter(r=>r.task).map(r=>({id:r.task,request:r.id,partial:r.status==='部分成功',types:r.files.filter(f=>f.status==='可查').map(f=>f.name.replace('.log','')),device:r.device,version:r.version,build:r.build,platform:r.platform||'Native',begin:r.begin,end:r.end,createdAt:r.createdAt||''})).concat((i.logAssets||[]).flatMap(a=>a.runs.filter(r=>r.task).map(r=>({id:r.task,request:a.id+' / '+r.id,partial:r.status==='部分成功',types:[a.platform+' 日志'],device:a.device,version:a.version,build:a.build,platform:a.platform,records:r.records,begin:a.begin,end:a.end,createdAt:r.createdAt||a.createdAt||'',assetId:a.id,runId:r.id})))).sort((a,b)=>(Date.parse(a.createdAt)||0)-(Date.parse(b.createdAt)||0));}
function advanceAndNotify(r){const before=r.status;advance(r);if(before!==r.status&&terminal.includes(r.status)&&r.notify&&r.status!=='已取消')event(r,'生成本地站内通知：'+r.status+'（未发送外部消息）');}
root.LoganModel={initial,create,advance:advanceAndNotify,retry,tasks,terminal,stages};if(typeof module!=='undefined')module.exports=root.LoganModel;
})(typeof globalThis!=='undefined'?globalThis:this);
