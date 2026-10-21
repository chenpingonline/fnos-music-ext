import { computed, ref } from 'vue'
import { ApiError, errorMessage, request } from './api.ts'

type Draft = {id:string;qr_url:string}
type Check = {state:string;logged_in:boolean;saved?:boolean}
export function useAccountEnrollment() {
 const id=ref(''), qrImage=ref(''), error=ref(''), busy=ref(false), state=ref(''), saved=ref(false)
 let generation=0, controller:AbortController|undefined, poll:ReturnType<typeof setTimeout>|undefined
 const status=computed(()=>saved.value?'登录成功，账号已保存':state.value==='scanned'?'已扫码，请在手机上确认':state.value==='expired'?'二维码已过期，请重新生成':'等待使用网易云音乐 App 扫码')
 function schedule(){clearTimeout(poll);if(id.value&&qrImage.value&&!saved.value&&state.value!=='expired')poll=setTimeout(()=>void check(),2500)}
 async function image(url:string){
  const parsed=new URL(url)
  if(parsed.protocol!=='https:'||parsed.hostname!=='music.163.com')throw new Error('登录链接无效，请重试。')
  const {default:QRCode}=await import('qrcode')
  return QRCode.toDataURL(parsed.href,{width:224,margin:3,errorCorrectionLevel:'M',color:{dark:'#111111',light:'#ffffff'}})
 }
 async function start(name:string){
  if(busy.value)return
  const current=++generation;controller=new AbortController();busy.value=true;error.value='';clearTimeout(poll)
  try{
   const result=id.value?await request<{qr_url:string}>('/account-enrollments/'+id.value+'/qr','POST',{},controller.signal):await request<Draft>('/account-enrollments','POST',{name},controller.signal)
   if(current!==generation)return
   if('id' in result && typeof result.id==='string')id.value=result.id
   const png=await image(result.qr_url);if(current!==generation)return
   qrImage.value=png;state.value='pending';saved.value=false
  }catch(reason){if(current===generation){error.value=errorMessage(reason);if(reason instanceof ApiError&&reason.status===410){id.value='';qrImage.value='';state.value='expired'}}}
  finally{if(current===generation){busy.value=false;schedule()}}
 }
 async function check(){
  if(busy.value||!id.value||saved.value)return
  const current=generation;controller=new AbortController();busy.value=true;error.value='';clearTimeout(poll)
  try{
   const result=await request<Check>('/account-enrollments/'+id.value+'/check','POST',{},controller.signal)
   if(current!==generation)return
   state.value=result.state
   saved.value=!!result.saved&&result.logged_in
  }catch(reason){if(current===generation){error.value=errorMessage(reason);if(reason instanceof ApiError&&reason.status===410){state.value='expired';id.value='';qrImage.value=''}}}
  finally{if(current===generation){busy.value=false;schedule()}}
 }
 function close(){
  ++generation;controller?.abort();clearTimeout(poll)
  const previous=id.value;id.value='';qrImage.value='';state.value='';error.value='';busy.value=false;saved.value=false
  if(previous)void request('/account-enrollments/'+previous,'DELETE',{}).catch(()=>{/* Server expires abandoned in-memory drafts. */})
 }
 return {id,qrImage,error,busy,state,saved,status,start,check,close}
}
