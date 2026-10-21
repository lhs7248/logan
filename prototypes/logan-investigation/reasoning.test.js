const test=require('node:test'),assert=require('node:assert/strict'),AI=require('./ai-model'),M=require('./model');
const meta={scene:'login',task:'T1',device:'A',platform:'Native',version:'1',build:'100',day:'2026-10-19',file:'LoginViewModel.swift',line:124,requestId:'req-A'};
const log=(id,text,extra={})=>({...meta,id,label:id,kind:'log',index:4,text,...extra});
const source=(extra={})=>({...meta,id:'S',label:'S',kind:'source',index:4,mode:'matched',text:'118 func login() { loading = true\n try { await auth.login(); loading = false }\n catch { logger.error("retry exhausted") }\n}',...extra});
const setup=context=>({rounds:[],context,hypotheses:[],revision:0});
const ask=(s,q='解释登录转圈的证据和假设')=>{const r=AI.start(s,q);AI.finish(s,r.id);return r;};
test('日志下标不产生事实，普通第五条记录不推出 loading=true',()=>{
 const r=ask(setup([log('L','用户打开设置页')]));
 assert.doesNotMatch(r.answer.body,/日志显示.*loading=true/);
 assert.equal(r.answer.facts.length,0);
});
test('重排日志仍能识别显式状态，代码不能仅靠 matched 标签得出缺陷',()=>{
 const r=ask(setup([log('L','retry exhausted; loading=true',{index:0}),source({text:'catch { loading = false }'})]));
 assert.ok(r.answer.facts.some(f=>f.evidenceIds.includes('L')));
 assert.doesNotMatch(r.answer.body,/源码.*未展示状态复位/);
 assert.ok(r.answer.conflicts.some(x=>/复位/.test(x.text)));
});
test('仅网络切换不自动产生加载状态或超时因果假设',()=>{
 const s=setup([log('N','网络状态切换 Wi-Fi → cellular',{index:1,requestId:null,file:null})]);ask(s);
 assert.deepEqual(s.hypotheses,[]);
});
test('无证据的登录提问不产生候选假设',()=>{const s=setup([]);ask(s);assert.deepEqual(s.hypotheses,[]);});
test('源码关联要求同任务文件行号与构建，错误文件不能支撑缺陷',()=>{
 const r=ask(setup([log('L','retry exhausted; loading=true'),source({file:'Unrelated.swift'})]));
 assert.equal(r.answer.sourceLinked,false);
 assert.match(r.answer.unknown,/源码/);
});
test('仅存在相同下标、缺失身份的源码不视为已关联',()=>{
 const r=ask(setup([{id:'L',kind:'log',index:4,task:'T',text:'loading=true'},{id:'S',kind:'source',index:4,task:'T',mode:'matched',text:'catch { logger.error("failed") }'}]));
 assert.equal(r.answer.sourceLinked,false);
});
test('旧任务从工单借用的版本只作线索，不能当作采集时映射',()=>{
 const r=ask(setup([log('L','loading=true',{versionOrigin:'issue-hint'}),source({versionOrigin:'issue-hint'})]));
 assert.equal(r.answer.sourceLinked,false);
});
for(const field of ['requestId','device','build','day'])test('超时与状态的 '+field+' 不同不能拼成同一请求因果链',()=>{
 const r=ask(setup([log('T','POST /login timeout'),log('L','retry exhausted; loading=true',{[field]:'other'}),source()]));
 assert.equal(r.answer.requestLinked,false);
 assert.ok(r.answer.gaps.some(x=>/同一请求/.test(x)));
});
test('补采后列出新增证据及证据变化，旧轮次保持不变',()=>{
 const s=setup([log('N','网络状态切换 Wi-Fi → cellular',{index:1,requestId:null,file:null})]),a=ask(s),old=JSON.stringify(a);
 AI.add(s,log('T','POST /login timeout',{index:2}));AI.add(s,log('L','retry exhausted; loading=true'));AI.add(s,source());
 const b=ask(s);
 assert.deepEqual(b.answer.delta.added.map(x=>x.id),['T','L','S']);
 assert.equal(b.answer.requestLinked,true);assert.equal(b.answer.sourceLinked,true);
 assert.equal(JSON.stringify(a),old);
 assert.ok(s.hypotheses.some(h=>h.id==='H1'));
 assert.ok(!s.hypotheses.some(h=>h.id==='H2'));
});
test('人工反驳改变当前推断和下一步，保留原日志事实',()=>{
 const s=setup([log('L','retry exhausted; loading=true'),source()]);ask(s);
 AI.verify(s,'H1',{outcome:'refuted',method:'观察 UI 绑定',observation:'实际 loading 已恢复但遮罩仍存在',evidenceIds:['L']});
 const r=ask(s);assert.match(r.answer.body,/反驳/);assert.match(r.answer.next,/UI/);
 assert.ok(r.answer.facts.length);assert.ok(r.answer.conflicts.length);
});
test('回写保存逐项事实引用与证据变化，后续证据不会改写回答',()=>{
 const s=setup([log('L','retry exhausted; loading=true'),source()]),r=ask(s),h=AI.handoff(s,r.id);
 assert.deepEqual(h.reasoning.facts,r.answer.facts);assert.ok(h.reasoning.inferences.length);
 s.context[0].text='changed';assert.notEqual(h.evidence[0].text,'changed');
});
test('网络假设反驳后不再作为当前候选推断，历史事实保留',()=>{
 const s=setup([log('N','网络状态切换 Wi-Fi → cellular',{index:1}),log('T','POST /login timeout',{index:2})]);ask(s);
 AI.verify(s,'H2',{outcome:'refuted',method:'切换与不切换对照',observation:'两组都超时',evidenceIds:['N','T']});
 const r=ask(s);assert.ok(!r.answer.inferences.some(x=>x.text.startsWith('H2')));assert.ok(r.answer.conflicts.some(x=>/H2/.test(x.text)));
});
test('手动材料即便含 loading=true 也不能混入已验证样例事实',()=>{
 const r=ask(setup([log('manual','loading=true',{manual:true}),log('N','网络状态切换 Wi-Fi → cellular',{requestId:null})]));
 assert.ok(!r.answer.facts.some(x=>x.evidenceIds.includes('manual')));
});
test('新采集任务保留当时版本、现场日期，编辑工单不改旧任务映射',()=>{
 const s=M.initial(),i=s.issues[0],r=M.create(s,i,{device:i.device,begin:i.begin,end:i.end,types:['应用日志'],budget:32,ttl:30,notify:false}).request;
 while(!M.terminal.includes(r.status))M.advance(r);
 i.build='new';i.version='new';const t=M.tasks(i)[0];
 assert.equal(t.build,'38107');assert.equal(t.version,'3.8.1');assert.equal(t.begin,'2026-10-19T10:40');
});
