'use strict';
// Presentation adapter: existing files, requests, evidence and answer snapshots stay intact.
const D=LoganDesk;
const desk=Object.assign({tool:'source',library:false,libraryQuery:'',libraryStatus:'',module:'',from:'',to:'',checked:{},logKey:null,issueKeys:{}},state.deskUI||{});
let deskCollectOrigin=null,deskSaveOrigin=null,deskSubmittingCapture=false;
const deskOldGo=go;
function deskSave(){let x=deskSource();if(x?.request.task&&currentTask()?.id===x.request.task){desk.focusBySource=desk.focusBySource||{};desk.focusBySource[x.key]={selected,query,level,contextMode,module:desk.module,from:desk.from,to:desk.to};}state.deskUI={...desk};persistWork();}
function deskSources(){return fullLogSources().filter(x=>x.asset||x.request.files.some(f=>['已上传','可查','解析失败'].includes(f.status)));}
function deskSource(){let key=page==='workbench'?desk.issueKeys[state.active]:workUI.logKey;if(page==='workbench'&&!key){let e=issue().investigation?.rounds.filter(r=>r.status==='done').at(-1)?.snapshot.filter(e=>e.kind==='log').at(-1);key=deskSources().find(x=>x.issue.id===state.active&&x.request.task===e?.task)?.key;}return D.chooseSource(deskSources(),key,page==='workbench'?state.active:null);}
function deskSync(){for(const i of state.issues){if(['待采集','待解析'].includes(i.status)&&M.tasks(i).length)i.status='调查中';}let x=deskSource();if(x){if(x.issue.id!==state.active)draftHandoff=null;syncLog(x);if(page==='workbench')desk.issueKeys[state.active]=x.key;else desk.logKey=x.key;}else{taskId=null;requestId=null;}return x;}
function deskRows(){const x=deskSource();if(!x?.request.task)return [];return availableIndices().map(n=>({...recordsForTask()[n],n}));}
function deskFiltered(){return D.filterEvents(deskRows(),{query,level,module:desk.module,from:desk.from,to:desk.to});}
function deskSelection(){return desk.checked[currentTask()?.id]||[];}
function deskResetFilter(){query='';level='全部';contextMode=false;desk.module='';desk.from='';desk.to='';}
function deskChoose(key){persistHandoff();const x=deskSources().find(x=>x.key===key);if(!x)return;if(page==='workbench'&&x.issue.id!==state.active)return;if(x.issue.id!==state.active)draftHandoff=null;syncLog(x);desk.logKey=x.key;desk.issueKeys[x.issue.id]=x.key;selected=availableIndices()[0]??0;desk.library=false;deskResetFilter();deskSave();render();}
function deskTool(tool){persistHandoff();desk.tool=tool;workUI.issueMode=tool==='chat'?'chat':tool;deskSave();render();}
function deskOpenIssue(id){persistHandoff();desk.library=false;desk.libraryQuery='';desk.libraryStatus='';state.active=id;draftHandoff=null;taskId=null;deskResetFilter();desk.tool='chat';workUI.issueMode='chat';issueDetail=true;deskSave();go('workbench');}
selectIssue=deskOpenIssue;
setIssueSelection=deskOpenIssue;
setIssueMode=function(mode){persistHandoff();if(page==='logcenter'){let x=deskSource();if(x)desk.issueKeys[x.issue.id]=x.key;}desk.tool=mode;workUI.issueMode=mode;if(mode==='handoff')draftHandoff=null;deskSave();go('workbench');};
setLogMode=function(mode){desk.tool=mode==='parse'?'parse':mode==='source'?'source':desk.tool;workUI.logMode='read';deskSave();go('logcenter');};
setLogSelection=deskChoose;
go=function(route){
  if(!(route==='issues'&&deskSaveOrigin))persistHandoff();
  if(route==='issues'&&deskSaveOrigin){const origin=deskSaveOrigin;deskSaveOrigin=null;draftHandoff=null;desk.tool='result';workUI.issueMode='result';page=origin;location.hash=origin;render();return;}
  if(route==='collectioncenter'&&deskCollectOrigin){const origin=deskCollectOrigin;deskCollectOrigin=null;desk.tool='collect';workUI.issueMode='collect';page=origin;location.hash=origin;render();return;}
  if(route==='parsing'){desk.tool='parse';workUI.logMode='read';route='logcenter';}
  if(route==='collect'&&['workbench','logcenter'].includes(page)){let x=collectionSources().find(x=>x.issue.id===state.active&&x.request.id===requestId);if(x)workUI.collectionKey=x.key;deskTool('collect');return;}
  if(route==='source'){let x=deskSources().find(x=>x.request.task===taskId);if(x){workUI.logKey=x.key;desk.issueKeys[x.issue.id]=x.key;}desk.tool='source';workUI.issueMode='source';render();return;}
  if(route==='report'){desk.tool='chat';workUI.issueMode='chat';if(page==='logcenter'){let x=deskSource();if(x)desk.issueKeys[x.issue.id]=x.key;}route='workbench';}
  if(route==='logs'){let x=fullLogSources().find(x=>x.request.task===taskId);if(x){workUI.logKey=x.key;desk.issueKeys[x.issue.id]=x.key;}workUI.logMode='read';if(page==='workbench'){render();return;}route='logcenter';}
  if(route==='logcenter'&&workUI.logMode==='parse'){desk.tool='parse';workUI.logMode='read';}
  deskOldGo(route);
};
openParsing=function(key){const x=deskSources().find(x=>x.key===key);if(!x)return;syncLog(x);desk.issueKeys[x.issue.id]=key;desk.tool='parse';workUI.logMode='read';if(page==='workbench')render();else go('logcenter');};
openSourceLogs=function(key){const x=deskSources().find(x=>x.key===key);if(!x?.request.task)return toast('此批次尚无可读日志，请先查看解析结果');if(!['logcenter','workbench'].includes(page)){page='logcenter';location.hash='logcenter';}deskChoose(key);desk.tool='source';workUI.logMode='read';render();};
openLinkedInvestigation=function(key){let x=fullLogSources().find(x=>x.key===key);state.active=x.issue.id;desk.issueKeys[x.issue.id]=key;setIssueMode('chat');};
const deskOldSelectAsset=selectAssetRun;
selectAssetRun=function(a,r){const origin=page;deskOldSelectAsset(a,r);desk.issueKeys[a.issueId]=a.id+'/'+r.id;desk.tool='parse';if(origin==='workbench'){page='workbench';location.hash='workbench';}render();};
function deskTitle(x){return x?.asset?.files.map(f=>f.name).join('、')||(x?x.request.types?.join(' / ')+' · '+x.request.device:'尚无日志');}
function deskSourceOption(x){return deskTitle(x)+' · '+x.request.begin?.slice(5).replace('T',' ')+' · '+sourceStatus(x.request);}
function deskLibrary(){
 const sources=deskSources().filter(x=>page!=='workbench'||x.issue.id===state.active),groups=D.groupSources(sources);
 return `<section class="desk-library"><div class="desk-library-head"><b>日志资料库 <small>${groups.length} 份日志 · 历史批次归档保留</small></b>${btn('收起资料库',"desk.library=false;render()")}</div><div class="toolbar"><input aria-label="查找日志资料" value="${esc(desk.libraryQuery)}" placeholder="文件、设备、版本或工单" oninput="desk.libraryQuery=this.value;deskFilterLibrary()"><select aria-label="最新解析状态" onchange="desk.libraryStatus=this.value;deskFilterLibrary()">${['','解析完成','部分可查','解析失败','待解析','解析中'].map(v=>`<option value="${v}" ${desk.libraryStatus===v?'selected':''}>${v||'全部状态'}</option>`).join('')}</select></div><div class="desk-library-list">${groups.map(g=>{let x=g.latest,read=g.readable;return `<article data-desk-source data-search="${esc(JSON.stringify([deskTitle(x),x.request.device,x.issue.id,x.asset?.version||x.issue.version,x.asset?.build||x.issue.build]))}" data-status="${sourceStatus(x.request)}"><div><strong>${esc(deskTitle(x))}</strong><p>${esc(x.request.device)} · ${esc(x.request.begin.replace('T',' '))} · ${esc(x.issue.id)}</p><small>${g.sources.length} 个批次 · 最新 ${sourceStatus(x.request)}${!x.request.task&&read?' · 旧版仍可阅读':''}</small></div><div>${btn(read?'阅读日志':'查看解析',`deskChoose('${(read||x).key}');deskTool('${read?'source':'parse'}')`,read?'primary':'')}${btn('解析与历史',`deskChoose('${x.key}');deskTool('parse')`)}</div></article>`;}).join('')}</div><p id="desk-library-empty" hidden>没有匹配资料，请调整关键词或状态。</p></section>`;
}
function deskFilterLibrary(){let count=0;document.querySelectorAll('[data-desk-source]').forEach(e=>{e.hidden=!e.dataset.search.toLowerCase().includes(desk.libraryQuery.toLowerCase())||!!desk.libraryStatus&&e.dataset.status!==desk.libraryStatus;if(!e.hidden)count++;});if($('#desk-library-empty'))$('#desk-library-empty').hidden=!!count;deskSave();}
function deskScope(x){let i=issue();return `<section class="desk-scope"><div class="scope-main"><span class="scope-marker"></span><div><strong>${esc(x?.request.device||i.device)}</strong><span>${esc(x?.asset?.platform||'Native')} · ${esc(x?.asset?.version||i.version)} / ${esc(x?.asset?.build||i.build)}</span></div><div class="scope-time"><small>现场时间 · 北京时间</small><b>${esc((x?.request.begin||i.begin).replace('T',' '))} — ${esc((x?.request.end||i.end).slice(11))}</b></div></div><div class="scope-link">${badge(i.status)}${btn(esc(i.id)+' · '+esc(i.title),`deskOpenIssue('${i.id}')`,'textbutton')}</div></section>`;}
function deskSummary(){const s=investigation(),a=lastAnswer(s),i=issue();return `<section class="desk-summary"><div><small>当前判断${a?' · '+a.id:''}</small>${a?'<div class="judgment-scope">依据 '+esc([...new Set(a.snapshot.map(e=>e.task))].join('、'))+' · 切换阅读日志不会改写此判断</div>':''}${a&&a.revision!==s.revision?'<p class="notice">有新证据或验证尚未纳入此判断，请继续追问。</p>':''}<p>${esc(a?.answer.body||'尚未形成判断。先定位异常时间和事件，再将相关日志交给 AI 整理。')}</p></div><div><small>下一步</small><p>${esc(a?.answer.next||(tasks().length?'阅读现场日志，补充能解释现象的证据。':'获取故障时间段的日志。'))}</p>${btn(a?'检查假设与验证':'补充现场日志',a?"deskTool('hypotheses')":"deskTool('collect')",'textbutton')}</div></section>`;}
function deskWorkspace(isIssue){
 const x=deskSync(),i=issue();
 return `<div class="desk-page-heading"><div><span class="eyebrow">${isIssue?'INVESTIGATION / '+esc(i.id):'LOG EXPLORER'}</span><h1>${isIssue?esc(i.title):'日志阅读'}</h1></div><div class="toolbar">${isIssue?btn('问题与记录',"deskTool('result')")+btn('全部问题',"issueDetail=false;go('issues')"):btn('上传日志文件','openLogImport()','primary')}<details class="desk-menu"><summary>更多操作</summary><div>${btn('上传日志文件','openLogImport()')}${btn('载入 Web 样例','loadWebLogSample()')}${btn('下载 Web 样例','downloadWebLogSample()')}${btn('粘贴日志 / 源码片段','pasteEvidence()')}${btn('导出对话与调查','exportConversation()','',!investigation().rounds.length)}${isIssue?btn('编辑工单','editIssue()'):''}</div></details></div></div>${deskScope(x)}${isIssue?deskSummary():''}${desk.library?deskLibrary():''}<div class="desk-selection"><div>${btn('切换日志',"desk.library=!desk.library;render()")}${x?`<strong>${esc(deskTitle(x))}</strong><small>${x.asset?x.run.id:x.request.id} · ${sourceStatus(x.request)}</small>`:'<span>此问题还没有可读日志</span>'}</div><div>${btn('解析与历史',"deskTool('parse')")}${btn('补充采集',"deskTool('collect')")}${x?.request.task?btn('下载正文',`downloadSourceSample('${x.key}')`):''}</div></div><div class="desk-workspace"><section class="desk-reader" data-source-key="${esc(x?.key||'')}" aria-label="日志阅读区">${deskReader(x)}</section><section class="desk-assistant" data-tool="${desk.tool}" aria-label="调查工具"><div class="desk-tool-tabs" role="tablist" aria-label="调查工具">${[['source','源码'],['chat','AI 排查'],['evidence','证据'],['collect','补充采集'],['hypotheses','验证'],['handoff','回写']].map(([v,t])=>`<button role="tab" aria-selected="${desk.tool===v}" onclick="deskTool('${v}')">${t}</button>`).join('')}</div><div class="desk-tool-body">${deskPanel(x)}</div></section></div>`;
}
function deskCoverage(x){
 if(x.request.status!=='部分成功')return '';
 const recovered=x.asset?[]:x.issue.requests.filter(r=>r.parent===x.request.id&&r.task);
 const failed=x.request.files.filter(f=>f.status!=='可查').map(f=>f.name+'：'+f.status).join('；')||'部分记录已隔离';
 return '<div class="reader-coverage">当前批次部分可读 · '+esc(failed)+' '+btn('查看处理记录',"deskTool('"+(x.asset?'parse':'collect')+"')",'textbutton')+recovered.map(r=>'<br>关联补采 '+esc(r.id)+' 已返回可读文件 '+btn('打开补采结果',"deskChoose('"+x.issue.id+'/'+r.id+"');deskTool('chat')",'textbutton')).join('')+'</div>';
}
function deskReader(x){
 if(!x?.request.task)return `<div class="empty"><div class="empty-symbol">≋</div><h3>${x?'这份日志尚不可读':'先取得问题现场的日志'}</h3><p>${x?'查看右侧解析结果，原文件与历史记录都已保留。':'可以从设备补采，或导入本地文件。已有调查记录不受影响。'}</p><div class="toolbar">${btn(x?'查看解析与恢复':'配置补采',x?"deskTool('parse')":"deskTool('collect')",'primary')}${btn('导入本地日志','openLogImport()')}</div></div>`;
 const rows=deskRows(),modules=[...new Set(rows.map(r=>r.module))];
 return `${x.run?.warnings?.length?'<div class="reader-coverage">'+x.run.warnings.map(esc).join('；')+'<br>实际记录：'+esc(x.run.records[0]?.time||'')+' — '+esc(x.run.records.at(-1)?.time||'')+'</div>':''}${deskCoverage(x)}<div class="reader-topline"><b>事件时间线</b><span>${rows.length} 条事件 · ${rows.filter(r=>['ERROR','FATAL'].includes(r.level)).length} 条错误</span></div><div id="desk-minimap"></div><div class="desk-filters"><input id="desk-query" aria-label="日志关键词" placeholder="搜索事件、requestId、错误信息…" value="${esc(query)}" oninput="query=this.value;contextMode=false;renderLogs()"><select aria-label="日志级别" onchange="level=this.value;contextMode=false;renderLogs()">${['全部','ERROR','WARN','INFO','DEBUG','FATAL'].map(l=>`<option ${l===level?'selected':''}>${l}</option>`).join('')}</select><select aria-label="日志模块" onchange="desk.module=this.value;contextMode=false;renderLogs()"><option value="">全部模块</option>${modules.map(m=>`<option value="${esc(m)}" ${desk.module===m?'selected':''}>${esc(m)}</option>`).join('')}</select><details class="time-filter"><summary>时间范围</summary><label>起始时刻<input type="time" step="0.001" aria-label="起始时刻" value="${esc(desk.from)}" onchange="desk.from=this.value;contextMode=false;renderLogs()"></label><label>结束时刻<input type="time" step="0.001" aria-label="结束时刻" value="${esc(desk.to)}" onchange="desk.to=this.value;contextMode=false;renderLogs()"></label></details>${btn('清除',"deskResetFilter();render()")}</div><div class="reader-resultline"><small id="count"></small><div>${btn('加入筛选结果',"deskAddRows('filtered')",'textbutton')}${btn('用所选日志提问',"deskAsk()",'textbutton')}</div></div><div id="context-banner"></div><div id="logs" class="desk-log-list"></div><div id="detail" class="desk-log-detail"></div>`;
}
renderLogs=function(){
 if(!['logcenter','workbench'].includes(page)||!$('#desk-minimap'))return;
 const all=deskRows(),found=deskFiltered(),shown=contextMode?all:found;
 $('#count').textContent=(contextMode?'原始上下文':'筛选结果')+' '+shown.length+' / '+all.length+' · 已选 '+deskSelection().length;
 $('#context-banner').innerHTML=contextMode?`<div class="notice">原筛选已保留 · 正在查看本份日志全部上下文 ${btn('返回筛选结果',"contextMode=false;renderLogs()",'textbutton')}</div>`:'';
 const bins=D.timeBins(all),max=Math.max(1,...bins.map(b=>b.rows.length));
 $('#desk-minimap').innerHTML=`<div class="desk-bars" aria-label="事件时间分布">${bins.map((b,n)=>`<button ${!b.rows.length?'disabled':''} aria-label="${b.rows.length?'定位 '+D.clockLabel(b.rows[0].time)+'，'+b.rows.length+' 条事件':'此区间无事件'}" title="${b.rows.length} 条事件 / ${b.errors} 条错误" class="${b.errors?'has-errors':''} ${b.rows.some(r=>r.n===selected)?'at-event':''}" onclick="deskJump(${b.rows[0]?.n??0})"><span style="height:${Math.max(5,b.rows.length/max*100)}%"></span></button>`).join('')}</div><div class="desk-time-labels"><span>${esc(D.clockLabel(all[0]?.time||''))}</span><span>点击刻度定位 · 红色为错误</span><span>${esc(D.clockLabel(all.at(-1)?.time||''))}</span></div>`;
 $('#logs').innerHTML=shown.length?shown.map(r=>`<div class="desk-log-row ${selected===r.n?'selected':''}"><input type="checkbox" aria-label="选择事件 L${r.n+1}" ${deskSelection().includes(r.n)?'checked':''} onchange="deskCheck(${r.n},this.checked)"><button onclick="deskSelectEvent(${r.n})" aria-label="日志 L${r.n+1} ${esc(r.module)}"><span class="event-clock">${esc(D.clockLabel(r.time))}<small>L${r.n+1}</small></span><span class="event-body"><span class="event-meta"><b class="event-level ${['ERROR','FATAL'].includes(r.level)?'error':''}">${esc(r.level)}</b><span>${esc(r.module)}</span></span><span>${esc(r.content)}</span></span></button></div>`).join(''):'<div class="empty"><h3>没有匹配事件</h3><p>可调整关键词、级别、模块和时间，原日志仍然保留。</p>'+btn('清除筛选',"deskResetFilter();render()")+'</div>';
 const r=shown.find(r=>r.n===selected);
 $('#detail').innerHTML=r?`<div class="reader-topline"><b>L${selected+1} · 原始事件</b><small>${esc(currentTask().id)}</small></div><pre>${esc(r.content)}</pre><div class="toolbar">${btn('加入调查证据','addLog()')}${btn('查看关联源码',"sourceMode=recordsForTask()[selected].file&&recordsForTask()[selected].sourceVerified!==false?'matched':'candidate';deskTool('source')")}${btn('解释此事件',"deskAsk(true)",'primary')}${btn(contextMode?'返回筛选结果':'展开原始上下文',`contextMode=${!contextMode};renderLogs()`)}${requestLink(r)}</div><details><summary>原始字段、文件位置与版本</summary><dl><dt>模块</dt><dd>${esc(r.module)}</dd><dt>源码线索</dt><dd>${esc(r.file?r.file+':'+r.line:'没有文件映射')}</dd><dt>原始位置</dt><dd>${esc(r.sourceName||currentTask().id)} · ${esc(r.originLine||'L'+(selected+1))}</dd><dt>版本 / 构建</dt><dd>${esc(currentTask().version||issue().version)} / ${esc(currentTask().build||issue().build)}</dd></dl>${r.fields?`<pre>${esc(JSON.stringify(r.fields,null,2))}</pre>`:''}</details>`:'<div class="empty">选择一条命中事件查看详情。其他事件不会冒充当前命中。</div>';
 if(desk.tool==='source'&&$('.desk-tool-body'))$('.desk-tool-body').innerHTML=deskPanel(deskSource());
 deskSave();
};
function deskSelectEvent(n){selected=n;render();}
function deskJump(n){selected=n;contextMode=true;render();document.querySelector('.desk-log-row.selected')?.scrollIntoView({block:'nearest'});}
function deskCheck(n,on){let key=currentTask().id,list=deskSelection();desk.checked[key]=on?[...new Set([...list,n])]:list.filter(v=>v!==n);renderLogs();}
function deskAddRows(mode){const t=currentTask();if(!deskSource()?.request.task)return;let ids=mode==='filtered'?deskFiltered().map(r=>r.n):mode==='current'?[selected]:deskSelection().length?deskSelection():[selected];ids=ids.filter(n=>availableIndices().includes(n));if(!ids.length){toast('没有可加入的事件');return false;}let s=investigation(),count=0;ids.forEach(n=>{let e=logEvidence(t.id,n);if(AI.add(s,e))count++;if(!issue().evidence.some(v=>v.id===e.id))issue().evidence.push({...e});});save();render();toast('已加入 '+count+' 条日志证据；历史回答保持原快照');return true;}
function deskAsk(current=false){if(!deskSource()?.request.task)return toast('先打开可读日志');if((current||!deskSelection().length)&&!(contextMode?deskRows():deskFiltered()).some(r=>r.n===selected))return toast('请先选择一条命中日志');if(!deskAddRows(current?'current':'selected'))return;desk.tool='chat';workUI.issueMode='chat';desk.issueKeys[state.active]=deskSource()?.key;const s=investigation();if(!s.draft)s.draft='请结合当前日志证据，解释'+(current?'这条事件':'问题现象')+'，区分事实、候选原因和需要补充的信息。';page='workbench';location.hash='workbench';deskSave();render();$('#chat-question')?.focus();}
function deskPanel(x){
 switch(desk.tool){
 case 'parse':return `<div class="tool-heading"><span class="eyebrow">FILES & PARSING</span><h2>文件与解析历史</h2></div>${x?parseView(x):'<div class="empty">暂无文件。可先导入或配置补采。</div>'+btn('导入日志','openLogImport()')}`;
 case 'source':return `<div class="tool-heading"><span class="eyebrow">SOURCE CONTEXT</span><h2>与事件一起读代码</h2><p>日志选择与代码定位保持同步。</p></div>${x?.request.task&&((contextMode?deskRows():deskFiltered()).some(r=>r.n===selected))?inlineSource():'<div class="empty">先选择一条可读事件，查看对应版本的源码线索。</div>'}`;
 case 'evidence':return `<div class="tool-heading"><span class="eyebrow">EVIDENCE</span><h2>下轮分析依据</h2></div>${evidencePanel()}${btn('粘贴补充材料','pasteEvidence()')}`;
 case 'collect':return deskCollectionPanel();
 case 'hypotheses':return `<div class="tool-heading"><span class="eyebrow">VALIDATION</span><h2>验证当前判断</h2><p>记录观察，区分支持、反驳与无法确定。</p></div>${verificationPlan()+hypothesesPanel()}<div class="toolbar">${btn('结合验证结果继续追问',"suggestQuestion('结合新增验证结果，重新检查当前假设和未知项。')",'primary')}${btn('整理回写草稿',"deskTool('handoff')")}</div>`;
 case 'handoff':return `<div class="tool-heading"><span class="eyebrow">TICKET UPDATE</span><h2>将调查结果交给下一位</h2><p>保存结论、未知项、证据快照和验证记录。</p></div>${inlineHandoff()}`;
 case 'result':return deskResult();
 default:return deskChat();
 }
}
function deskChat(){const s=investigation();return `<div class="tool-heading"><span class="eyebrow">AI INVESTIGATION · 本地模拟</span><h2>围绕证据继续排查</h2><p>${s.context.length} 项证据 · ${lastAnswer(s)?.answer.proposals?.length??s.hypotheses.length} 个本轮检验方向 · ${s.hypotheses.length} 个历史假设</p></div>${deskSource()?.request.task&&!s.context.some(e=>e.task===deskSource().request.task)?'<div class="notice">当前阅读的日志尚未加入分析范围。可在左侧选取事件后提问。</div>':''}${currentRevisionNote(s)}<div class="desk-ai-actions">${btn('查看分析范围',"deskTool('evidence')")}${btn('补采缺失日志',"deskTool('collect')")}</div><div id="conversation">${conversationMarkup(s)||'<div class="empty">先在左侧选取事件，或直接描述需要澄清的问题。</div>'}</div>${composer()}`;}
composer=function(){let s=investigation(),run=runningRound(s);return `<div class="composer panel"><h3>继续提问</h3><div class="question-chips">${['已有证据能确认什么？','还有哪些反证或未知项？','下一步应该补采什么日志？'].map(q=>btn(q,`suggestQuestion('${q}')`,'chip',!!run)).join('')}</div><form id="chat-form" onsubmit="sendQuestion(event)"><label for="chat-question">你的问题或追问</label><textarea id="chat-question" ${run?'disabled':''} maxlength="1600" placeholder="结合左侧事件，继续追问…" oninput="investigation().draft=this.value;save()" onkeydown="if(event.key==='Enter'&&!event.shiftKey&&!event.isComposing){event.preventDefault();this.form.requestSubmit()}">${esc(s.draft||'')}</textarea><div id="chat-error" class="error" role="alert"></div><small>将使用 ${s.context.length} 项证据快照 · Enter 发送</small><div class="toolbar"><button type="button" onclick="attachmentPicker('log')">补日志</button><button type="button" onclick="attachmentPicker('source')">补源码</button><button class="primary" ${run?'disabled':''}>${run?'正在生成…':'发送问题'}</button></div></form></div>`;};
suggestQuestion=function(q){investigation().draft=q;desk.tool='chat';workUI.issueMode='chat';save();if(page==='logcenter'){desk.issueKeys[state.active]=deskSource()?.key;page='workbench';location.hash='workbench';}render();$('#chat-question')?.focus();};
analysisForm=function(){deskAsk(true);};
showHypotheses=function(){deskTool('hypotheses');};
chatTicket=function(roundId){try{draftHandoff=issue().handoffDraft?.snapshot||AI.handoff(investigation(),roundId||lastAnswer()?.id);deskTool('handoff');}catch(e){toast(e.message);}};
const deskOldSaveTicket=saveChatTicket;
saveChatTicket=function(e){deskSaveOrigin=page;deskOldSaveTicket(e);deskSaveOrigin=null;};
function deskCollectionPanel(){
 const i=issue(),candidates=collectionSources().filter(x=>x.issue.id===i.id),x=candidates.find(x=>x.key===workUI.collectionKey)||candidates.at(-1),a=lastAnswer();
 return `<div class="tool-heading"><span class="eyebrow">COLLECT MORE EVIDENCE</span><h2>补充采集</h2><p class="muted">针对当前问题缺少的时间段或日志类型，再次回捞已有日志。返回后选择新增证据，继续调查。</p><p>${esc(a?.answer.unknown||'确认设备、故障时间与日志类型，返回文件自动关联当前问题。')}</p></div>${btn('补充采集','deskStartCollection()','primary')}<p class="muted">工单设备线索（非推送目标）${esc(i.device)} · ${esc(i.begin.replace('T',' '))} — ${esc(i.end.slice(11))}</p>${x?`<label>此问题的回捞记录<select aria-label="补采记录" onchange="workUI.collectionKey=this.value;render()">${candidates.map(c=>`<option value="${c.key}" ${c.key===x.key?'selected':''}>${c.request.id} · ${esc(c.request.status)}</option>`).join('')}</select></label>${x.request.task?`<div class="notice"><b>返回日志已可读</b><p>打开后选择新证据，再追问；之前的回答和阅读范围保留。</p>${btn('打开补采日志',`deskChoose('${x.key}');deskTool('chat')`,'primary')}</div>`:''}${deskCollectionBody(x)}`:'<div class="empty">尚无回捞请求。配置完成后会在此追踪推送和返回文件。</div>'}`;
}
function deskCollectionBody(x){syncCollection(x);workUI.collectionMode='files';let html=inlineCollection(x);return html+(x.request.status==='解析失败'?`<div class="notice">文件已返回，优先重新解析，避免再次采集。${btn('查看解析并恢复',`openParsing('${x.key}')`)}</div>`:'');}
function deskStartCollection(){deskCollectOrigin=page;startAcquisitionFromWorkspace(issue().id);$('#new-capture-issue').disabled=true;}
const deskOldSubmitCapture=submitInlineCollection;
submitInlineCollection=function(e){deskSubmittingCapture=true;try{deskOldSubmitCapture(e);}finally{deskSubmittingCapture=false;if(!$('#modal').open)deskCollectOrigin=null;}};
const deskOldClose=closeModal;
closeModal=function(){deskOldClose();if(!deskSubmittingCapture)deskCollectOrigin=null;};
const deskOldRunCollection=runInlineCollection;
runInlineCollection=function(key){if(!['logcenter','workbench'].includes(page))return deskOldRunCollection(key);if(demoRuns.has(key))return;let x=fullLogSources().find(x=>x.key===key);let timer=setInterval(()=>{M.advance(x.request);if(M.terminal.includes(x.request.status)){clearInterval(timer);demoRuns.delete(key);}save();if(state.active===x.issue.id&&['workbench','logcenter'].includes(page))render();},550);demoRuns.set(key,timer);render();};
const deskOldRetry=retryInlineCollection;
retryInlineCollection=function(key){if(['workbench','logcenter'].includes(page)){desk.tool='collect';deskOldRetry(key);return;}deskOldRetry(key);};
function deskResult(){let i=issue(),a=lastAnswer(),latest=i.updates[0];return `<div class="tool-heading"><span class="eyebrow">ISSUE / ${esc(i.id)}</span><h2>问题与处理结果</h2><p>${esc(i.owner)} · ${esc(i.priority)} · ${esc(i.status)}</p></div><div class="panel"><h3>问题现象</h3><p>${esc(i.description)}</p>${btn('编辑工单','editIssue()')}</div><div class="panel"><h3>最近保存的结果</h3>${latest?`<small>${esc(latest.time)} · ${esc(latest.owner)}</small><p>${esc(latest.investigation?.body||latest.summary.slice(0,260))}</p><p>${latest.evidence.length} 项证据 · ${latest.investigation?.verifications.length||0} 条验证 · ${latest.verified?'修复验证已记录':'修复仍待验证'}</p><details><summary>查看完整保存内容</summary><p>${esc(latest.summary)}</p></details>`:'<p>还没有保存处理结果。完成排查后可生成草稿。</p>'}${btn('继续整理工单结果',"deskTool('handoff')",'',!a)}</div><details class="panel"><summary>全部处理记录 · ${i.updates.length} 次</summary>${i.updates.map(u=>`<details class="update"><summary>${esc(u.time)} · ${esc(u.owner)} · ${esc(u.status)}</summary><p>${esc(u.summary)}</p><small>${u.evidence.length} 项证据 · ${u.verified?'修复验证已记录':'修复仍待验证'}</small></details>`).join('')}</details><div class="toolbar">${btn('导出对话与调查','exportConversation()')}${btn('返回 AI 排查',"deskTool('chat')")}</div>`;}
views.logcenter=()=>deskWorkspace(false);
views.workbench=()=>deskWorkspace(true);
views.issues=function(){if(issueDetail)return deskWorkspace(true);return `<div class="desk-page-heading"><div><span class="eyebrow">ISSUES & INVESTIGATIONS</span><h1>问题调查</h1><p class="muted">接着上一次判断，推动问题走向解决。</p></div>${btn('新建工单','newIssue()','primary')}</div><div class="toolbar"><input aria-label="搜索工单" placeholder="问题、设备或工单编号" oninput="searchIssues(this.value)"></div><div class="desk-issue-list">${state.issues.map(i=>{let a=i.investigation?.rounds.filter(r=>r.status==='done').at(-1);return `<article data-issue-search="${esc(i.id+i.title+i.device)}"><div><small>${esc(i.id)} · ${esc(i.priority)}</small><h2>${esc(i.title)}</h2><p>${esc(i.device)} · ${esc(i.owner)} · ${esc(i.begin.replace('T',' '))}</p><p class="issue-next"><span>下一步</span> ${esc(a?.answer.next||(M.tasks(i).length?'阅读日志，确认异常范围。':'获取故障时间段日志。'))}</p></div><div>${badge(i.status)}${btn('继续调查',`deskOpenIssue('${i.id}')`,'primary')}<small>${i.investigation?.context.length??i.evidence.length} 项证据 · ${i.updates.length} 次更新</small></div></article>`;}).join('')}<p id="empty-search" hidden>没有匹配问题，可修改关键词或新建工单。</p></div>`;};
searchIssues=function(q){let count=0;document.querySelectorAll('[data-issue-search]').forEach(e=>{e.hidden=!e.dataset.issueSearch.toLowerCase().includes(q.toLowerCase());if(!e.hidden)count++;});$('#empty-search').hidden=!!count;};
render=function(){
 const activeTool=document.activeElement?.id,selectionStart=document.activeElement?.selectionStart;
 const oldTool=$('.desk-assistant')?.dataset.tool,oldToolScroll=$('.desk-tool-body')?.scrollTop||0,oldSource=$('.desk-reader')?.dataset.sourceKey,oldLogScroll=$('#logs')?.scrollTop||0;
 document.body.classList.add('desk-app');
 if(page==='issues'&&issueDetail){page='workbench';location.hash='workbench';}
 if(['logcenter','workbench'].includes(page))workUI.logMode='read';
 $('#view').innerHTML=views[page]();
 const section=['logcenter','logs','parsing'].includes(page)?'logcenter':page==='collectioncenter'?'collectioncenter':'issues';
 $('#nav').innerHTML=[['logcenter','日志阅读','≋'],['collectioncenter','日志回捞','⇣'],['issues','问题调查','◎']].map(([key,label,icon])=>`<button class="${section===key?'active':''}" ${section===key?'aria-current="page"':''} onclick="${key==='issues'?"issueDetail=false;go('issues')":`go('${key}')`}"><span>${icon}</span>${label}</button>`).join('');
 $('#crumb').textContent={logcenter:'日志阅读',workbench:'问题调查',issues:'问题调查',collectioncenter:'日志回捞'}[page]||names[page];
 $('#global-status').textContent=storageOK?'本地原型 · 已自动保存':'存储不足 · 当前变更未保存';
 if(['logcenter','workbench'].includes(page)){renderLogs();if(desk.library)deskFilterLibrary();}
 if(page==='collectioncenter'){let input=$('#rail-collection');if(input)filterRail(input,'collection');}
 deskSave();
 if(oldTool===desk.tool&&$('.desk-tool-body'))$('.desk-tool-body').scrollTop=oldToolScroll;
 if(oldSource===$('.desk-reader')?.dataset.sourceKey&&$('#logs'))$('#logs').scrollTop=oldLogScroll;
 if(activeTool==='chat-question'&&$('#chat-question')&&!$('#chat-question').disabled){$('#chat-question').focus({preventScroll:true});if(selectionStart!=null)$('#chat-question').setSelectionRange(selectionStart,selectionStart);}
};
// Respect older routes and selected history. Prefer reading on the first redesigned visit.
if(!state.deskUI){desk.tool='source';workUI.logMode='read';}
if(page==='workbench'&&!state.deskUI){desk.tool='chat';workUI.issueMode='chat';}
const restoredFocus=desk.focusBySource?.[deskSource()?.key];
if(restoredFocus){selected=restoredFocus.selected;query=restoredFocus.query||'';level=restoredFocus.level||'全部';contextMode=!!restoredFocus.contextMode;desk.module=restoredFocus.module||'';desk.from=restoredFocus.from||'';desk.to=restoredFocus.to||'';}
workUI.issueMode=desk.tool;
render();

