import {idempotencyKey} from './api.mjs';

const statusLabels={pending_dispatch:'待提交内部服务',waiting_push:'已受理，等待推送回调',waiting_logs:'渠道已受理，等待日志',dispatch_unknown:'发送结果未知',push_failed:'推送失败',timed_out:'等待超时',cancelled:'已取消后续处理'};
export const collectionStatusLabel=status=>statusLabels[status]||'状态未知';
export const collectionActive=collection=>['pending_dispatch','waiting_push','waiting_logs','dispatch_unknown'].includes(collection?.status);
export function collectionProgress(collection={},attempt={}) {
 const dispatch={queued:'待提交',sending:'提交中',accepted:'内部服务已受理',rejected:'内部服务拒绝',unknown:'提交结果未知'}[attempt.dispatchStatus]||'尚无受理证据';
 const push={succeeded:'渠道已受理（签名回调确认）',failed:'渠道推送失败'}[attempt.pushStatus]||'无渠道结果回调';
 const result={pending_dispatch:'等待服务受理，未确认回捞',waiting_push:'等待推送结果，未确认回捞',dispatch_unknown:'推送结果未知，未确认回捞',cancelled:'已取消，设备仍可能上传',timed_out:'等待超时，未确认回捞成功',push_failed:'推送失败，未确认回捞成功'}[collection.status]||'等待日志，不能判定回捞成功';
 return [['内部推送服务',dispatch],['下游推送渠道',push],['客户端接收','无客户端接收证据'],['日志回传','尚未关联此次回捞的日志'],['回捞结果',result]];
}

// A transport failure must not turn a deliberate user action into a new send.
// Keys and confirmed payloads are scoped to the in-memory project/session.
export function createCollectionMutations({api,scope,newKey=idempotencyKey}) {
 const keys=new Map();
 return {async submit(path,body,revision) {
  const origin=scope();
  const fingerprint=JSON.stringify([origin,path,body,revision]);
  if(!keys.has(fingerprint))keys.set(fingerprint,newKey());
  const key=keys.get(fingerprint);
  let result;
  try {result=await api(path,{method:'POST',body,revision,key});}
  catch(error) {
   if(origin!==scope())throw new Error('项目或身份已切换，请在原项目检查提交结果。');
   if(error.status)throw error;
   try {
    const operation=await api('/operations?'+new URLSearchParams({key,operation:'POST '+path}));
    if(!operation?.resourceId||!operation?.result?.id)throw new Error('No durable operation');
    result=operation.result;
   } catch {
    throw new Error('提交结果未知，请先刷新请求列表检查；再次确认同一操作时将沿用原操作编号，不会自动重发。');
   }
  }
  if(origin!==scope())throw new Error('项目或身份已切换，请在原项目检查提交结果。');
  return result;
 }};
}
