// Association is deliberately limited to the versioned protocol, never inferred
// from ordinary log text. Returned events retain their server evidence identity.
const namespace='xlb.page-diagnostic';
const identity=value=>typeof value==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(value);
const eventNames=new Set(['page_open_started','bundle_load_started','bundle_load_succeeded','bundle_load_failed','rn_mounted','content_ready','page_interactive','render_failed','page_suspected_blank','page_slow','page_abandoned']);
export function pageDiagnostic(event){
 // Trust only normalized fields. Parsing raw content here would reclassify
 // malformed diagnostics that the strict server parser deliberately rejected.
 const value=event;
 if(value?.schema!==namespace||value.schemaVersion!==1||!identity(value.pageAttemptId)||!identity(value.pageName)||!eventNames.has(value.eventName))return null;
 return value;
}
const timestamp=(event,diagnostic)=>Number.isFinite(diagnostic.occurredAtMs)?diagnostic.occurredAtMs:Number.isFinite(Date.parse(event.time))?Date.parse(event.time):null;
export function groupPageAttempts(events=[],runId=''){
 const groups=new Map();
 for(const event of events){
  if(event.runId&&event.runId!==runId)continue;
  const d=pageDiagnostic(event);if(!d)continue;
  const key=JSON.stringify([runId,d.pageAttemptId]);
  if(!groups.has(key))groups.set(key,{key,runId,pageAttemptId:d.pageAttemptId,pageName:d.pageName,retryOf:d.retryOf||'',events:[],slow:false,testInjected:false,scenarios:[],gaps:[]});
  const group=groups.get(key);group.events.push(event);group.slow ||=d.eventName==='page_slow';group.testInjected ||=d.testInjected===true;
  if(d.scenario&&!group.scenarios.includes(d.scenario))group.scenarios.push(d.scenario);
 }
 for(const group of groups.values()){
  group.events.sort((left,right)=>{
   const a=pageDiagnostic(left),b=pageDiagnostic(right),at=timestamp(left,a),bt=timestamp(right,b);
   if(at!==null&&bt!==null&&at!==bt)return at-bt;
   // Source identifies the clock domain in this protocol. Never subtract clocks
   // from the RN runtime and the native process, or from different attempts.
   if(at===bt&&a.source===b.source&&Number.isFinite(a.monotonicMs)&&Number.isFinite(b.monotonicMs))return a.monotonicMs-b.monotonicMs;
   return 0;
  });
  const diagnostics=group.events.map(pageDiagnostic),names=new Set(diagnostics.map(d=>d.eventName));
  if(!names.has('page_open_started'))group.gaps.push('缺少 page_open_started 开始证据');
  if(!names.has('content_ready'))group.gaps.push('缺少 content_ready 业务内容就绪证据');
  if(!names.has('page_interactive'))group.gaps.push('缺少 page_interactive 可交互证据');
  // Opening has one terminal result. Preserve late callbacks as evidence,
  // but never let them revive an attempt that already failed or was abandoned.
  // A post-opening error is observable separately from the opening result.
  group.status='incomplete';group.hasErrors=false;
  let ready=false,interactive=false,terminal=false;
  for(const diagnostic of diagnostics){
   const name=diagnostic.eventName,isFailure=name==='bundle_load_failed'||name==='render_failed';
   group.hasErrors ||=isFailure;
   if(terminal)continue;
   if(isFailure){group.status='failed';terminal=true;}
   else if(name==='page_abandoned'){group.status='abandoned';terminal=true;}
   else {
    if(name==='content_ready')ready=true;
    if(name==='page_interactive')interactive=true;
    if(name==='page_suspected_blank')group.status='suspected';
    if(ready&&interactive){group.status='succeeded';terminal=true;}
   }
  }
  const durations=diagnostics.map(d=>d.durationMs).filter(n=>Number.isFinite(n)&&n>=0);group.durationMs=durations.length?Math.max(...durations):null;
 }
 return [...groups.values()];
}
export function filterPageAttempt(events=[],runId='',pageAttemptId=''){
 if(!pageAttemptId)return events;
 return events.filter(event=>(!event.runId||event.runId===runId)&&pageDiagnostic(event)?.pageAttemptId===pageAttemptId);
}