runParse=function(id,run){let key=id+'/'+run;if(parseTimers.has(key))return;let a=findAsset(id),r=a.runs.find(r=>r.id===run);let timer=setInterval(()=>{let persisted=persistParseAdvance(a,r);if(!persisted||!['待解析','解析中'].includes(r.status)){clearInterval(timer);parseTimers.delete(key);}if(['logcenter','workbench'].includes(page)&&deskSource()?.key===key)render();},550);parseTimers.set(key,timer);render();};

showGuide=function(){openModal('连续排查演练',`<ol class="guide"><li>在日志阅读中切换资料；点击时间刻度、筛选事件、勾选需要的日志。</li><li>选中事件，右侧打开源码；将对应日志和源码加入证据。</li><li>点击用所选日志提问。AI 使用明确的证据快照，后续补充不会改写历史回答。</li><li>在补采中配置缺少的材料，追踪推送和返回文件。新日志就绪后明确打开，再补证追问。</li><li>在验证中记录步骤与实际观察，继续追问或编辑回写草稿。保存后，问题与记录中查看最新结果和完整历史。</li><li>上传、Web 样例和手动片段在更多操作；解析失败和历史批次在解析与历史。</li></ol><p>文件解析在本地执行，推送、源码与 AI 为模拟。旧工单、批次、证据和回写记录保留。</p>${btn('开始查看','closeModal()','primary')}${btn('重置所有演示记录','resetDemo()')}`);};

