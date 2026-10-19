<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { TrimApp } from '@trimjs/web-app'
import { connectTheme } from './theme'
import { errorMessage, request } from './api.ts'
import { useAccountPanel, type Account } from './useAccountPanel.ts'
import AppIcon from './components/AppIcon.vue'
import AppDialog from './components/AppDialog.vue'
import MusicSettings from './components/MusicSettings.vue'
import './app.css'

type Config = {isolation: boolean; shared_account: string; accounts: Account[]; bindings: Record<string,string>;user_bindings?:Record<string,string>;user_identities?:Record<string,{username:string;uid:string}>}
type Status = {version: string; checked_at?: string; socket: {journal: boolean; target_state?: {connected?: boolean}; upstream_state?: {connected?: boolean}}}
type Page = 'accounts' | 'access' | 'status' | 'help' | 'music' | 'recommend' | 'storage' | 'tasks'
const settingsRef=ref<InstanceType<typeof MusicSettings>>()
const settingsDirty=ref(false),settingsBusy=ref(false),pendingPage=ref<Page|null>(null)
const musicPages=['music','recommend','storage','tasks']
const version = __APP_VERSION__
const pages: {id: Page; label: string; icon: string; description: string}[] = [
  {id:'accounts',label:'音源账号',icon:'music',description:'管理网易云账号、登录状态与个人歌单'},
  {id:'music',label:'播放与搜索',icon:'music',description:'音质选择、下载品质与搜索策略'},
  {id:'recommend',label:'推荐歌单',icon:'music',description:'管理每日推荐、榜单和公共歌单'},
  {id:'storage',label:'存储设置',icon:'link',description:'缓存目录、边听边存与收藏自动入库'},
  {id:'tasks',label:'下载任务',icon:'status',description:'查看下载进度、失败原因与入库绑定状态'},
  {id:'access',label:'访问设置',icon:'accounts',description:'设置飞牛用户与音源账号之间的访问关系'},
  {id:'status',label:'运行概览',icon:'status',description:'查看官方音乐代理与账号配置状态'},
  {id:'help',label:'帮助与诊断',icon:'help',description:'使用说明与本机故障排查'},
]
const selected = ref<Page>('accounts')
const currentPage = computed(() => pages.find(page => page.id === selected.value)!)
const config = ref<Config>({isolation:true,shared_account:'',accounts:[],bindings:{}})
const state = ref<Status | null>(null)
const loaded = ref(false), refreshing = ref(false), operation = ref(''), error = ref(''), notice = ref('')
const busy = computed(() => refreshing.value || operation.value !== '')
const content = ref<HTMLElement>(), scrolling = ref(false)
const trackSection = ref<HTMLElement>()
const search = ref(''), showAdd = ref(false), deleting = ref<Account | null>(null)
const accountID = ref(''), accountName = ref(''), provider = ref('native'), bridgeURL = ref('http://127.0.0.1:8770')
type NASUser={username:string;uid:string;current:boolean;verified:boolean;account_id:string}
const nasUsers=ref<NASUser[]>([]),userSearch=ref(''),usersError=ref(''),usersLoading=ref(false)
const matchingUsers=computed(()=>nasUsers.value.filter(u=>u.username.toLocaleLowerCase().includes(userSearch.value.trim().toLocaleLowerCase())))
const selectedNASUser=computed(()=>nasUsers.value.find(u=>u.username===selectedUsername.value))
const selectedUsername = ref(''), boundAccount = ref('')
const panel = useAccountPanel()
const {account: focusAccount, action: panelAction, error: panelError, auth, qrImage, qrURL, expired, playlists, playlist, tracks, playlistsLoaded, page: trackPage, retryAt, busy: panelBusy, visibleTracks, pageCount, statusText} = panel
let usersController:AbortController|undefined
let controller: AbortController | undefined, disposed = false
let host: TrimApp | undefined
async function pickDirectory(){
if(!host || host.isStandaloneWeb)throw new Error('NAS 目录选择器仅在飞牛应用内可用，请手动输入 NAS 绝对路径。')
const paths=await host.pickFile({directory:true,title:'选择音乐目录',multiple:false});return paths?.[0]
}
let disposeTheme: (() => void) | undefined
let scrollTimer: ReturnType<typeof setTimeout> | undefined
let noticeTimer: ReturnType<typeof setTimeout> | undefined
const matchingAccounts = computed(() => {
  const query = search.value.trim().toLocaleLowerCase()
  return config.value.accounts.filter(account => (account.name+' '+account.id).toLocaleLowerCase().includes(query))
})
const bindingCount = computed(() => Object.keys(config.value.bindings).length+Object.keys(config.value.user_bindings??{}).length)
const sharedName = computed(() => config.value.accounts.find(account => account.id === config.value.shared_account)?.name ?? '未选择账号')
const idValid = computed(() => /^[A-Za-z0-9_-]{1,48}$/.test(accountID.value.trim()))
const nameValid = computed(() => accountName.value.trim().length > 0 && new TextEncoder().encode(accountName.value.trim()).length <= 80)
const proxyReady = computed(() => !!state.value?.socket.journal && state.value.socket.target_state?.connected === true && state.value.socket.upstream_state?.connected === true)
const proxyText = computed(() => !state.value ? '等待检查' : proxyReady.value ? '代理已连接' : state.value.socket.journal ? '需要检查' : '未接管')
const checkedTime = computed(() => state.value?.checked_at ? new Date(state.value.checked_at).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit',second:'2-digit'}) : '—')
function accountType(account: Account) { return account.bridge_url.startsWith('native://') ? '原生网易云' : 'MusicBox 桥接' }
function announce(message: string) { clearTimeout(noticeTimer); notice.value = message; noticeTimer = setTimeout(() => notice.value = '', 5000) }
function clearFeedback() { error.value = ''; notice.value = ''; clearTimeout(noticeTimer) }
async function navigate(page: Page) {
  if(settingsBusy.value)return
  if(settingsDirty.value&&!musicPages.includes(page)){pendingPage.value=page;return}
  selected.value = page; clearFeedback();if(page==='access')void loadUsers(); await nextTick(); content.value?.scrollTo({top:0})
}
function onScroll() { scrolling.value = true; clearTimeout(scrollTimer); scrollTimer = setTimeout(() => scrolling.value = false, 800) }
async function sync(signal: AbortSignal) {
  const [status, settings] = await Promise.all([
    request<Status>('/status','GET',undefined,signal), request<Config>('/accounts','GET',undefined,signal),
  ])
  if (disposed || signal.aborted) return
  state.value = status
  config.value = {...settings, accounts: settings.accounts ?? [], bindings: settings.bindings ?? {}}
  loaded.value = true
}
async function refresh() {
  if (busy.value || settingsBusy.value || settingsDirty.value) return
  controller = new AbortController(); refreshing.value = true; clearFeedback()
  try { await sync(controller.signal); await settingsRef.value?.reload() } catch (reason) { if (!disposed) error.value = errorMessage(reason) }
  finally { if (!disposed) refreshing.value = false }
}
async function mutate(key: string, path: string, method: string, body: unknown, success: string) {
  if (busy.value || !loaded.value) return false
  controller = new AbortController(); operation.value = key; clearFeedback()
  try {
    await request(path,method,body,controller.signal)
    if (disposed) return false
    try { await sync(controller.signal) } catch { loaded.value = false; error.value = '操作已保存，但刷新列表失败。请点击刷新重新获取最新配置。' }
    announce(success)
    return true
  } catch (reason) { if (!disposed) error.value = errorMessage(reason); return false }
  finally { if (!disposed) operation.value = '' }
}
function openAdd() {
  clearFeedback(); accountID.value = ''; accountName.value = ''; provider.value = 'native'; bridgeURL.value = 'http://127.0.0.1:8770'; showAdd.value = true
}
async function addAccount() {
  if (!idValid.value || !nameValid.value) return
  const id = accountID.value.trim()
  if (await mutate('add','/accounts','POST',{id,name:accountName.value.trim(),bridge_url:provider.value === 'native' ? 'native://netease/'+id : bridgeURL.value.trim()},'账号已添加，可点击“登录与歌单”继续。')) showAdd.value = false
}
async function deleteAccount() {
  const account = deleting.value
  if (!account) return
  if (await mutate('delete','/accounts/'+encodeURIComponent(account.id),'DELETE',{},'已删除账号及其绑定。')) { deleting.value = null; if (focusAccount.value?.id === account.id) panel.close() }
}
function confirmDelete(account: Account) { clearFeedback(); deleting.value = account }
async function loadUsers(){
 if(usersLoading.value)return;usersLoading.value=true;usersError.value=''
 usersController=new AbortController()
 try{const result=await request<{list:NASUser[]}>('/users','GET',undefined,usersController.signal);if(disposed)return;nasUsers.value=result.list??[];if(!nasUsers.value.some(u=>u.username===selectedUsername.value))selectedUsername.value=''}catch(reason){if(!disposed)usersError.value=errorMessage(reason)}finally{if(!disposed)usersLoading.value=false}
}
function selectUser(){boundAccount.value=selectedNASUser.value?.account_id??''}
async function saveBinding() {
 const user=selectedNASUser.value;if(!user){error.value='请搜索并选择飞牛用户。';return}
 if(await mutate('bind','/accounts/users/'+encodeURIComponent(user.username),'PUT',{account_id:boundAccount.value},boundAccount.value?(user.verified?'用户账号分配已保存。':'账号分配已保存，等待音乐身份确认。'):'用户分配已解除。'))await loadUsers()
}
async function unbindName(name:string){if(await mutate('unbind:'+name,'/accounts/users/'+encodeURIComponent(name),'PUT',{account_id:''},'用户分配已解除。'))await loadUsers()}
async function unbind(user: string) { await mutate('unbind:'+user,'/accounts/bindings/'+encodeURIComponent(user),'PUT',{account_id:''},'用户绑定已解除。') }
async function changeIsolation(event: Event) {
  const target = event.target as HTMLInputElement
  const desired = target.checked
  target.checked = config.value.isolation
  await mutate('mode','/accounts/mode','PUT',{isolation:desired},desired ? '已切换为用户隔离模式。' : '已切换为共享账号模式。')
}
async function changeShared(event: Event) {
  const target = event.target as HTMLSelectElement
  const account_id = target.value
  target.value = config.value.shared_account
  await mutate('shared','/accounts/shared','PUT',{account_id},'共享账号已更新。')
}
async function copyDiagnostics() {
  try {
    await navigator.clipboard.writeText(JSON.stringify({version:state.value?.version ?? version,socket:state.value?.socket,checked_at:state.value?.checked_at},null,2))
    announce('诊断信息已复制。')
  } catch { error.value = '浏览器未允许复制，请在下方诊断详情中手动复制。' }
}
async function showPlaylist(value: Parameters<typeof panel.loadTracks>[0]) {
  await panel.loadTracks(value)
  if (playlist.value?.id === value.id) { await nextTick(); trackSection.value?.scrollIntoView({block:'start'}) }
}
async function changeTrackPage(value: number) {
  trackPage.value = value
  await nextTick(); trackSection.value?.scrollIntoView({block:'start'})
}
onMounted(() => {
  try {
    const connection = connectTheme(host = new TrimApp(), matchMedia('(prefers-color-scheme: dark)'), theme => {document.documentElement.dataset.theme=theme; document.documentElement.style.colorScheme=theme})
    disposeTheme = connection.dispose
  } catch { /* CSS keeps the browser theme when the SDK cannot initialize. */ }
  void refresh()
})
watch(userSearch,()=>{if(!matchingUsers.value.some(u=>u.username===selectedUsername.value)){selectedUsername.value='';boundAccount.value=''}})
onUnmounted(() => {disposed=true; usersController?.abort(); controller?.abort(); panel.close(); disposeTheme?.(); clearTimeout(scrollTimer); clearTimeout(noticeTimer)})
</script>

<template>
<div class="shell">
  <aside class="sidebar" aria-label="应用导航">
    <nav aria-label="功能导航"><button v-for="page in pages" :key="page.id" :class="{active:selected===page.id}" :aria-current="selected===page.id ? 'page' : undefined" :disabled="operation!==''||settingsBusy" @click="navigate(page.id)"><AppIcon :name="page.icon"/><span>{{page.label}}</span><span v-if="page.id==='accounts' && loaded" class="nav-count">{{config.accounts.length}}</span></button></nav>
    <div class="sidebar-footer"><span class="connection"><i :class="{online:proxyReady}"/>{{proxyText}}</span><span class="version">v{{version}} <span>预览版</span></span></div>
  </aside>
  <main class="workspace" :aria-busy="refreshing">
    <header class="page-header"><div><h1>{{currentPage.label}}</h1><p>{{currentPage.description}}</p></div><div class="header-actions"><button class="button secondary compact" :disabled="busy||settingsDirty||settingsBusy" @click="refresh"><AppIcon name="refresh" :class="{spinning:refreshing}"/><span>{{refreshing?'刷新中':'刷新'}}</span></button><button v-if="selected==='accounts'" class="button primary compact" :disabled="busy || !loaded" @click="openAdd"><AppIcon name="plus"/>添加账号</button></div></header>
    <div ref="content" class="page-scroll" :class="{'is-scrolling':scrolling}" @scroll.passive="onScroll">
      <div class="page-content">
        <div v-if="error && !showAdd && !deleting" class="feedback error" role="alert"><AppIcon name="alert"/><span>{{error}}</span><button class="icon-button" aria-label="关闭错误提示" @click="error='' "><AppIcon name="close"/></button></div>
        <div v-if="notice" class="feedback success" role="status"><AppIcon name="check"/><span>{{notice}}</span><button class="icon-button" aria-label="关闭操作提示" @click="notice='' "><AppIcon name="close"/></button></div>
        <div v-if="!loaded && selected!=='help'" class="empty-state initial-state"><AppIcon :name="refreshing?'refresh':'alert'" :class="{spinning:refreshing}"/><h2>{{refreshing?'正在读取账号配置':'暂时无法读取配置'}}</h2><p>{{refreshing?'请稍候，正在连接管理服务。':'请确认应用正在运行，并使用飞牛管理员账号打开。'}}</p><button v-if="!refreshing" class="button secondary" @click="refresh">重新连接</button></div>

        <MusicSettings ref="settingsRef" @dirty="settingsDirty=$event" @busy="settingsBusy=$event" @access="navigate('access')" :has-access="config.isolation?(Object.keys(config.bindings).length>0||Object.keys(config.user_bindings??{}).some(name=>Object.values(config.user_identities??{}).some(u=>u.username===name))):!!config.shared_account" v-else-if="selected==='music'||selected==='recommend'||selected==='storage'||selected==='tasks'" :page="selected" :accounts="config.accounts" :pick-directory="pickDirectory"/>
        <template v-else-if="selected==='accounts'">
          <div class="context-bar"><div><AppIcon :name="config.isolation?'lock':'accounts'"/><span>{{config.isolation?'用户隔离':'共享账号'}}</span><span class="muted">{{config.isolation?'每位用户使用各自绑定的账号':sharedName}}</span></div><button class="text-button" @click="navigate('access')">管理访问设置<AppIcon name="arrow"/></button></div>
          <section class="section account-section" aria-labelledby="accounts-title">
            <div class="section-heading"><div class="heading-inline"><h2 id="accounts-title">已添加账号</h2><span class="count">{{config.accounts.length}}</span></div><label v-if="config.accounts.length" class="search-field"><AppIcon name="search"/><input v-model="search" aria-label="搜索账号" placeholder="搜索名称或标识" type="search"/><button v-if="search" class="icon-button" aria-label="清除搜索" @click="search=''"><AppIcon name="close"/></button></label></div>
            <div v-if="!config.accounts.length" class="empty-state"><span class="empty-icon"><AppIcon name="music"/></span><h3>添加你的第一个音乐账号</h3><p>连接网易云账号，在飞牛音乐中使用在线音源。</p><button class="button primary" @click="openAdd"><AppIcon name="plus"/>添加账号</button><span class="helper">支持原生网易云与本机 MusicBox 桥接</span></div>
            <div v-else-if="!matchingAccounts.length" class="empty-state"><AppIcon name="search"/><h3>没有找到匹配的账号</h3><p>试试其他名称或账号标识。</p><button class="button secondary" @click="search=''">清除搜索</button></div>
            <div v-else class="account-list"><article v-for="account in matchingAccounts" :key="account.id" class="account-row"><span class="account-avatar"><AppIcon name="music"/></span><div class="account-info"><div class="account-name"><h3>{{account.name}}</h3><span v-if="!config.isolation && config.shared_account===account.id" class="badge accent">共享使用中</span></div><div class="account-meta"><span>{{accountType(account)}}</span><i>·</i><code>{{account.id}}</code><span v-if="config.isolation">· {{Object.values(config.bindings).filter(id=>id===account.id).length+Object.values(config.user_bindings??{}).filter(id=>id===account.id).length}} 位用户</span></div><span v-if="!account.bridge_url.startsWith('native://')" class="bridge-address">{{account.bridge_url}}</span></div><div class="row-actions"><button class="button secondary compact" :disabled="busy" @click="panel.open(account)">登录与歌单<AppIcon name="arrow"/></button><button class="icon-button danger-hover" :aria-label="'删除账号 '+account.name" :title="'删除 '+account.name" :disabled="busy" @click="confirmDelete(account)"><AppIcon name="trash"/></button></div></article></div>
          </section>
          <p class="page-note"><AppIcon name="lock"/>登录凭证仅保存在 NAS 本机，不同账号独立存储。</p>
        </template>

        <template v-else-if="selected==='access'">
          <section class="section"><div class="section-heading"><div><h2>账号访问模式</h2><p>决定飞牛用户使用哪个网易云账号。</p></div><label class="switch"><input type="checkbox" role="switch" aria-label="开启用户隔离" :checked="config.isolation" :disabled="busy" @change="changeIsolation"/><span aria-hidden="true"/></label></div><div class="mode-description"><AppIcon :name="config.isolation?'lock':'accounts'"/><div><h3>{{config.isolation?'用户隔离已开启':'当前使用共享账号'}}</h3><p>{{config.isolation?'每位用户只使用为其绑定的账号；未绑定用户仍可使用本地音乐。':'所有已验证的飞牛用户共用一个账号，其歌单和账号权益会被共同使用。'}}</p></div></div><div v-if="!config.isolation" class="field shared-select"><label for="shared-account">共享网易云账号</label><select id="shared-account" :value="config.shared_account" :disabled="busy" @change="changeShared"><option value="">不启用在线账号</option><option v-for="account in config.accounts" :key="account.id" :value="account.id">{{account.name}}</option></select><p v-if="!config.accounts.length" class="helper">请先在“音源账号”中添加账号。</p></div></section>
          <section v-if="config.isolation" class="section"><div class="section-heading"><div><h2>用户账号分配</h2><p>按用户名选择，无需填写内部标识。切换共享模式后分配仍会保留。</p></div><button class="text-button" :disabled="usersLoading||busy" @click="loadUsers">{{usersLoading?'读取中…':'刷新用户'}}</button></div>
          <p v-if="usersError" class="feedback error" role="alert">{{usersError}}</p>
          <form class="binding-form" @submit.prevent="saveBinding"><div class="field"><label for="user-search">搜索用户名</label><input id="user-search" v-model="userSearch" type="search" placeholder="输入用户名筛选" :disabled="busy"/></div><div class="field"><label for="nas-user">飞牛用户</label><select id="nas-user" v-model="selectedUsername" :disabled="busy||usersLoading" @change="selectUser"><option value="">请选择用户</option><option v-for="u in matchingUsers" :key="u.username" :value="u.username">{{u.username}}{{u.current?'（当前用户）':''}}</option></select></div><div class="field"><label for="bound-account">网易云账号</label><select id="bound-account" v-model="boundAccount" :disabled="busy"><option value="">解除此用户的分配</option><option v-for="account in config.accounts" :key="account.id" :value="account.id">{{account.name}}</option></select></div><button class="button primary" :disabled="busy||!selectedNASUser">{{operation==='bind'?'保存中…':'保存分配'}}</button></form>
          <p v-if="!usersLoading&&!matchingUsers.length&&!usersError" class="inline-empty">{{userSearch?'没有匹配的本机用户。':'暂无可选择的本机用户，请刷新。'}}</p>
          <p v-if="selectedNASUser&&!selectedNASUser.verified" class="helper">该用户的音乐身份尚未确认。可先保存分配，再让其打开飞牛音乐；接口提供可核对的身份信息后会自动关联。未确认前仅使用本地音乐，也可切换共享模式。</p>
          <div class="binding-list"><div v-for="(id,name) in config.user_bindings" :key="name" class="binding-row"><strong>{{name}}</strong><AppIcon name="arrow"/><strong>{{config.accounts.find(a=>a.id===id)?.name??id}}</strong><span class="helper">{{nasUsers.find(u=>u.username===name)?.verified?'音乐身份已确认':'等待音乐身份确认'}}</span><button class="text-button" :disabled="busy" @click="unbindName(String(name))">解除分配</button></div><div v-for="(id,guid,index) in config.bindings" :key="guid" class="binding-row"><strong>{{config.user_identities?.[guid]?.username??('旧版绑定用户 '+(index+1))}}</strong><AppIcon name="arrow"/><strong>{{config.accounts.find(a=>a.id===id)?.name??id}}</strong><span class="helper">保留的旧版绑定</span><button class="text-button" :disabled="busy" @click="unbind(String(guid))">解除绑定</button></div></div>
          </section>
          <p class="page-note"><AppIcon name="check"/>共享设置即时生效；隔离分配在音乐身份确认后生效，无需重启。</p>
        </template>

        <template v-else-if="selected==='status'">
          <div class="stats-grid"><section class="stat"><div class="stat-label"><AppIcon name="status"/>官方音乐代理</div><strong :class="{'healthy-text':proxyReady}">{{proxyText}}</strong><p>{{proxyReady?'接管状态与上游连接正常':'请检查官方音乐及扩展服务状态'}}</p></section><section class="stat"><div class="stat-label"><AppIcon name="music"/>音源账号</div><strong>{{config.accounts.length}}<small>个</small></strong><p>{{config.isolation?bindingCount+' 位用户已绑定':sharedName}}</p></section><section class="stat"><div class="stat-label"><AppIcon name="accounts"/>访问模式</div><strong>{{config.isolation?'用户隔离':'共享账号'}}</strong><button class="text-button" @click="navigate('access')">调整设置<AppIcon name="arrow"/></button></section></div>
          <section class="section"><div class="section-heading"><h2>连接详情</h2><span class="helper">上次检查 {{checkedTime}}</span></div><dl class="detail-list"><div><dt>扩展版本</dt><dd>v{{state?.version ?? version}}</dd></div><div><dt>官方 Socket 接管记录</dt><dd><span class="status-dot" :class="{online:state?.socket.journal}"/>{{state?.socket.journal?'存在':'无'}}</dd></div><div><dt>当前代理连接</dt><dd>{{state?.socket.target_state?.connected?'已连接':'未连接或未知'}}</dd></div><div><dt>官方上游连接</dt><dd>{{state?.socket.upstream_state?.connected?'已连接':'未连接或未知'}}</dd></div></dl></section>
          <section class="section"><div class="section-heading"><h2>诊断信息</h2><button class="button secondary compact" @click="copyDiagnostics"><AppIcon name="copy"/>复制诊断</button></div><details><summary>查看原始状态</summary><pre class="diagnostic">{{JSON.stringify({version:state?.version,socket:state?.socket,checked_at:state?.checked_at},null,2)}}</pre></details></section>
        </template>

        <template v-else>
          <section class="section"><div class="section-heading"><h2>开始使用</h2><span class="badge">v{{version}} 预览版</span></div><ol class="steps"><li><span>1</span><div><h3>添加音源账号</h3><p>优先选择原生网易云；已有本机 MusicBox 服务时可选择桥接。</p></div></li><li><span>2</span><div><h3>完成网易云登录</h3><p>在账号的“登录与歌单”中生成二维码，使用网易云 App 扫码并确认。</p></div></li><li><span>3</span><div><h3>设置访问方式</h3><p>为飞牛用户绑定账号，或选择一个供已验证用户共用的账号。</p></div></li></ol></section>
          <section class="section"><div class="section-heading"><h2>常见问题</h2></div><details class="faq"><summary>添加账号后，为什么用户仍看不到在线音源？</summary><p>确认账号已登录。用户隔离模式下，还需要在“访问设置”绑定飞牛用户 GUID；共享模式下需要选择共享账号。</p></details><details class="faq"><summary>原生模式与 MusicBox 桥接有什么区别？</summary><p>原生模式直接连接网易云，无需单独运行 Python 或 Docker。桥接模式连接 NAS 本机的 MusicBox 服务，每个账号需使用独立实例和登录态。</p></details><details class="faq"><summary>数据保存在哪里？</summary><p>账号配置位于应用数据目录的 accounts.json，原生登录凭证位于同目录的 netease-sessions。配置与凭证仅本机 root 可读写，删除原生账号会清理其凭证。</p></details><details class="faq"><summary>连接异常时如何排查？</summary><p>先确认官方飞牛音乐与本扩展均已启动，再到“运行概览”检查连接详情。需要进一步排查时，在 NAS 终端查看服务日志：</p><pre class="diagnostic">journalctl -u fnmusic-go -n 100 --no-pager</pre></details></section>
          <div class="info-note"><AppIcon name="help"/><p>当前为预览版。原生模式依赖网易云非公开协议，仍需真实 fnOS 与音源验证。请仅使用拥有合法权限的账号及音源。</p></div>
        </template>
      </div>
    </div>
  </main>

  <AppDialog v-if="pendingPage" title="尚有未保存的设置" @close="pendingPage=null"><div class="dialog-body"><p>离开此页面会丢弃尚未保存的设置。</p></div><div class="dialog-footer"><button class="button secondary" @click="pendingPage=null">继续编辑</button><button class="button danger" @click="settingsRef?.reset();selected=pendingPage!;pendingPage=null">丢弃并离开</button></div></AppDialog>
  <AppDialog v-if="showAdd" title="添加音源账号" :busy="busy" @close="showAdd=false; error=''">
    <form @submit.prevent="addAccount"><div class="dialog-body"><p class="dialog-description">添加后可继续登录，并为飞牛用户配置访问方式。</p><div v-if="error" class="feedback error" role="alert">{{error}}</div><div class="provider-options" role="group" aria-label="音源连接方式"><label :class="{chosen:provider==='native'}"><input v-model="provider" type="radio" value="native" name="provider" :disabled="busy"/><div><strong>原生网易云<span class="badge">推荐</span></strong><span>直接连接，无需额外服务</span></div></label><label :class="{chosen:provider==='musicbox'}"><input v-model="provider" type="radio" value="musicbox" name="provider" :disabled="busy"/><div><strong>MusicBox 桥接</strong><span>连接 NAS 上已运行的实例</span></div></label></div><div class="field"><label for="account-name">显示名称</label><input id="account-name" v-model="accountName" autofocus autocomplete="off" placeholder="例如：我的网易云" required maxlength="80" :aria-invalid="accountName.length>0 && !nameValid" aria-describedby="account-name-hint" :disabled="busy"/><p v-if="accountName.length>0 && !nameValid" id="account-name-hint" class="helper danger-text">名称过长，请缩短后重试（最多约 26 个汉字）。</p></div><div class="field"><label for="account-id">账号标识</label><input id="account-id" v-model="accountID" autocomplete="off" spellcheck="false" placeholder="例如：netease_home" required maxlength="48" pattern="[A-Za-z0-9_\-]+" aria-describedby="account-id-hint" :aria-invalid="accountID.length>0 && !idValid" :disabled="busy"/><p id="account-id-hint" class="helper" :class="{'danger-text':accountID.length>0 && !idValid}">1–48 位字母、数字、下划线或减号，添加后不可修改。</p></div><div v-if="provider==='musicbox'" class="field"><label for="bridge-url">MusicBox 本机地址</label><input id="bridge-url" v-model="bridgeURL" type="url" required placeholder="http://127.0.0.1:8770" :disabled="busy"/><p class="helper">每个账号使用独立的本机实例与端口。</p></div><p v-else class="helper">原生模式会在 NAS 本机单独保存此账号的登录凭证。</p></div><div class="dialog-footer"><button class="button secondary" type="button" :disabled="busy" @click="showAdd=false; error=''">取消</button><button class="button primary" :disabled="busy || !idValid || !nameValid">{{operation==='add'?'添加中…':'添加账号'}}</button></div></form>
  </AppDialog>

  <AppDialog v-if="deleting" title="删除音源账号" :busy="busy" @close="deleting=null; error=''">
    <div class="dialog-body"><div v-if="error" class="feedback error" role="alert">{{error}}</div><p>确定删除 <strong>{{deleting.name}}</strong>？</p><p class="muted">此账号的飞牛用户绑定会一并解除。{{deleting.bridge_url.startsWith('native://')?'保存在 NAS 的原生登录凭证也会被清除。':'MusicBox 服务中的登录状态不会被清除。'}}此操作无法撤销。</p></div><div class="dialog-footer"><button class="button secondary" :disabled="busy" @click="deleting=null; error=''">取消</button><button class="button danger" :disabled="busy" @click="deleteAccount">{{operation==='delete'?'删除中…':'删除账号'}}</button></div>
  </AppDialog>

  <AppDialog v-if="focusAccount" :title="focusAccount.name+' · 登录与歌单'" @close="panel.close()">
    <div class="dialog-body account-panel"><div v-if="panelError" class="feedback error" role="alert">{{panelError}}</div><div class="login-summary"><span class="badge" :class="{success:auth?.logged_in}" role="status"><i class="status-dot" :class="{online:auth?.logged_in}"/>{{statusText}}</span><button class="text-button" :disabled="panelBusy" @click="panel.checkStatus()"><AppIcon name="refresh" :class="{spinning:panelAction==='status'}"/>检查状态</button></div><div v-if="!auth?.logged_in" class="login-area"><div v-if="qrImage" class="qr-frame"><img :src="qrImage" width="224" height="224" alt="网易云音乐登录二维码"/><p>打开网易云音乐 App 扫码，并在手机上确认。</p><span class="helper">正在自动检查扫码结果 · 二维码约 3 分钟有效</span><button class="button primary" :disabled="panelBusy || retryAt>0" @click="panel.checkLogin()">{{panelAction==='check'?'检查中…':retryAt?'请稍候再检查':'检查扫码结果'}}</button><a :href="qrURL" target="_blank" rel="noreferrer noopener">无法扫码？打开登录链接</a></div><div v-else class="login-placeholder"><AppIcon name="music"/><h3>{{expired?'二维码已过期':'登录网易云音乐'}}</h3><p>{{expired?'重新生成后，再使用手机扫码。':'使用网易云音乐 App 扫码登录，凭证保存在本机。'}}</p></div><button class="button" :class="qrImage?'secondary':'primary'" :disabled="panelBusy" @click="panel.startLogin()">{{panelAction==='qr'?'正在生成…':qrImage||expired?'重新生成二维码':'生成登录二维码'}}</button></div><div class="playlist-heading"><div><h3>自建歌单</h3><p class="helper">只读展示当前账号创建的歌单</p></div><button class="button secondary compact" :disabled="panelBusy" @click="panel.loadPlaylists()">{{panelAction==='playlists'?'加载中…':playlistsLoaded?'刷新歌单':'加载歌单'}}</button></div><p v-if="!playlistsLoaded" class="inline-empty">点击“加载歌单”查看当前账号的内容。</p><p v-else-if="!playlists.length" class="inline-empty">当前账号没有可展示的自建歌单。</p><div v-else class="playlist-list"><button v-for="item in playlists" :key="item.id" :class="{chosen:playlist?.id===item.id}" :disabled="panelBusy" @click="showPlaylist(item)"><AppIcon name="music"/><span>{{item.title}}<small>{{item.track_count}} 首歌曲</small></span><AppIcon :name="playlist?.id===item.id?'check':'arrow'"/></button></div><div v-if="playlist" ref="trackSection" class="track-list"><div class="playlist-heading"><h3>{{playlist.title}}</h3><span class="helper">{{tracks.length}} 首</span></div><p v-if="!tracks.length" class="inline-empty">此歌单暂无歌曲。</p><ol v-else :start="(trackPage-1)*50+1"><li v-for="track in visibleTracks" :key="track.id"><span>{{track.title}}</span><small>{{track.artists.join(' / ') || '未知歌手'}}</small></li></ol><div v-if="pageCount>1" class="pagination"><button class="button secondary compact" :disabled="trackPage<=1" @click="changeTrackPage(trackPage-1)">上一页</button><span>{{trackPage}} / {{pageCount}}</span><button class="button secondary compact" :disabled="trackPage>=pageCount" @click="changeTrackPage(trackPage+1)">下一页</button></div></div></div>
    <div class="dialog-footer"><button class="button secondary" @click="panel.close()">完成</button></div>
  </AppDialog>
</div>
</template>
