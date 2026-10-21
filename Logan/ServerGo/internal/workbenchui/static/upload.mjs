import {BASE, APIError} from './api.mjs';

// This inspects actual bounded file bytes, not a filename extension. Record-level
// format errors remain parser decisions so skip-invalid mode stays available.
export async function preflightFiles(files, limits={}, format='auto') {
 if(!files.length) throw new Error('请选择日志文件');
 if(files.length>(limits.files||10)) throw new Error(`单批最多 ${limits.files||10} 个文件`);
 let total=0; const report=[];
 for(const file of files) {
  if(!file.size) throw new Error(`${file.name} 是空文件`);
  if(limits.fileBytes&&file.size>limits.fileBytes) throw new Error(`${file.name} 超过单文件上限`);
  total+=file.size;
  if(limits.batchBytes&&total>limits.batchBytes) throw new Error('文件正文总量超过单批上限');
  const bytes=new Uint8Array(await file.arrayBuffer());
  if(format!=='native'&&format!=='web') {
   if(bytes.includes(0)||bytes[0]===31&&bytes[1]===139||bytes[0]===80&&bytes[1]===75&&bytes[2]===3&&bytes[3]===4) throw new Error(`${file.name} 不是可直接导入的 UTF-8 明文；请使用已配置的对应协议`);
   let content;try{content=new TextDecoder('utf-8',{fatal:true}).decode(bytes);}catch{throw new Error(`${file.name} 不是有效 UTF-8 明文`);}
   let warning='';
   if(format==='json') {try{if(!Array.isArray(JSON.parse(content)))warning='JSON 顶层不是数组，解析可能失败';}catch{warning='JSON 语法待解析器检查';}}
   if(format==='jsonl') {const sampled=content.split(/\r?\n/).filter(x=>x.trim()).slice(0,10);if(sampled.some(x=>{try{JSON.parse(x);return false;}catch{return true;}}))warning='前 10 条非空记录有格式错误，可选择跳过坏记录解析';}
   report.push({name:file.name,size:file.size,warning});
  }else report.push({name:file.name,size:file.size,warning:'协议完整性由服务端解析器核验'});
 }
 return {files:report,total};
}

// Progress comes only from browser upload byte events. Abort means transport was
// stopped; the caller checks the durable client batch before suggesting a retry.
export function uploadRequest({body,token,project,key,onProgress=()=>{},signal,xhrFactory=()=>new XMLHttpRequest()}) {
 return new Promise((resolve,reject)=>{
  const xhr=xhrFactory();let finished=false;
  const finish=(fn,value)=>{if(finished)return;finished=true;signal?.removeEventListener('abort',abort);fn(value);};
  const abort=()=>xhr.abort();
  xhr.open('POST',BASE+'/imports');
  xhr.setRequestHeader('Authorization','Bearer '+token);
  xhr.setRequestHeader('X-Project-ID',project);
  xhr.setRequestHeader('Idempotency-Key',key);
  xhr.upload.onprogress=e=>onProgress({loaded:e.loaded,total:e.lengthComputable?e.total:null});
  xhr.onload=()=>{let value;try{value=JSON.parse(xhr.responseText);}catch{return finish(reject,new APIError(xhr.status,null));}if(xhr.status<200||xhr.status>=300)return finish(reject,new APIError(xhr.status,value));finish(resolve,value.data);};
  xhr.onerror=()=>finish(reject,new Error('上传连接中断，服务端结果尚待核对'));
  xhr.onabort=()=>finish(reject,Object.assign(new Error('上传传输已取消；服务端可能已收到文件'),{name:'AbortError'}));
  xhr.timeout=120000;xhr.ontimeout=()=>finish(reject,new Error('上传传输超时，服务端结果尚待核对'));
  signal?.addEventListener('abort',abort,{once:true});
  if(signal?.aborted){finish(reject,Object.assign(new Error('上传已取消'),{name:'AbortError'}));return;}
  xhr.send(body);
 });
}
