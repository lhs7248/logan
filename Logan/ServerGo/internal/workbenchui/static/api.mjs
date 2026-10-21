export function idempotencyKey(now=Date.now(), crypto=globalThis.crypto) {
 const bytes=crypto.getRandomValues(new Uint8Array(16)); let time=BigInt(now);
 for(let i=5;i>=0;i--){bytes[i]=Number(time & 255n);time >>= 8n;}
 bytes[6]=(bytes[6]&15)|112;bytes[8]=(bytes[8]&63)|128;
 const hex=[...bytes].map(x=>x.toString(16).padStart(2,'0')).join('');
 return `${hex.slice(0,8)}-${hex.slice(8,12)}-${hex.slice(12,16)}-${hex.slice(16,20)}-${hex.slice(20)}`;
}
export const BASE = '/logan-web/api/workbench/v1';
export class APIError extends Error {
 constructor(status, body) { super(body?.error?.message || `请求失败 (${status})`); this.status=status; this.code=body?.error?.code || 'HTTP_ERROR'; this.traceId=body?.meta?.traceId; }
}
export function createAPI({fetcher=globalThis.fetch, token=()=>'', project=()=>''}={}) {
 return async function request(path, {method='GET',body,revision,key,download=false,signal}={}) {
  const headers = {Authorization:`Bearer ${token()}`, 'X-Project-ID':project()};
  if(method!=='GET') headers['Idempotency-Key']=key || idempotencyKey();
  if(revision!==undefined && revision!==null) headers['If-Match']=String(revision);
  const multipart = typeof FormData!=='undefined' && body instanceof FormData;
  if(body!==undefined && !multipart) headers['Content-Type']='application/json';
  const response = await fetcher(BASE+path,{method,headers,body:body===undefined?undefined:multipart?body:JSON.stringify(body),signal});
  if(!response.ok) { let value; try {value=await response.json();} catch {} throw new APIError(response.status,value); }
  if(download) return response.blob();
  if(response.status===204) return null;
  const result=await response.json(); return result.data;
 };
}
export function filtersQuery(filters) { const q=new URLSearchParams(); for(const key of ['query','level','module','type','unknownTime','start','end']) if(filters[key]) q.set(key,filters[key]); return q.toString(); }
export function evidenceDelta(previous=[],current=[]) { return {added:current.filter(id=>!previous.includes(id)),removed:previous.filter(id=>!current.includes(id))}; }
export const readable = run => ['succeeded','partial'].includes(run?.status);
export const active = object => ['queued','running'].includes(object?.status);
export function list(value,key) { return Array.isArray(value)?value:Array.isArray(value?.[key])?value[key]:[]; }

export function supportedFormats(capabilities={}) { return ['auto','jsonl','json','text',...(capabilities.protocols?.native?.configured?['native']:[]),...(capabilities.protocols?.web?.configured?['web']:[])]; }

// Compare identities and recorded observations; never infer a contradiction from
// neighboring timestamps or unrelated log messages.
export function evidenceChanges(previous=[],current=[],verifications=[],now=Date.now()) {
 const before=new Map(previous.map(e=>[e.id,e]));const after=new Map(current.map(e=>[e.id,e]));
 const isExpired=e=>e.availability==='expired'||e.expiresAt&&Date.parse(e.expiresAt)<=now;
 return {
  added:current.filter(e=>!before.has(e.id)).map(e=>e.id),
  removed:previous.filter(e=>!after.has(e.id)).map(e=>e.id),
  changed:current.filter(e=>{const p=before.get(e.id);return p&&(e.availability==='source_changed'||p.contentHash!==e.contentHash||JSON.stringify(p.originRef)!==JSON.stringify(e.originRef));}).map(e=>e.id),
  expired:current.filter(isExpired).map(e=>e.id),
  unavailable:current.filter(e=>['restricted','unavailable'].includes(e.availability)).map(e=>e.id),
  contradicted:verifications.filter(v=>v.purpose==='hypothesis'&&v.result==='refuted'&&!previous.some(e=>e.originRef?.verificationId===v.id)).map(v=>({verificationId:v.id,hypothesisId:v.hypothesisId,hypothesisVersion:v.hypothesisVersion}))
 };
}
