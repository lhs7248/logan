'use strict';
const availabilityNames={available:'可用',expired:'已过期（演练）',denied:'无权访问（演练）'};
function productBlocked(i,items){return (items||[]).filter(e=>P.availability(i,e)!=='available');}
const productEvidenceMetadata=evidenceMetadata;
evidenceMetadata=function(t,r){return {...productEvidenceMetadata(t,r),version:t?.version||null,build:t?.build||null,assetId:t?.assetId||null,runId:t?.runId||null};};
const productEvidencePanel=evidencePanel;
evidencePanel=function(){const s=investigation();return `<section class="product-card"><h3>下轮引用范围</h3><label class="check"><input type="checkbox" ${productState().includeSource!==false?'checked':''} onchange="productState().includeSource=this.checked;investigation().revision++;save();render()">包含已选源码证据</label><p>只引用下方选中的材料；补采不会自动进入本轮，历史回答不被改写。</p><div class="product-evidence-list">${s.context.map((e,n)=>`<div><span><b>${esc(e.label)}</b><small>${esc(e.assetId||e.task)}${e.runId?' / '+esc(e.runId):''} · ${esc(availabilityNames[P.availability(issue(),e)])}</small></span>${btn('管理引用',`productEvidenceStatus(${n})`)}</div>`).join('')||'<p>尚未选择证据，可从左侧日志或源码面板加入。</p>'}</div></section>`+productEvidencePanel();};
function productEvidenceStatus(n){const e=investigation().context[n];openModal('证据可用性 · '+esc(e.label),`<p>出处：${esc(e.assetId||e.task)}${e.runId?' / '+esc(e.runId):''}。同一来源重新解析后，历史引用仍保留原批次。</p><p>当前：${esc(availabilityNames[P.availability(issue(),e)])}</p><p class="notice">下方只演练到期/撤权后的界面与操作限制，不删除原始数据。真实保留期清理仍需服务端实现。</p><div class="toolbar">${btn('演练证据过期',`productSetEvidence(${n},'expired')`)}${btn('演练无权访问',`productSetEvidence(${n},'denied')`)}${btn('恢复演练前可用状态',`productSetEvidence(${n},'available')`)}${btn('移出下轮范围',`removeContext('${e.id}');closeModal()`)}${btn('返回','closeModal()')}</div>`);}
function productSetEvidence(n,value){const e=investigation().context[n],p=productState();p.availability=p.availability||{};if(value==='available')delete p.availability[e.id];else p.availability[e.id]=value;investigation().revision++;save();closeModal();render();}
const productOpenEvidence=openChatEvidence;
openChatEvidence=function(id,round){const e=findChatEvidence(id,round);if(e&&P.availability(issue(),e)!=='available')return openModal('证据内容当前不可用',`<p>${esc(e.label)} · ${esc(availabilityNames[P.availability(issue(),e)])}</p><p>保留原始 ID、出处与轮次，不替换成其他日志。可在证据范围中恢复演练或重新补采。</p>${btn('管理证据范围',"closeModal();deskTool('evidence')")}${btn('配置补采',"closeModal();deskTool('collect')")}`);productOpenEvidence(id,round);};
const productOriginalLocation=openOriginalEvidence;
openOriginalEvidence=function(id,round){const e=findChatEvidence(id,round);if(e&&P.availability(issue(),e)!=='available')return openChatEvidence(id,round);productOriginalLocation(id,round);};
const productRoundView=roundView;
roundView=function(r){const bad=productBlocked(issue(),r.snapshot);if(bad.length)return `<article class="round"><h3>${esc(r.id)} · ${esc(r.question)}</h3><div class="notice">本轮有 ${bad.length} 项证据当前不可用。回答可能含相关摘录，暂不展示正文及导出；原轮次和引用身份保留。</div>${bad.map(e=>chatCitation(e,null,r.id)).join('')}${btn('查看证据范围',"deskTool('evidence')")}</article>`;
 const manifest=r.productManifest;
 return productRoundView(r)+(manifest?`<details class="product-card"><summary>本轮实际引用清单 · 包含 ${manifest.included.length} / 排除 ${manifest.excluded.length}</summary><p>发送时固定，不随之后补证变化。未访问其他工单、仓库或模型。</p>${manifest.included.map(e=>`<p>包含：${esc(e.label)} · ${esc(e.task)}</p>`).join('')}${manifest.excluded.map(e=>`<p>排除：${esc(e.label)} · ${esc(e.reason)}</p>`).join('')}</details>`:'');};
