// Local UI preview only. Responses live in memory and never reach a NAS or music service.
import { createServer } from 'vite'
import { readFileSync } from 'node:fs'
import QRCode from 'qrcode'
const qqImage=await QRCode.toDataURL('qqmusic-local-preview-only')
const challenge=source=>source==='qqmusic'?{qr_image:qqImage}:{qr_url:'https://music.163.com/login?codekey=local-ui-preview-only'}
const version=readFileSync(new URL('../../packaging/fpk/manifest.in',import.meta.url),'utf8').match(/^version\s*=\s*(\S+)/m)[1]
const config={isolation:false,shared_account:'family',accounts:[{id:'family',name:'家庭账号',bridge_url:'native://netease/family'},{id:'personal',name:'私人账号',bridge_url:'native://netease/personal'}],bindings:{},user_bindings:{},user_identities:{}}
let settings={quality:'high',download_quality:'app',search_timeout:10,search_probe:false,deep_page:true,max_pages:10,personal_playlists:true,hot:false,daily:false,public_enabled:false,public:[],save_enabled:false,auto_cover:true,auto_lyrics:false,cache_max:2,cache_dir:'/vol1/1000/music-cache',cover_cache_dir:'/vol1/1000/cover-cache',download_dir:'/vol1/1000/Music',auto_bind:false,bind_timeout:300,handoff_max:2,scan_path:'',filename:'{artist} - {title}'}
const drafts=new Map();let draftSequence=0
const playlists=[{id:'morning',title:'日常循环',track_count:3},{id:'quiet',title:'轻音乐',track_count:2}]
const previewPlugin={name:'local-ui-preview',transformIndexHtml(html){return html.replace('<head>',`<head><script>(()=>{const original=window.matchMedia.bind(window);window.matchMedia=q=>q==='(prefers-color-scheme: dark)'?{matches:new URLSearchParams(location.search).get('theme')==='dark',media:q,onchange:null,addEventListener(){},removeEventListener(){},addListener(){},removeListener(){},dispatchEvent(){return false}}:original(q)})()</script>`).replace('</body>','<div style="position:fixed;bottom:10px;right:14px;font:11px system-ui;color:#687387;pointer-events:none;z-index:100">界面预览 · 示例数据</div></body>')},configureServer(server){server.middlewares.use(async(req,res,next)=>{
 const url=new URL(req.url,'http://127.0.0.1');const prefix='/app/fnmusic-go/api';if(!url.pathname.startsWith(prefix))return next()
 const path=url.pathname.slice(prefix.length);const method=req.method;let body={}
 if(method!=='GET'){let raw='';for await(const chunk of req)raw+=chunk;try{body=JSON.parse(raw||'{}')}catch{res.statusCode=400;res.end('{}');return}}
 let data
 if(path==='/csrf')data={token:'preview_token_'.repeat(4)}
 else if(path==='/status')data={version,checked_at:new Date().toISOString(),socket:{journal:true,target_state:{connected:true},upstream_state:{connected:true}}}
 else if(path==='/account-enrollments'&&method==='POST'){
  const id='draft_'+(++draftSequence);drafts.set(id,{name:body.name,provider:body.provider??'netease',checks:0,saved:false});data={id,...challenge(body.provider)}
 }
 else if(path.startsWith('/account-enrollments/')){
  const [, ,id,action]=path.split('/');const draft=drafts.get(id)
  if(!draft){res.statusCode=410;data={error:'登录会话已结束'}}
  else if(method==='DELETE'){drafts.delete(id);data={ok:true}}
  else if(action==='qr'){draft.checks=0;data=challenge(draft.provider)}
  else if(action==='check'){
   draft.checks++
   const scenario=new URL(req.headers.referer??'http://127.0.0.1').searchParams.get('enrollment')
   const success=scenario==='success'&&draft.checks>=2
   if(success&&!draft.saved){config.accounts.push({id,name:draft.name,bridge_url:'native://'+draft.provider+'/'+id});draft.saved=true}
   data={state:success?'success':draft.checks>1?'scanned':'pending',logged_in:success,saved:draft.saved}
  }else{res.statusCode=404;data={error:'预览未模拟此操作'}}
 }
 else if(path==='/accounts'&&method==='GET')data=config
 else if(path==='/accounts'&&method==='POST'){if(config.accounts.some(a=>a.id===body.id)){res.statusCode=409;data={error:'账号已存在'}}else{config.accounts.push(body);data={ok:true}}}
 else if(path==='/accounts/mode'){config.isolation=body.isolation;data={ok:true}}
 else if(path==='/accounts/shared'){config.shared_account=body.account_id;data={ok:true}}
 else if(/^\/accounts\/[^/]+$/.test(path)&&method==='DELETE'){const id=decodeURIComponent(path.split('/')[2]);config.accounts=config.accounts.filter(a=>a.id!==id);if(config.shared_account===id)config.shared_account='';data={ok:true}}
 else if(path.endsWith('/auth/status'))data={state:'success',logged_in:!path.includes('/personal/')}
 else if(path.endsWith('/auth/qr'))data=challenge(config.accounts.find(a=>path.includes('/'+a.id+'/'))?.bridge_url.startsWith('native://qqmusic/')?'qqmusic':'netease')
 else if(path.endsWith('/auth/check'))data={state:'pending',logged_in:false}
 else if(path.endsWith('/playlists'))data=path.includes('/personal/')?[{id:'personal-only',title:'私人收藏歌单',track_count:2}]:playlists
 else if(path.endsWith('/tracks'))data=[{id:'1',title:'清晨的风',artists:['示例歌手']},{id:'2',title:'慢慢走',artists:['示例歌手']},{id:'3',title:'归途',artists:['示例歌手']}]
 else if(path==='/settings'){if(method==='PUT')settings=body;data=settings}
 else if(path==='/users')data={list:[{username:'demo',uid:'1000',verified:true}]}
 else if(path.startsWith('/accounts/users/'))data={ok:true}
 else if(path==='/tasks')data={list:[],ffmpeg:true}
 else if(path==='/cover-cache')data={directory:settings.cover_cache_dir,files:0,bytes:0,max_bytes:536870912,ttl_days:7}
 else if(path==='/discovery')data={list:url.searchParams.get('kind')==='categories'?['全部','流行','轻音乐']:[],has_more:false}
 else {res.statusCode=404;data={error:'本地预览未模拟此操作'}}
 res.setHeader('Content-Type','application/json');res.end(JSON.stringify(data))
})}}
const server=await createServer({root:new URL('..',import.meta.url).pathname,plugins:[previewPlugin],server:{host:'127.0.0.1',port:Number(process.env.PORT??4173),strictPort:true}})
await server.listen();server.printUrls()
