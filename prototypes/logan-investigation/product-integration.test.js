const {test}=require('node:test'),assert=require('node:assert/strict'),vm=require('node:vm'),fs=require('node:fs'),path=require('node:path');
const P=require('./product-model'),M=require('./model'),AI=require('./ai-model');
function load(){
 const i={id:'I',owner:'U',status:'调查中',description:'test',evidence:[],updates:[],requests:[],investigation:{context:[],rounds:[],hypotheses:[],revision:0}},out=[];
 const c={LoganProduct:P,M:{...M},AI:{...AI},state:{issues:[i]},issue:()=>i,investigation:()=>i.investigation,JSON,Date,console,crypto:require('node:crypto').webcrypto,records:[{module:'Network',time:'10:00',level:'INFO',content:'network'},{module:'App',time:'10:01',level:'ERROR',content:'application'}],recordsForTask:()=>c.records,save:()=>true,toast:t=>out.push(t),file:(name,text)=>out.push({name,text}),lastAnswer:()=>i.investigation.rounds.filter(r=>r.status==='done').at(-1),productBlocked:(i,items)=>items.filter(e=>P.availability(i,e)!=='available')};
 for(const name of ['product-collection.js','product-evidence.js']){
  const source=fs.readFileSync(path.join(__dirname,name),'utf8');
  for(const [,dependency] of source.matchAll(/const product\w+=(\w+);/g))if(!(dependency in c))c[dependency]=()=>'';
 }
 c.currentTask=()=>null;c.fullLogSources=()=>c.sources||[];c.attachmentCandidates=()=>c.candidates||[];c.evidenceMetadata=()=>({});c.findAsset=()=>null;
 vm.createContext(c);for(const name of ['product-collection.js','product-evidence.js'])vm.runInContext(fs.readFileSync(path.join(__dirname,name),'utf8'),c);
 return {c,i,out};
}
test('实际采集推进在无渠道回执时不伪造设备送达',()=>{const {c,i}=load(),s={sequence:1},r=c.M.create(s,i,{device:'D',begin:'a',end:'b',types:['混合日志']}).request;r.scenario='noreceipt';for(let n=0;n<5;n++)c.M.advance(r);assert.equal(r.status,'完成');assert.equal(r.events.some(e=>e.text.includes('回执已收到')),false);assert.ok(r.events.some(e=>e.text.includes('SDK 已确认')));});
test('按日混合文件的阅读和下载均保留全部类型',()=>{const {c,out}=load(),r={task:'T',types:['混合日志']};c.sources=[{key:'K',request:r}];assert.equal(c.availableIndices({types:['混合日志']}).length,2);c.downloadSourceSample('K');assert.match(out[0].text,/network/);assert.match(out[0].text,/application/);});
test('AI 排除源码后实际快照与清单一致，调查范围不被删除',()=>{const {c,i}=load();i.product={includeSource:false};i.investigation.context=[{id:'L',kind:'log',task:'T',text:'log'},{id:'S',kind:'source',task:'T',text:'source'}];const r=c.AI.start(i.investigation,'question');assert.equal(r.snapshot.length,1);assert.equal(r.productManifest.excluded.length,1);assert.equal(i.investigation.context.length,2);});
test('AI 开始前及生成过程中均检查证据可用性',()=>{const {c,i}=load();i.investigation.context=[{id:'L',kind:'log',text:'log'}];i.product={availability:{L:'expired'}};assert.throws(()=>c.AI.start(i.investigation,'question'),/过期/);delete i.product.availability.L;const r=c.AI.start(i.investigation,'question');i.product.availability.L='denied';c.AI.finish(i.investigation,r.id);assert.equal(r.status,'interrupted');assert.equal(r.answer,undefined);});
test('过期文件重解析不会新建批次',()=>{const {c,i,out}=load();i.product={expiredAssets:{A:true}};c.findAsset=()=>({id:'A',issueId:'I'});c.openReparse('A');assert.match(out[0],/过期/);});
test('原资产及仓库撤权优先于单条证据可用标记',()=>{const i={product:{availability:{E:'available'},unavailableTasks:{T:'expired'},sourceDenied:true}};assert.equal(P.availability(i,{id:'E',task:'T'}),'expired');assert.equal(P.availability(i,{id:'E',kind:'source'}),'denied');});