const productStartAI=AI.start;
AI.start=function(s,q){
 const included=s.context.filter(e=>productState().includeSource!==false||e.kind!=='source');
 if(productBlocked(issue(),included).length)throw Error('所选证据已过期或无权访问，请在证据范围中处理后再提问');
 const all=s.context;let r;
 try{s.context=included;r=productStartAI(s,q);}finally{s.context=all;}
 r.productManifest={included:included.map(e=>({id:e.id,label:e.label,task:e.task})),excluded:all.filter(e=>!included.includes(e)).map(e=>({id:e.id,label:e.label,reason:'本轮未包含源码'}))};
 return r;
};
const productRetryChat=retryChat;
retryChat=function(id){const r=investigation().rounds.find(r=>r.id===id);if(productBlocked(issue(),r?.snapshot).length)return toast('原快照含不可用证据，无法原样重试。请更新范围后发起新问题。');productRetryChat(id);const next=investigation().rounds.at(-1);if(next?.retryOf===id){next.productManifest=r.productManifest?JSON.parse(JSON.stringify(r.productManifest)):{included:r.snapshot.map(e=>({id:e.id,label:e.label,task:e.task})),excluded:[]};save();}};
const productExportConversation=exportConversation;
exportConversation=function(){const s=investigation();if(productBlocked(issue(),[...s.context,...s.rounds.flatMap(r=>r.snapshot)]).length)return toast('调查含不可用证据，导出暂不可用；可恢复演练状态后导出。');productExportConversation();};
downloadReport=exportConversation;
const productSourceMarkup=inlineSource;
inlineSource=function(){
 const t=currentTask();if(t&&P.availability(issue(),{task:t.id})!=='available')return '<div class="notice">日志资产已过期，无法继续定位源码。请先恢复演练或补采。</div>';
 if(productState().sourceDenied)return `<div class="product-card"><h3>源码当前不可访问</h3><p>仓库权限不足（演练）。保留定位线索，但不显示源码正文、不用于 AI 分析。</p>${btn('恢复源码访问演练',"productState().sourceDenied=false;investigation().revision++;save();render()")}${btn('记录访问缺口',"addGap('仓库无权访问，需由仓库负责人确认权限');toast('已记录访问缺口')")}</div>`;
 return productSourceMarkup()+`<details class="product-card"><summary>版本与访问边界</summary><p>构建匹配仅为合成映射。关键词候选、人工片段和缺失映射均不能确认现场根因；相关变更须区分故障构建与修复构建。</p>${btn('演练仓库无法访问',"productState().sourceDenied=true;investigation().revision++;save();render()")}</details>`;
};
function productAssetExpired(a){return !!productState(state.issues.find(i=>i.id===a.issueId)).expiredAssets?.[a.id];}
function productToggleAsset(id){const a=findAsset(id),i=state.issues.find(i=>i.id===a.issueId),p=productState(i);p.expiredAssets=p.expiredAssets||{};p.unavailableTasks=p.unavailableTasks||{};p.expiredAssets[id]=!p.expiredAssets[id];for(const r of a.runs){if(r.task){if(p.expiredAssets[id])p.unavailableTasks[r.task]='expired';else delete p.unavailableTasks[r.task];}}if(i.investigation)i.investigation.revision++;save();render();}
const productAssetView=assetParseView;
assetParseView=function(x){const a=x.asset,r=x.run,expired=productAssetExpired(a);return `<section class="product-card"><h3>原文件与解析版本</h3><p>原文件 ${esc(a.id)} → 解析批次 ${esc(r.id)} → ${esc(r.task||'尚未发布任务')}</p><p>重新解析生成新批次；已有证据始终指向原批次。${expired?'当前原文件已过期（演练），不能读取或重新解析。':'当前原文件可用，历史批次独立保留。'}</p><details><summary>演练文件期限</summary><p>仅限制读取与操作，不清除本地原文。</p>${btn(expired?'恢复文件可用状态':'演练原文件过期',`productToggleAsset('${a.id}')`,'', ['待解析','解析中'].includes(r.status))}</details></section>`+(expired?`<div class="notice">内容不可用；文件与批次身份保留。可重新导入文件或从工单补采。</div>${btn('重新导入日志','openLogImport()')}${btn('补采日志',"deskTool('collect')")}`:productAssetView(x)+(['待解析','解析中'].includes(r.status)?btn('取消本次解析',`productCancelParse('${a.id}','${r.id}')`):''));};
function productCancelParse(id,run){const a=findAsset(id),r=a.runs.find(r=>r.id===run);if(!['待解析','解析中'].includes(r.status))return toast('批次已结束，无需取消');clearInterval(parseTimers.get(id+'/'+run));parseTimers.delete(id+'/'+run);r.status='已取消';r.events.push({time:new Date().toLocaleTimeString('zh-CN'),text:'用户取消此轮解析，原文件及其他批次保留'});save();render();}
const productReparse=openReparse;
openReparse=function(id){if(productAssetExpired(findAsset(id)))return toast('原文件已过期，不能重新解析；请重新导入或补采');productReparse(id);};
const productDownloadAsset=downloadAssetFile;
downloadAssetFile=function(id,n){if(productAssetExpired(findAsset(id)))return toast('原文件已过期，下载不可用');productDownloadAsset(id,n);};
const productExportAsset=exportAssetRun;
exportAssetRun=function(id,run){if(productAssetExpired(findAsset(id)))return toast('解析记录包含过期正文，导出不可用');productExportAsset(id,run);};
const productDownloadSource=downloadSourceSample;
downloadSourceSample=function(key){const x=fullLogSources().find(x=>x.key===key);if(x?.asset&&productAssetExpired(x.asset))return toast('原文件已过期，下载不可用');productDownloadSource(key);};
const productRenderLogs=renderLogs;
renderLogs=function(){productRenderLogs();const t=currentTask();if(t&&P.availability(issue(),{task:t.id})!=='available'){$('#logs').innerHTML='<div class="empty">当前解析内容不可用，原引用身份保留。</div>';$('#detail').innerHTML=btn('查看文件与历史',"deskTool('parse')");$('#count').textContent='内容已过期（演练）';}};
// Apply simulated availability to all entry points, without deleting stored history.
const productSafeEvidencePanel=evidencePanel;
evidencePanel=function(){let html=productSafeEvidencePanel();for(const e of productBlocked(issue(),investigation().context))html=html.split('<p>'+esc(e.text)+'</p>').join('<p class="notice">正文当前不可用；保留原引用。</p>');return html;};
const productDeskSummary=deskSummary;
deskSummary=function(){const r=lastAnswer();return r&&productBlocked(issue(),r.snapshot).length?'<section class="desk-summary"><div><small>当前判断 · 证据不可用</small><p>本轮依赖的部分证据当前不可访问，暂不展示可能包含摘录的判断。</p></div>'+btn('检查证据范围',"deskTool('evidence')")+'</section>':productDeskSummary();};
const productFinishAI=AI.finish;
AI.finish=function(s,id){const i=state.issues.find(i=>i.investigation===s),r=s.rounds.find(r=>r.id===id);if(i&&r&&productBlocked(i,r.snapshot).length){r.status='interrupted';r.productInterrupted='生成期间证据失效，请检查范围后重新提问';return;}return productFinishAI(s,id);};
const productAddRows=deskAddRows;
deskAddRows=function(mode){const t=currentTask();if(t&&P.availability(issue(),{task:t.id})!=='available'){toast('当前内容不可用，请先恢复演练状态或补采');return false;}return productAddRows(mode);};
const productCandidates=attachmentCandidates;
attachmentCandidates=function(){return productCandidates().filter(e=>P.availability(issue(),e)==='available');};
const productMixedDownload=downloadSourceSample;
downloadSourceSample=function(key){const x=fullLogSources().find(x=>x.key===key);if(x?.request.task&&x.request.types?.includes('混合日志'))return file(x.request.task+'-synthetic.log','合成混合日志，非实际设备数据\n'+records.map(r=>r.time+' '+r.level+' '+r.module+' '+r.content).join('\n'));productMixedDownload(key);};
const productCollectionReparse=reparseCollection;
reparseCollection=function(key){const x=fullLogSources().find(x=>x.key===key);if(!x?.request.types?.includes('混合日志'))return productCollectionReparse(key);const previous=(x.issue.logAssets||[]).find(a=>a.sourceKey===key);if(previous)return openReparse(previous.id);try{const r=x.request,a=L.importFiles(state,x.issue,{platform:'Native',origin:'collection',sourceKey:key,device:r.device,version:r.version||'未知',build:r.build||'未知',begin:r.product?.scope.begin||r.begin,end:r.product?.scope.end||r.end,files:[{name:'mixed-demo.log',text:records.map(v=>v.time+' '+v.level+' '+v.module+' '+v.content).join('\n')}],format:'text',policy:'strict'}).asset;selectAssetRun(a,a.runs[0]);}catch(e){toast(e.message);}};

