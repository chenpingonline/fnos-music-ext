const base = '/app/fnmusic-go/api'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) { super(message); this.status = status }
}

async function responseError(response:Response):Promise<never>{
 const data=await response.json().catch(()=>null)
 if(data?.code==='qr_state_unexpected'&&Number.isSafeInteger(data.upstream_code))throw new ApiError(response.status,`网易云扫码接口返回异常状态码 ${data.upstream_code}，请重新生成二维码后重试。`)
 const codes:Record<string,string>={account_already_assigned:'此网易云账号已分配给其他用户或旧版绑定，请先解除原分配，或切换共享模式。',admin_required:'当前操作需要飞牛管理员权限，请确认登录身份。',management_request_rejected:'写入请求校验失败，请刷新页面后重试。'}
 const messages:Record<number,string>={401:'登录已失效，请重新登录飞牛后打开应用。',403:'当前操作需要飞牛管理员权限，请确认登录身份。',409:'账号标识或桥接地址已存在，请修改后重试。',410:'登录二维码已过期，请重新生成。',429:'检查过于频繁，请稍等 2 秒再试。',502:'音源暂时无法响应，请检查连接或稍后重试。'}
 throw new ApiError(response.status,codes[data?.code]??messages[response.status]??data?.error??`请求失败（HTTP ${response.status}）`)
}

export async function request<T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> {
  const controller = new AbortController()
  let timedOut = false
  const abort = () => controller.abort()
  if (signal?.aborted) abort()
  else signal?.addEventListener('abort', abort, {once: true})
  const timeout = setTimeout(() => { timedOut = true; controller.abort() }, 20_000)
  const send=async(url:string,options:RequestInit)=>{
   try{return await fetch(base+url,{...options,signal:controller.signal,credentials:'same-origin',cache:'no-store'})}
   catch(error){if(signal?.aborted)throw error;throw new Error(timedOut?'请求超时，请检查服务状态后重试。':'无法连接服务，请检查应用是否正在运行。')}
  }
  try {
    let headers:Record<string,string>|undefined
    if(method!=='GET'){
     // Fetch afresh for each write: a service restart invalidates old tokens.
     const challenge=await send('/csrf',{method:'GET'})
     if(!challenge.ok)await responseError(challenge)
     const data=await challenge.json().catch(()=>null) as {token?:unknown}|null
     if(typeof data?.token!=='string'||!/^[A-Za-z0-9_-]{32,128}$/.test(data.token))throw new Error('无法获取写入校验信息，请刷新页面后重试。')
     headers={'Content-Type':'application/json','X-FnMusic-Admin':'1','X-FnMusic-CSRF':data.token}
    }
    const response=await send(path,{method,headers,body:method==='GET'?undefined:JSON.stringify(body??{})})
    if(!response.ok)await responseError(response)
    return await response.json() as T
  } finally {
    clearTimeout(timeout)
    signal?.removeEventListener('abort', abort)
  }
}

export function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : '操作未完成，请重试。'
}