const deskSourceMarkup=inlineSource;
inlineSource=function(){let html=deskSourceMarkup().replace('>加入调查证据</button>','>将源码加入证据</button>').replace('<div class="source-controls">','<details class="source-options"><summary>定位与版本选项</summary><div class="source-controls">').replace('</div><div class="evidence">','</div></details><div class="evidence">').replace('class="badge warn">版本匹配（演示）','class="badge ok">版本匹配（演示）');let r=recordsForTask()[selected];return r?.scene==='generic'&&!r.file?html.replace('NetworkMonitor.swift','未提供源码路径'):html;};
addSource=function(){
 const t=currentTask(),r=recordsForTask()[selected];if(!t||!r)return;
 if(!r.file||r.sourceVerified===false||!t.version||!t.build)sourceMode='candidate';
 const text=code(r).replace(/&quot;/g,'"').replace(/&#39;/g,"'").replace(/&lt;/g,'<').replace(/&gt;/g,'>').replace(/&amp;/g,'&');
 addEvidence({...evidenceMetadata(t,r),id:t.id+'-S'+selected+'-'+sourceMode,kind:'source',index:selected,task:t.id,label:'S'+(selected+1)+' · '+(r.file||'模块候选'),text,match:sourceMode==='matched'?'版本匹配（虚构样例）':'未验证的关键词候选',mode:sourceMode,commit:sourceMode==='matched'?'demo-c81（虚构提交）':'未验证'});
};