const productComposer=composer;
composer=function(){const s=investigation(),included=s.context.filter(e=>productState().includeSource!==false||e.kind!=='source');return productComposer().replace('将使用 '+s.context.length+' 项证据快照','将使用 '+included.length+' 项证据快照'+(included.length<s.context.length?' · 已排除 '+(s.context.length-included.length)+' 项源码':''));};
const productScopeMarkup=deskScope;
deskScope=function(x){let html=productScopeMarkup(x);const begin=x?.request.begin||issue().begin,end=x?.request.end||issue().end;if(begin?.slice(0,10)!==end?.slice(0,10))html=html.replace(' — '+esc(end.slice(11)),' — '+esc(end.replace('T',' ')));return html;};
const productGuide=showGuide;
showGuide=function(){productGuide();const note=document.createElement('section');note.className='product-card';note.innerHTML='<h3>定稿规则演练</h3><ol><li>补采：选择按日能力，核对扩大范围；在演练结果中选择无送达回执。</li><li>证据：管理引用可演练过期/撤权，恢复后继续调查；包含源码开关只影响下轮。</li><li>解析：取消本次解析后可新建批次；原文件过期演练可恢复。</li><li>验证：假设验证与修复验证分别记录；关闭工单前先保存待验证。</li><li>回写：支持人工记录、陈旧草稿重新预览及重复提交保护。</li></ol><p>本轮所有限制是本地交互演练；真实权限、数据清理、渠道和模型尚未接入。</p>';$('#modal').append(note);};
