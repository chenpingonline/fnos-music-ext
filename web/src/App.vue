<script setup lang="ts">
import { onMounted, ref } from 'vue'
interface State {
 version:string;phase:string;source_enabled:boolean;netease_mode:string;netease_owner:string;
 socket:{journal:boolean;target_state:unknown;upstream_state:unknown};checked_at:string
}
const state=ref<State|null>(null)
const error=ref('')
const loading=ref(false)
const section=ref<'status'|'source'|'help'>('status')
async function refresh(){loading.value=true;error.value='';try{const r=await fetch('/app/fnmusic-go/api/status',{credentials:'same-origin',cache:'no-store'});if(!r.ok)throw new Error('HTTP '+r.status);state.value=await r.json()}catch(e){error.value=String(e)}finally{loading.value=false}}
onMounted(refresh)
</script>
<template>
<div class="layout">
 <aside>
  <div class="brand"><span class="brand-icon">♫</span> fnMusic <small>GO</small></div>
  <nav>
   <button :class="{active:section==='status'}" @click="section='status'">运行概览</button>
   <button :class="{active:section==='source'}" @click="section='source'">网易云音源</button>
   <button :class="{active:section==='help'}" @click="section='help'">安装与诊断</button>
  </nav>
  <footer>Go + Vue 3 · v0.2.0 预览</footer>
 </aside>
 <main>
  <header><div><label>FNOS MUSIC EXTENSION</label><h1>{{section==='status'?'运行概览':section==='source'?'网易云音源':'安装与诊断'}}</h1><p>在官方飞牛音乐中使用在线音乐扩展</p></div><button class="primary" @click="refresh" :disabled="loading">{{loading?'检测中…':'刷新状态'}}</button></header>
  <p v-if="error" class="error">{{error}}</p>
  <template v-if="section==='status'">
   <div class="cards"><section><label>原生代理</label><h2>{{state?.socket?.journal?'已接管':'未接管'}}</h2><p>Socket 所有权保护</p></section><section><label>网易云音源</label><h2>{{state?.source_enabled?'已配置':'未配置'}}</h2><p>单用户隔离 · MusicBox 桥接</p></section><section><label>版本</label><h2>{{state?.version||'—'}}</h2><p>原生 Go 服务</p></section></div>
   <section class="details"><h3>运行状态</h3><pre>{{JSON.stringify(state,null,2)}}</pre></section>
  </template>
  <template v-if="section==='source'">
   <section><h3>网易云 MusicBox 适配</h3><p>当前阶段实现在线歌曲搜索、音乐元数据、歌词、音频流与封面接口。需要单独运行上游 MusicBox HTTP 服务，且仅向绑定的飞牛用户开放。</p><div class="fact"><span>桥接状态</span><b>{{state?.source_enabled?'已配置（不代表上游可用）':'未配置'}}</b></div><div class="fact"><span>绑定用户 GUID</span><code>{{state?.netease_owner || '未绑定'}}</code></div><div class="fact"><span>扫码登录与私人歌单</span><span>v0.3 开发计划</span></div><p class="notice">此页面暂不写入配置。请由管理员在 NAS 的受限配置文件中设置，避免共享登录态泄露给其他飞牛用户。</p></section>
  </template>
  <template v-if="section==='help'">
   <section><h3>诊断与安全</h3><p>代理停止应恢复官方音乐 Socket，网易云服务故障应仅影响在线功能。本预览版本未完成真实 fnOS 兼容验证。</p><pre>sudo journalctl -u fnmusic-go -n 100 --no-pager
sudo cat /var/lib/fnmusic-go/provider.env</pre><p>配置文件可能包含个人标识，分享诊断日志前请检查并脱敏。</p></section>
  </template>
  <p class="footnote">v0.2.0 开发预览：网易云登录、原生 Go 协议解析、私人歌单、下载、AI 推荐尚未实现。</p>
 </main>
</div>
</template>
<style scoped>
:global(*){box-sizing:border-box}:global(body){margin:0;background:#f7f9fb;color:#203145;font:14px -apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif}.layout{display:flex;min-height:100vh}aside{background:white;width:230px;border-right:1px solid #e9eef3;padding:28px 19px;display:flex;flex-direction:column}.brand{font-size:20px;font-weight:750}.brand-icon{display:inline-flex;background:#e8f2ff;border-radius:9px;color:#176ce3;width:30px;height:30px;align-items:center;justify-content:center}.brand small{font-size:10px;color:#126ff3}nav{display:grid;gap:7px;margin-top:38px}nav button{border:0;text-align:left;color:#778393;background:transparent;padding:13px 12px;border-radius:9px;cursor:pointer}nav button.active{color:#186ee6;background:#eaf3ff;font-weight:650}footer{margin-top:auto;color:#9da5b0;font-size:12px}main{flex:1;padding:44px;max-width:1160px}header{display:flex;justify-content:space-between;align-items:center;gap:20px;margin-bottom:26px}label{color:#8691a1;font-size:12px;letter-spacing:.5px}h1{font-size:30px;margin:6px 0}p{color:#8591a2;line-height:1.65}.primary{background:#126ff3;color:white;border:0;border-radius:9px;padding:10px 16px;cursor:pointer}.cards{display:grid;grid-template-columns:repeat(3,1fr);gap:16px}section{border:1px solid #e9edf2;border-radius:14px;background:#fff;padding:24px}h2{font-size:22px;margin:12px 0 5px}.details{margin-top:16px}.details pre,pre{background:#f6f8fa;border-radius:9px;padding:16px;white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.6}h3{margin-top:0}.error{color:#d33}.fact{display:flex;justify-content:space-between;border-top:1px solid #edf0f4;padding:13px 0;gap:20px}.fact>span:first-child{color:#738093}.notice{background:#fffaf0;padding:13px;border-radius:10px;color:#79623c}.footnote{font-size:12px}@media(max-width:800px){aside{width:170px;padding:18px 10px}main{padding:24px 15px}.cards{grid-template-columns:1fr}}@media(max-width:480px){.layout{display:block}aside{width:100%;padding:14px}.brand{font-size:18px}nav{display:flex;gap:4px;margin-top:13px}nav button{padding:9px 8px;font-size:12px}footer{display:none}main{padding:20px 12px}h1{font-size:24px}}
</style>
