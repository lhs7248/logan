(function(root){
'use strict';
const clone=x=>JSON.parse(JSON.stringify(x));
const clock=()=>new Date().toLocaleTimeString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false});
function add(s,e){if(s.context.some(x=>x.id===e.id))return false;s.context.push(clone(e));s.revision++;return true;}
function start(s,question){question=question.trim();if(!question)throw Error('请输入要排查的问题');if(s.rounds.some(r=>r.status==='running'))throw Error('当前正在生成，请先停止或等待完成');const previous=s.rounds.filter(r=>r.status==='done').at(-1);let r={id:'R'+(s.rounds.length+1),question,previous:previous?.id||null,snapshot:clone(s.context),previousSnapshot:clone(previous?.snapshot||[]),previousVerifications:clone(previous?.verifications||[]),revision:s.revision,verifications:clone(s.hypotheses.flatMap(h=>(h.history||[]).map(v=>({...v,hypothesis:h.id,title:h.title})))),status:'running',step:0,time:clock()};s.rounds.push(r);return r;}
function stop(s,id){let r=s.rounds.find(r=>r.id===id);if(r?.status==='running')r.status='canceled';}
// Only structured, configured sample evidence participates in the local simulation.
// Imported or pasted material remains available for human review and historical citations.
function requestKey(e){return e.requestId||String(e.text||'').match(/requestId=([^;\s]+)/)?.[1]||null;}
function same(a,b,keys){return keys.every(k=>a[k]!=null&&a[k]!==''&&a[k]===b[k]);}
function sameRequest(a,b){return !!requestKey(a)&&requestKey(a)===requestKey(b)&&same(a,b,['device','platform','version','build','day']);}
function sourceFor(l,s){return l.versionOrigin!=='issue-hint'&&s.versionOrigin!=='issue-hint'&&s.mode==='matched'&&s.sourceVerified!==false&&same(l,s,['task','device','platform','version','build','file','line']);}
function deltaFor(r){const previous=r.previousSnapshot||[],current=r.snapshot;return {
 previous:r.previous||null,
 added:current.filter(e=>!previous.some(p=>p.id===e.id)),
 removed:previous.filter(e=>!current.some(p=>p.id===e.id)),
 changed:current.filter(e=>previous.some(p=>p.id===e.id&&JSON.stringify(p)!==JSON.stringify(e))),
 verifications:(r.verifications||[]).filter(v=>!(r.previousVerifications||[]).some(p=>p.id===v.id&&p.hypothesis===v.hypothesis))
};}
function reply(r){
 const eligible=e=>!e.manual&&e.scene!=='generic',logs=r.snapshot.filter(e=>e.kind==='log'&&eligible(e)),sources=r.snapshot.filter(e=>e.kind==='source'&&eligible(e));
 const states=logs.filter(e=>/\bloading\s*=\s*true\b/i.test(e.text)),timeouts=logs.filter(e=>/\btimeout\b|请求超时/i.test(e.text)),switches=logs.filter(e=>/网络状态切换|浏览器切换到离线/.test(e.text));
 const facts=[],inferences=[],conflicts=[],gaps=[],proposals=[],delta=deltaFor(r);
 const claim=(text,evidence)=>({text,evidenceIds:evidence.map(e=>e.id)});
 const last=(r.verifications||[]).filter(v=>v.hypothesis==='H1').at(-1),network=(r.verifications||[]).filter(v=>v.hypothesis==='H2').at(-1);
 const intent=/总结|工单|结论/.test(r.question)?'summary':/验证|下一步|怎么查|如何查/.test(r.question)?'verify':/网络|超时|服务端/.test(r.question)?'network':'overview';
 const base={intent,codeConfirmed:false,sourceLinked:false,requestLinked:false,facts,inferences,conflicts,gaps,proposals,delta,citations:[]};
 if(r.snapshot.length&&r.snapshot.every(e=>!eligible(e)))return {...base,intent:'clarify',body:'已收到本地导入材料。当前原型只解析、检索和引用这些文件，不理解任意日志或源码，不能把导入记录套入固定登录诊断。',unknown:'请人工核对现象、时间、版本和请求；导入材料未参与预设归因。',next:'依据原始记录提出可验证假设，并记录人工验证。'};
 if(/支付|扣款|订单|崩溃|闪退/.test(r.question)||(!logs.length&&!/登录|转圈|loading|源码|代码|H[12]/i.test(r.question)))return {...base,intent:'clarify',body:'当前本地模拟仅覆盖登录超时与加载状态场景，不覆盖此问题。材料会被保存，但不会据此生成真实诊断。',unknown:'问题超出演示场景，不能套用登录假设。',next:'先明确现象与调查范围，再收集证据。'};
 timeouts.forEach(e=>facts.push(claim('该事件记录请求超时；不等于服务端故障。',[e])));
 states.forEach(e=>facts.push(claim('该事件记录 loading=true；只代表记录时刻，不证明之后持续未恢复。',[e])));
 switches.forEach(e=>facts.push(claim('记录了网络状态切换；时间相近不能证明导致请求超时。',[e])));
 const pairs=states.flatMap(l=>sources.filter(s=>sourceFor(l,s)).map(s=>({l,s})));
 base.sourceLinked=pairs.length>0;
 let missingReset=false;
 for(const {l,s} of pairs){
  // This deliberately does not parse arbitrary programs or infer control flow.
  const snippet=String(s.text).replace(/\/\/[^\n]*/g,'');
  const tail=snippet.split(/\bcatch\b/).slice(1).join('catch');
  if(/loading\s*=\s*false|setLoading\s*\(\s*false|\bfinally\b|\bdefer\b/.test(tail)||/\bdefer\b/.test(snippet)){
   facts.push(claim('同位置源码片段包含复位或清理逻辑，不能按“失败分支缺少复位”解释。',[s]));
   conflicts.push(claim('状态日志与源码复位逻辑需要核对执行顺序、实际发布映射和 UI 绑定。',[l,s]));
  }else if(tail&&/logger\.error/.test(tail)){
   missingReset=true;facts.push(claim('对应失败分支的版本匹配源码样例片段未展示状态复位；片段以外是否清理仍未知。',[s]));
   inferences.push(claim('H1 候选：失败路径可能没有结束加载。仅凭日志时刻和局部代码不能确认完整运行路径。',[l,s]));
  }else gaps.push('源码片段不足以检查完整失败路径，不能仅凭版本匹配标签判断缺陷。');
 }
 if(!states.length)gaps.push('缺少失败后的应用状态日志；需要同设备、构建、故障时间和 requestId 的 loading 与 UI 状态。');
 if(states.length&&!pairs.length)gaps.push('缺少与状态事件同任务、设备、构建、文件和行号的已匹配源码；候选或旧证据的缺失元数据尚未验证。');
 if(!timeouts.length)gaps.push('缺少请求超时记录，网络切换本身不能证明请求失败。');
 base.requestLinked=timeouts.some(t=>states.some(l=>sameRequest(t,l)));
 if(timeouts.length&&states.length){
  if(base.requestLinked){const t=timeouts.find(t=>states.some(l=>sameRequest(t,l))),l=states.find(l=>sameRequest(t,l));inferences.push(claim('超时和加载状态有同设备、平台、构建、日期与 requestId，可作为同一请求的关联线索；超时起因与 UI 不退出仍须分开验证。',[t,l]));}
  else gaps.push('超时与加载状态尚未核对为同一请求：检查设备、平台、版本、日期和 requestId，不能拼接成因果链。');
 }
 const linkedNetwork=switches.find(n=>timeouts.some(t=>sameRequest(n,t)));
 if(switches.length){
  if(linkedNetwork){const t=timeouts.find(t=>sameRequest(linkedNetwork,t));inferences.push(claim('H2 候选：网络切换可能影响请求；需要切换/不切换的对照复现与请求阶段数据。',[linkedNetwork,t]));proposals.push({id:'H2',title:'网络切换是否影响此次请求超时',evidenceIds:[linkedNetwork.id,t.id]});}
  else gaps.push('网络切换记录没有与超时配对的完整请求身份；补采请求阶段、连接状态或同 requestId 的服务端记录。');
 }
 if(states.length){
  proposals.push({id:'H1',title:'登录失败路径是否恢复加载状态',evidenceIds:[...states,...pairs.map(p=>p.s)].map(e=>e.id)});
  if(!missingReset&&!conflicts.length)inferences.push(claim('H1 待检验：需要观察失败后是否最终复位，并区分状态变量与实际 UI。',states));
 }
 let body=states.length?'日志记录了 loading=true，但持续转圈的根因尚未确认。':'当前证据不足，无法解释持续转圈。';
 if(missingReset)body+=' 同位置源码片段未展示失败后的复位，形成待复现的 H1 候选。';
 else if(states.length&&!pairs.length)body+=' 缺少对应已匹配源码，尚未验证代码解释。';
 if(switches.length)body+=' 网络切换目前只作为现象记录，未证明它导致超时。';
 if(last){
  const observation=last.observation.replace(/[。.!！]+$/,'');
  if(last.outcome==='refuted'){
   body='最新人工验证反驳 H1：'+observation+'。原日志事实保留，但不能继续把 H1 作为已支持原因。';
   for(let n=inferences.length-1;n>=0;n--)if(inferences[n].text.startsWith('H1'))inferences.splice(n,1);
   conflicts.push({text:'人工反驳与此前 H1 解释冲突，需核对复现场景及 UI 绑定。验证记录：'+last.id,evidenceIds:(last.evidenceIds||[]).filter(id=>r.snapshot.some(e=>e.id===id))});
  }else body+=' 最新人工验证'+(last.outcome==='supported'?'记录支持 H1：'+observation:'仍无法确定 H1')+'；这是人工观察，不是 AI 实际执行或自动确认根因。';
 }
 if(network){
  body+=' H2 最新人工记录：'+({supported:'支持',refuted:'反驳',inconclusive:'无法确定'}[network.outcome])+'。';
  if(network.outcome==='refuted'){
   for(let n=inferences.length-1;n>=0;n--)if(inferences[n].text.startsWith('H2'))inferences.splice(n,1);
   conflicts.push({text:'人工反驳 H2：'+network.observation+'；网络切换事实仍保留，但不再将它作为已支持的超时解释。',evidenceIds:(network.evidenceIds||[]).filter(id=>r.snapshot.some(e=>e.id===id))});
  }
 }
 gaps.push('尚未完成真实设备复现或修复验证；本地规则无法确认真实根因。');
 if(r.snapshot.some(e=>!eligible(e)))gaps.push('导入或手动材料仅保留和引用，未参与预设场景归因。');
 const ids=new Set([...facts,...inferences,...conflicts].flatMap(c=>c.evidenceIds));base.citations=r.snapshot.filter(e=>ids.has(e.id));
 return {...base,body,unknown:gaps.join(' '),next:last?.outcome==='refuted'?'检查实际 UI 绑定、其他状态变量与对应版本，做反证复现并记录替代假设。':!states.length?'补采失败后的应用日志，保留同设备、构建、现场日期及请求标识，再继续调查。':!pairs.length?'补齐状态事件对应构建的完整源码和清理路径，再检验 H1。':conflicts.length?'核对复位执行时序及 UI 绑定，查明日志与源码之间的差异。':'分别验证请求为何超时、失败后 UI 是否退出；记录对照步骤与实际观察，再回写待验证结果。'};
}
function finish(s,id){
 const r=s.rounds.find(x=>x.id===id);if(!r||r.status!=='running')return;
 r.status='done';r.step=3;r.answer=reply(r);
 for(const p of r.answer.proposals||[]){let h=s.hypotheses.find(h=>h.id===p.id);if(!h)s.hypotheses.push({...p,status:'pending',history:[]});}
}
function verify(s,id,result){const h=s.hypotheses.find(h=>h.id===id);if(!h)throw Error('未找到假设');if(!['supported','refuted','inconclusive'].includes(result.outcome)||!result.method?.trim()||!result.observation?.trim())throw Error('请填写验证步骤和实际观察');h.history.push({...clone(result),evidence:clone(s.context.filter(e=>(result.evidenceIds||[]).includes(e.id))),method:result.method.trim(),observation:result.observation.trim(),time:clock(),id:id+'-V'+(h.history.length+1)});h.status=result.outcome;s.revision++;return h.history.at(-1);}
function handoff(s,id){let r=s.rounds.find(r=>r.id===id);if(r?.status!=='done')throw Error('请选择已完成的回答');const refs=[...r.snapshot,...s.context,...s.hypotheses.flatMap(h=>h.history.flatMap(v=>v.evidence||[]))];const evidence=[...new Map(refs.map(e=>[e.id+'|'+JSON.stringify(e),clone(e)])).values()];for(const e of evidence){if(evidence.filter(x=>x.id===e.id).length>1)e.label+='（'+(r.snapshot.some(x=>JSON.stringify(x)===JSON.stringify(e))?'回答快照':'后续材料快照')+'）';}return clone({round:id,question:r.question,body:r.answer.body,unknown:r.answer.unknown,next:r.answer.next,evidence,verifications:s.hypotheses.flatMap(h=>h.history.map(v=>({...v,hypothesis:h.id,title:h.title}))),hypotheses:s.hypotheses,revision:s.revision,answerRevision:r.revision,reasoning:clone(r.answer)});}
root.LoganAI={add,start,stop,finish,verify,handoff};if(typeof module!=='undefined')module.exports=root.LoganAI;
})(typeof globalThis!=='undefined'?globalThis:this);
