(function(root){
'use strict';
function groupSources(sources){
  const groups=new Map();
  for(const s of sources){
    const id=s.asset?s.issue.id+'/'+s.asset.id:s.key;
    if(!groups.has(id))groups.set(id,{id,sources:[],latest:s,readable:null});
    const g=groups.get(id);g.sources.push(s);g.latest=s;if(s.request.task)g.readable=s;
  }
  return [...groups.values()];
}
function chooseSource(sources,key,issueId){
  const scoped=issueId?sources.filter(x=>x.issue.id===issueId):sources;
  return scoped.find(x=>x.key===key)||scoped.filter(x=>x.request.task).at(-1)||scoped.at(-1)||null;
}
function clockValue(time){
  const m=String(time).match(/(?:T|^)(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?/);
  return m?(Number(m[1])*3600+Number(m[2])*60+Number(m[3]))*1000+Number((m[4]||'').padEnd(3,'0').slice(0,3)):null;
}
function clockLabel(time){const m=String(time).match(/(?:T|^)(\d{2}:\d{2}:\d{2}(?:\.\d+)?)/);return m?m[1]:String(time);}
function filterEvents(rows,{query='',level='全部',module='',from='',to=''}={}){
  const start=from?clockValue(from.length===5?from+':00':from):null,end=to?clockValue(to.length===5?to+':59.999':to):null;
  return rows.filter(r=>{
    const t=clockValue(r.time);
    return (level==='全部'||r.level===level)&&(!module||r.module===module)&&JSON.stringify(r).toLowerCase().includes(query.toLowerCase())&&(start===null||(t!==null&&t>=start))&&(end===null||(t!==null&&t<=end));
  });
}
function timeBins(rows,count=24){
  const timed=rows.filter(r=>clockValue(r.time)!==null),values=timed.map(r=>clockValue(r.time));
  if(!timed.length)return [];
  const min=Math.min(...values),max=Math.max(...values),bins=Array.from({length:count},()=>({rows:[],errors:0}));
  timed.forEach(r=>{const n=Math.min(count-1,Math.floor((clockValue(r.time)-min)/Math.max(1,max-min)*(count-1)));bins[n].rows.push(r);if(['ERROR','FATAL'].includes(r.level))bins[n].errors++;});
  return bins;
}
root.LoganDesk={groupSources,chooseSource,clockValue,clockLabel,filterEvents,timeBins};
if(typeof module!=='undefined')module.exports=root.LoganDesk;
})(typeof globalThis!=='undefined'?globalThis:this);
