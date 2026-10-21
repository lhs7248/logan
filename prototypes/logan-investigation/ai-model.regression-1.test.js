const test=require('node:test'),assert=require('node:assert/strict'),AI=require('./ai-model');
const setup=()=>({rounds:[],context:[],hypotheses:[],revision:0});
// Regression: QA-009 — historical handoff evidence was overwritten by a new same-ID source.
// Found by /qa on 2026-10-20; report: evidence/qa-2026-10-20/report.md
test('回写保留同 ID 的旧回答证据和后续材料两个版本',()=>{let s=setup();AI.add(s,{id:'S5',kind:'source',index:4,task:'T1',label:'S5',text:'old source',mode:'matched'});let r=AI.start(s,'解释登录失败');AI.finish(s,r.id);s.context=[];AI.add(s,{id:'S5',kind:'source',index:4,task:'T1',label:'S5',text:'new source',mode:'matched'});let h=AI.handoff(s,r.id);assert.ok(h.evidence.some(e=>e.text==='old source'));assert.ok(h.evidence.some(e=>e.text==='new source'));assert.equal(r.snapshot[0].text,'old source');});
// Regression: QA-010 — out-of-scenario questions produced fixed login hypotheses.
test('无证据的非登录问题明确告知场景限制，不生成登录假设',()=>{let s=setup(),r=AI.start(s,'支付重复扣款，先补充什么信息？');AI.finish(s,r.id);assert.equal(s.hypotheses.length,0);assert.match(r.answer.body,/场景|不覆盖/);assert.equal(r.answer.codeConfirmed,false);});
// Regression: QA-011 — source matching ignored task, device and build identity.
for(const field of ['task','device','build'])test('不同'+field+'的源码不能称为对应版本源码',()=>{let s=setup(),base={task:'T1',device:'A',build:'100',index:4};AI.add(s,{...base,id:'L5',kind:'log',text:'loading=true'});AI.add(s,{...base,[field]:'different',id:'S5',kind:'source',text:'catch',mode:'matched'});let r=AI.start(s,'源码解释登录转圈');AI.finish(s,r.id);assert.doesNotMatch(r.answer.body,/对应失败分支的版本匹配源码样例/);assert.match(r.answer.body,/缺少|未验证/);});
