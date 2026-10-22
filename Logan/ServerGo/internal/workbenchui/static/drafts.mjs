// A single server-backed editing stream per actor/issue. Writes are serialized,
// preserve server revisions, and never replace local edits after a failed save.
export function createDraftAutosaver({draft=null,create,patch,onState=()=>{},delay=450}) {
 let current=draft,values=draft?pick(draft):null,dirty=false,saving=null,error=null,timer=null,disposed=false;
 const state=()=>({draft:current,values,dirty,saving:Boolean(saving),error});
 const emit=()=>onState(state());
 function change(next){if(disposed)return;values=pick(next);dirty=true;error=null;clearTimeout(timer);timer=setTimeout(()=>{flush().catch(()=>{});},delay);emit();}
 function updateDraft(next){if(!saving&&next&&(!current||next.revision>=current.revision)){current=next;if(!dirty)values=pick(next);emit();}}
 async function flush(){
  clearTimeout(timer);
  if(disposed)return current;
  if(saving){await saving;if(dirty)return flush();return current;}
  if(!dirty||!values)return current;
  const sent={...values};const signature=JSON.stringify(sent);
  saving=(async()=>{
   try{current=current?await patch(current,sent):await create(sent);error=null;dirty=!disposed&&JSON.stringify(values)!==signature;}
   catch(e){error=e;dirty=true;throw e;}
   finally{saving=null;emit();}
  })();emit();
  await saving;
  if(dirty)return flush();
  return current;
 }
 function dispose(){disposed=true;clearTimeout(timer);values=null;dirty=false;}
 return {change,flush,updateDraft,state,dispose};
}
export function pick(value={}) {return {mode:value.mode||'manual',turnId:value.turnId||'',summary:value.summary||'',status:value.status||'investigating',owner:value.owner||''};}
