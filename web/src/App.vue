<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { TrimApp } from '@trimjs/web-app'
import { connectTheme } from './theme'
import { errorMessage, request } from './api.ts'
import { useAccountPanel, type Account } from './useAccountPanel.ts'
import { useAccountEnrollment } from './useAccountEnrollment.ts'
import { useAccountStatuses } from './useAccountStatuses.ts'
import AppIcon from './components/AppIcon.vue'
import { accountSource, sourceName, scanClient, type MusicSource } from './loginQR'
import neteaseIcon from './assets/netease-cloud-music.png'
import qqMusicIcon from './assets/qq-music.svg'
import PlaylistCover from './components/PlaylistCover.vue'
import AppDialog from './components/AppDialog.vue'
import MusicSettings from './components/MusicSettings.vue'
import './app.css'

type Config = {account_groups?:Record<string,string[]>;isolation: boolean; shared_account: string; accounts: Account[]; bindings: Record<string,string>;user_bindings?:Record<string,string>;user_identities?:Record<string,{username:string;uid:string}>}
type Status = {version: string; checked_at?: string; socket: {journal: boolean; target_state?: {connected?: boolean}; upstream_state?: {connected?: boolean}}}
type Page = 'accounts' | 'access' | 'status' | 'help' | 'music' | 'recommend' | 'storage' | 'tasks'
const settingsRef=ref<InstanceType<typeof MusicSettings>>()
const settingsDirty=ref(false),settingsBusy=ref(false),pendingPage=ref<Page|null>(null)
const musicPages=['music','recommend','storage','tasks']
const version = __APP_VERSION__
const pages: {id: Page; label: string; icon: string; description: string}[] = [
  {id:'accounts',label:'音源账号',icon:'user',description:'管理音乐平台账号，登录后即可使用歌单、搜索等功能。'},
  {id:'music',label:'播放与搜索',icon:'search',description:'音质选择、下载品质与搜索策略'},
  {id:'recommend',label:'推荐歌单',icon:'list',description:'管理每日推荐、榜单和公共歌单'},
  {id:'storage',label:'存储设置',icon:'drive',description:'缓存目录、边听边存与收藏自动入库'},
  {id:'tasks',label:'下载任务',icon:'download',description:'查看下载进度、失败原因与入库绑定状态'},
  {id:'access',label:'访问设置',icon:'accounts',description:'设置飞牛用户与音源账号之间的访问关系'},
  {id:'status',label:'运行概览',icon:'status',description:'查看官方音乐代理与账号配置状态'},
  {id:'help',label:'帮助与诊断',icon:'help',description:'使用说明与本机故障排查'},
]
const navGroups = [{label:'音乐', pages:pages.slice(0,5)}, {label:'管理', pages:pages.slice(5)}]
const selected = ref<Page>('accounts')
const currentPage = computed(() => pages.find(page => page.id === selected.value)!)
const groupPrimary=ref(''), groupMembers=ref<string[]>([])
const config = ref<Config>({isolation:true,shared_account:'',accounts:[],bindings:{}})
const state = ref<Status | null>(null)
const loaded = ref(false), refreshing = ref(false), operation = ref(''), error = ref(''), notice = ref('')
const busy = computed(() => refreshing.value || operation.value !== '')
const content = ref<HTMLElement>(), scrolling = ref(false)
const trackSection = ref<HTMLElement>()
const search = ref(''), showAdd = ref(false), deleting = ref<Account | null>(null)
const accountName = ref(''), newSource=ref<MusicSource>('netease')
type NASUser={username:string;uid:string;current:boolean;verified:boolean;account_id:string}
const nasUsers=ref<NASUser[]>([]),userSearch=ref(''),usersError=ref(''),usersLoading=ref(false)
const matchingUsers=computed(()=>nasUsers.value.filter(u=>u.username.toLocaleLowerCase().includes(userSearch.value.trim().toLocaleLowerCase())))
const selectedNASUser=computed(()=>nasUsers.value.find(u=>u.username===selectedUsername.value))
const selectedUsername = ref(''), boundAccount = ref('')
const enrollment = useAccountEnrollment()
const panel = useAccountPanel()
const accountStatuses = useAccountStatuses()
const {account: focusAccount, action: panelAction, error: panelError, playlists, playlist, tracks, playlistsLoaded, page: trackPage, busy: panelBusy, visibleTracks, pageCount} = panel
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
const nameValid = computed(() => accountName.value.trim().length > 0 && new TextEncoder().encode(accountName.value.trim()).length <= 80)
const proxyReady = computed(() => !!state.value?.socket.journal && state.value.socket.target_state?.connected === true && state.value.socket.upstream_state?.connected === true)
const proxyText = computed(() => !state.value ? '等待检查' : proxyReady.value ? '代理已连接' : state.value.socket.journal ? '需要检查' : '未接管')
const checkedTime = computed(() => state.value?.checked_at ? new Date(state.value.checked_at).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit',second:'2-digit'}) : '—')
function accountUsers(account: Account) {
  return Object.values(config.value.bindings).filter(id=>id===account.id).length + Object.values(config.value.user_bindings??{}).filter(id=>id===account.id).length
}
const groupCandidates=computed(()=>config.value.accounts.filter(a=>a.id!==groupPrimary.value&&accountSource(a.bridge_url)!==accountSource(config.value.accounts.find(x=>x.id===groupPrimary.value)?.bridge_url??'')))
function readGroup(){groupMembers.value=[...(config.value.account_groups?.[groupPrimary.value]??[])]}
watch(groupPrimary,readGroup)
async function saveGroup(){if(!groupPrimary.value)return;if(await mutate('group','/accounts/groups/'+encodeURIComponent(groupPrimary.value),'PUT',{account_ids:groupMembers.value},'账号组合已保存。请在音乐设置及歌单设置中选择来源。'))readGroup()}
function accountScope(account: Account) {
  if (!config.value.isolation) return config.value.shared_account===account.id ? '共享账号' : config.value.account_groups?.[config.value.shared_account]?.includes(account.id)?'组合音源':'未启用'
  return accountUsers(account) ? '独立分配' : Object.entries(config.value.account_groups??{}).some(([primary,ids])=>ids.includes(account.id)&&accountUsers(config.value.accounts.find(a=>a.id===primary)!))?'组合音源':'尚未分配'
}
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
  if(!config.value.accounts.some(a=>a.id===groupPrimary.value))groupPrimary.value=config.value.accounts[0]?.id??'';readGroup()
 loaded.value = true
  if (selected.value === 'accounts') void accountStatuses.check(config.value.accounts)
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
  clearFeedback(); enrollment.close(); accountName.value = ''; newSource.value='netease'; showAdd.value = true
}
function closeAdd() { enrollment.close(); showAdd.value=false }
async function addAccount() {
  if (!nameValid.value) return
  await enrollment.start(accountName.value.trim(),newSource.value)
}
watch(enrollment.saved, async saved => {
 if (!saved || disposed) return
 // The server only reports saved after authentication and persistence both succeed.
 await refresh()
 if (!disposed && showAdd.value) { closeAdd(); announce('账号已登录并保存。') }
})
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
watch(selected, page => {
  if (page === 'accounts' && loaded.value) void accountStatuses.check(config.value.accounts)
  else accountStatuses.cancel()
})
onUnmounted(() => {disposed=true; accountStatuses.cancel(); usersController?.abort(); controller?.abort(); panel.close(); enrollment.close(); disposeTheme?.(); clearTimeout(scrollTimer); clearTimeout(noticeTimer)})
</script>

<template>
<div class="shell" :class="{'accounts-view':selected==='accounts'}">
  <aside class="sidebar" aria-label="应用导航">
    <nav aria-label="功能导航"><div v-for="group in navGroups" :key="group.label" class="nav-group"><p class="nav-group-label">{{group.label}}</p><button v-for="page in group.pages" :key="page.id" :class="{active:selected===page.id}" :aria-current="selected===page.id ? 'page' : undefined" :disabled="operation!==''||settingsBusy" @click="navigate(page.id)"><AppIcon :name="page.icon"/><span>{{page.label}}</span></button></div></nav>
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
          <div class="accounts-layout">
            <div class="accounts-main">
              <div class="context-bar"><div><span class="muted">当前访问模式：</span><AppIcon :name="config.isolation?'lock':'accounts'"/><strong>{{config.isolation?'用户隔离':'共享账号'}}</strong><button class="text-button" @click="navigate('access')">调整</button></div></div>
              <section class="account-section" aria-labelledby="accounts-title">
                <div class="account-toolbar"><h2 id="accounts-title" class="sr-only">已添加账号</h2><label v-if="config.accounts.length" class="search-field"><AppIcon name="search"/><input v-model="search" aria-label="搜索账号" placeholder="搜索名称或标识" type="search"/><button v-if="search" class="icon-button" aria-label="清除搜索" @click="search=''"><AppIcon name="close"/></button></label></div>
                <div v-if="!config.accounts.length" class="empty-state"><span class="empty-icon"><AppIcon name="music"/></span><h3>添加你的第一个音乐账号</h3><p>连接网易云或 QQ 音乐账号，在飞牛音乐中使用在线音源。</p><button class="button primary" @click="openAdd"><AppIcon name="plus"/>添加账号</button><span class="helper">使用对应平台的客户端扫码登录</span></div>
                <div v-else-if="!matchingAccounts.length" class="empty-state"><AppIcon name="search"/><h3>没有找到匹配的账号</h3><p>试试其他名称或账号标识。</p><button class="button secondary" @click="search=''">清除搜索</button></div>
                <template v-else>
                  <div class="account-columns" aria-hidden="true"><span>账号</span><span>连接类型</span><span>访问范围</span><span>歌单</span></div>
                  <div class="account-list"><article v-for="account in matchingAccounts" :key="account.id" class="account-row">
                    <div class="account-identity"><span class="account-avatar native-avatar"><img :src="accountSource(account.bridge_url)==='qqmusic'?qqMusicIcon:neteaseIcon" width="40" height="40" decoding="async" :alt="accountSource(account.bridge_url)==='qqmusic'?'QQ 音乐':'网易云音乐'"/></span><div class="account-info account-name-status"><h3>{{account.name}}</h3><span class="account-login-status" :class="accountStatuses.get(account.id).state" :title="accountStatuses.get(account.id).detail" :aria-label="account.name+'：'+accountStatuses.get(account.id).label" role="status"><i aria-hidden="true"/>{{accountStatuses.get(account.id).label}}</span></div></div>
                    <div class="account-type"><span>{{sourceName(accountSource(account.bridge_url))}}</span></div>
                    <div class="account-scope"><strong><AppIcon :name="config.isolation?'user':'accounts'"/>{{accountScope(account)}}</strong></div>
                    <div class="row-actions"><button class="account-open" :disabled="busy" @click="panel.openPlaylists(account)"><span>歌单</span><AppIcon name="arrow"/></button><button class="icon-button danger-hover" :aria-label="'删除账号 '+account.name" :title="'删除 '+account.name" :disabled="busy" @click="confirmDelete(account)"><AppIcon name="trash"/></button></div>
                  </article></div>
                </template>
              </section>
              <div class="account-tip"><AppIcon name="qr"/><div><h3>添加原生音乐账号</h3><p>点击“添加账号”选择网易云或 QQ 音乐，使用对应客户端扫码登录。QQ 音乐当前使用手机 QQ 扫码。</p></div></div>
              <p class="page-note"><AppIcon name="lock"/>登录凭证仅保存在 NAS 本机，不同账号独立存储。</p>
            </div>
            <aside class="account-guide" aria-label="开始使用"><h2>开始使用</h2><ol><li><span>1</span><div><h3>添加账号</h3><p>点击“添加账号”，填写别名并扫码，成功后自动保存。</p></div></li><li><span>2</span><div><h3>扫码登录</h3><p>网易云使用音乐 App，QQ 音乐使用手机 QQ 扫码并确认。</p></div></li><li><span>3</span><div><h3>查看歌单</h3><p>登录后加载自建歌单，并在访问设置中选择使用账号的用户。</p></div></li></ol></aside>
          </div>
        </template>

        <template v-else-if="selected==='access'">
          <section class="section"><div class="section-heading"><div><h2>账号访问模式</h2><p>决定飞牛用户使用哪个音源账号。</p></div><label class="switch"><input type="checkbox" role="switch" aria-label="开启用户隔离" :checked="config.isolation" :disabled="busy" @change="changeIsolation"/><span aria-hidden="true"/></label></div><div class="mode-description"><AppIcon :name="config.isolation?'lock':'accounts'"/><div><h3>{{config.isolation?'用户隔离已开启':'当前使用共享账号'}}</h3><p>{{config.isolation?'每位用户只使用为其绑定的账号；未绑定用户仍可使用本地音乐。':'所有已验证的飞牛用户共用所选账号及其组合音源，其歌单和账号权益会被共同使用。'}}</p></div></div><div v-if="!config.isolation" class="field shared-select"><label for="shared-account">共享音源账号</label><select id="shared-account" :value="config.shared_account" :disabled="busy" @change="changeShared"><option value="">不启用在线账号</option><option v-for="account in config.accounts" :key="account.id" :value="account.id">{{account.name}}</option></select><p v-if="!config.accounts.length" class="helper">请先在“音源账号”中添加账号。</p></div></section>
          <section v-if="config.isolation" class="section"><div class="section-heading"><div><h2>用户账号分配</h2><p>按用户名选择，无需填写内部标识。切换共享模式后分配仍会保留。</p></div><button class="text-button" :disabled="usersLoading||busy" @click="loadUsers">{{usersLoading?'读取中…':'刷新用户'}}</button></div>
          <p v-if="usersError" class="feedback error" role="alert">{{usersError}}</p>
          <form class="binding-form" @submit.prevent="saveBinding"><div class="field"><label for="user-search">搜索用户名</label><input id="user-search" v-model="userSearch" type="search" placeholder="输入用户名筛选" :disabled="busy"/></div><div class="field"><label for="nas-user">飞牛用户</label><select id="nas-user" v-model="selectedUsername" :disabled="busy||usersLoading" @change="selectUser"><option value="">请选择用户</option><option v-for="u in matchingUsers" :key="u.username" :value="u.username">{{u.username}}{{u.current?'（当前用户）':''}}</option></select></div><div class="field"><label for="bound-account">音源账号</label><select id="bound-account" v-model="boundAccount" :disabled="busy"><option value="">解除此用户的分配</option><option v-for="account in config.accounts" :key="account.id" :value="account.id">{{account.name}}</option></select></div><button class="button primary" :disabled="busy||!selectedNASUser">{{operation==='bind'?'保存中…':'保存分配'}}</button></form>
          <p v-if="!usersLoading&&!matchingUsers.length&&!usersError" class="inline-empty">{{userSearch?'没有匹配的本机用户。':'暂无可选择的本机用户，请刷新。'}}</p>
          <p v-if="selectedNASUser&&!selectedNASUser.verified" class="helper">该用户的音乐身份尚未确认。可先保存分配，再让其打开飞牛音乐；接口提供可核对的身份信息后会自动关联。未确认前仅使用本地音乐，也可切换共享模式。</p>
          <div class="binding-list"><div v-for="(id,name) in config.user_bindings" :key="name" class="binding-row"><strong>{{name}}</strong><AppIcon name="arrow"/><strong>{{config.accounts.find(a=>a.id===id)?.name??id}}</strong><span class="helper">{{nasUsers.find(u=>u.username===name)?.verified?'音乐身份已确认':'等待音乐身份确认'}}</span><button class="text-button" :disabled="busy" @click="unbindName(String(name))">解除分配</button></div><div v-for="(id,guid,index) in config.bindings" :key="guid" class="binding-row"><strong>{{config.user_identities?.[guid]?.username??('旧版绑定用户 '+(index+1))}}</strong><AppIcon name="arrow"/><strong>{{config.accounts.find(a=>a.id===id)?.name??id}}</strong><span class="helper">保留的旧版绑定</span><button class="text-button" :disabled="busy" @click="unbind(String(guid))">解除绑定</button></div></div>
          </section>
          <section class="section"><div class="section-heading"><div><h2>账号组合</h2><p>为已分配的主账号添加另一个平台的账号，用于多来源搜索与歌单展示。每个平台最多使用一个账号。</p></div></div><form @submit.prevent="saveGroup"><div class="field"><label for="group-primary">主账号</label><select id="group-primary" v-model="groupPrimary" :disabled="busy"><option v-if="!config.accounts.length" value="">请先添加账号</option><option v-for="a in config.accounts" :key="a.id" :value="a.id">{{a.name}} · {{sourceName(accountSource(a.bridge_url))}}</option></select></div><div class="field"><span id="group-members-label">附加音源</span><div class="group-choices" role="group" aria-labelledby="group-members-label"><label v-for="a in groupCandidates" :key="a.id">{{a.name}} · {{sourceName(accountSource(a.bridge_url))}}<input v-model="groupMembers" type="checkbox" :value="a.id" :disabled="busy||(!groupMembers.includes(a.id)&&groupMembers.length>=1)"/></label><p v-if="!groupCandidates.length" class="helper">添加另一个平台的账号后即可组合。</p></div></div><p class="helper">主账号的使用者也能访问附加账号的歌单及权益。隔离模式下，附加账号不能已分配给其他用户；不勾选则仅使用主账号。</p><button class="button primary" :disabled="busy||!groupPrimary">{{operation==='group'?'保存中…':'保存账号组合'}}</button></form></section>
          <p class="page-note"><AppIcon name="check"/>共享设置即时生效；隔离分配在音乐身份确认后生效，无需重启。</p>
        </template>

        <template v-else-if="selected==='status'">
          <div class="stats-grid"><section class="stat"><div class="stat-label"><AppIcon name="status"/>官方音乐代理</div><strong :class="{'healthy-text':proxyReady}">{{proxyText}}</strong><p>{{proxyReady?'接管状态与上游连接正常':'请检查官方音乐及扩展服务状态'}}</p></section><section class="stat"><div class="stat-label"><AppIcon name="music"/>音源账号</div><strong>{{config.accounts.length}}<small>个</small></strong><p>{{config.isolation?bindingCount+' 位用户已绑定':sharedName}}</p></section><section class="stat"><div class="stat-label"><AppIcon name="accounts"/>访问模式</div><strong>{{config.isolation?'用户隔离':'共享账号'}}</strong><button class="text-button" @click="navigate('access')">调整设置<AppIcon name="arrow"/></button></section></div>
          <section class="section"><div class="section-heading"><h2>连接详情</h2><span class="helper">上次检查 {{checkedTime}}</span></div><dl class="detail-list"><div><dt>扩展版本</dt><dd>v{{state?.version ?? version}}</dd></div><div><dt>官方 Socket 接管记录</dt><dd><span class="status-dot" :class="{online:state?.socket.journal}"/>{{state?.socket.journal?'存在':'无'}}</dd></div><div><dt>当前代理连接</dt><dd>{{state?.socket.target_state?.connected?'已连接':'未连接或未知'}}</dd></div><div><dt>官方上游连接</dt><dd>{{state?.socket.upstream_state?.connected?'已连接':'未连接或未知'}}</dd></div></dl></section>
          <section class="section"><div class="section-heading"><h2>诊断信息</h2><button class="button secondary compact" @click="copyDiagnostics"><AppIcon name="copy"/>复制诊断</button></div><details><summary>查看原始状态</summary><pre class="diagnostic">{{JSON.stringify({version:state?.version,socket:state?.socket,checked_at:state?.checked_at},null,2)}}</pre></details></section>
        </template>

        <template v-else>
          <section class="section"><div class="section-heading"><h2>开始使用</h2><span class="badge">v{{version}} 预览版</span></div><ol class="steps"><li><span>1</span><div><h3>添加音源账号</h3><p>填写账号别名，并使用对应平台的客户端扫码登录。</p></div></li><li><span>2</span><div><h3>完成网易云登录</h3><p>添加账号时，在同一弹窗填写别名并使用网易云 App 扫码，验证成功后自动保存。</p></div></li><li><span>3</span><div><h3>设置访问方式</h3><p>为飞牛用户绑定账号，或选择一个供已验证用户共用的账号。</p></div></li></ol></section>
          <section class="section"><div class="section-heading"><h2>常见问题</h2></div><details class="faq"><summary>添加账号后，为什么用户仍看不到在线音源？</summary><p>确认账号已登录。用户隔离模式下，还需要在“访问设置”搜索飞牛用户名并分配账号；共享模式下需要选择共享账号。</p></details><details class="faq"><summary>需要安装其他音源服务吗？</summary><p>不需要。扩展使用 Go 原生实现直接连接网易云或 QQ 音乐，扫码登录后即可使用。</p></details><details class="faq"><summary>数据保存在哪里？</summary><p>账号配置位于应用数据目录的 accounts.json，登录凭证位于同目录的 netease-sessions 和 qqmusic-sessions。配置与凭证仅本机 root 可读写，删除原生账号会清理其凭证。</p></details><details class="faq"><summary>连接异常时如何排查？</summary><p>先确认官方飞牛音乐与本扩展均已启动，再到“运行概览”检查连接详情。需要进一步排查时，在 NAS 终端查看服务日志：</p><pre class="diagnostic">journalctl -u fnmusic-go -n 100 --no-pager</pre></details></section>
          <div class="info-note"><AppIcon name="help"/><p>当前为预览版。原生模式依赖网易云非公开协议，仍需真实 fnOS 与音源验证。请仅使用拥有合法权限的账号及音源。</p></div>
        </template>
      </div>
    </div>
    <div id="settings-footer" class="settings-footer-host"></div>
  </main>

  <AppDialog v-if="pendingPage" title="尚有未保存的设置" @close="pendingPage=null"><div class="dialog-body"><p>离开此页面会丢弃尚未保存的设置。</p></div><div class="dialog-footer"><button class="button secondary" @click="pendingPage=null">继续编辑</button><button class="button danger" @click="settingsRef?.reset();selected=pendingPage!;pendingPage=null">丢弃并离开</button></div></AppDialog>
  <AppDialog v-if="showAdd" title="添加音源账号" @close="closeAdd">
    <form @submit.prevent="addAccount"><div class="dialog-body">
      <p class="dialog-description">填写别名并扫码登录，确认成功后才会保存账号。</p>
      <div v-if="enrollment.error.value" class="feedback error" role="alert">{{enrollment.error.value}}</div>
      <fieldset class="enrollment-fields" :disabled="enrollment.busy.value || !!enrollment.id.value">
        <div class="field"><label for="account-source">音乐平台</label><select id="account-source" v-model="newSource"><option value="netease">网易云音乐</option><option value="qqmusic">QQ 音乐（实验性）</option></select></div>
        <div class="field"><label for="account-name">账号别名</label><input id="account-name" v-model="accountName" autofocus autocomplete="off" placeholder="例如：我的网易云" required maxlength="80" :aria-invalid="accountName.length>0 && !nameValid" aria-describedby="account-name-hint"/><p v-if="accountName.length>0 && !nameValid" id="account-name-hint" class="helper danger-text">名称过长，请缩短后重试（最多约 26 个汉字）。</p></div>
      </fieldset>
      <div v-if="enrollment.qrImage.value" class="enrollment-qr"><img :src="enrollment.qrImage.value" width="192" height="192" :alt="sourceName(newSource)+'登录二维码'"/><div><h3 role="status">{{enrollment.status.value}}</h3><p>使用{{scanClient(newSource)}}扫码，并在手机上确认。</p><p class="helper">正在自动检查结果，登录成功后保存。</p><button type="button" class="text-button" :disabled="enrollment.busy.value" @click="addAccount">重新生成二维码</button></div></div>
      <p v-else class="helper enrollment-hint">取消或登录失败不会添加账号。</p>
    </div><div class="dialog-footer"><button class="button secondary" type="button" @click="closeAdd">取消</button><button v-if="!enrollment.qrImage.value" class="button primary" :disabled="enrollment.busy.value || !nameValid">{{enrollment.busy.value?'正在生成二维码…':'生成二维码'}}</button><button v-else class="button primary" type="button" :disabled="enrollment.busy.value" @click="enrollment.check">{{enrollment.saved.value?'已保存':enrollment.busy.value?'正在检查…':'检查扫码结果'}}</button></div></form>
  </AppDialog>

  <AppDialog v-if="deleting" title="删除音源账号" :busy="busy" @close="deleting=null; error=''">
    <div class="dialog-body"><div v-if="error" class="feedback error" role="alert">{{error}}</div><p>确定删除 <strong>{{deleting.name}}</strong>？</p><p class="muted">此账号的飞牛用户绑定会一并解除。保存在 NAS 的登录凭证也会被清除。此操作无法撤销。</p></div><div class="dialog-footer"><button class="button secondary" :disabled="busy" @click="deleting=null; error=''">取消</button><button class="button danger" :disabled="busy" @click="deleteAccount">{{operation==='delete'?'删除中…':'删除账号'}}</button></div>
  </AppDialog>

  <AppDialog v-if="focusAccount" :title="focusAccount.name+' · 歌单'" @close="panel.close()">
    <div class="dialog-body account-panel"><div class="row-actions"><button class="button secondary compact" :disabled="panelBusy" @click="panel.startLogin()">重新扫码登录</button><span v-if="panel.auth.value" class="helper" role="status">{{panel.statusText.value}}</span></div><div v-if="panel.qrImage.value" class="enrollment-qr"><img :src="panel.qrImage.value" width="192" height="192" :alt="sourceName(accountSource(focusAccount.bridge_url))+'登录二维码'"/><p>使用{{scanClient(accountSource(focusAccount.bridge_url))}}扫码并确认。登录成功后点击下方“刷新”加载歌单。</p></div><div v-if="panelError" class="feedback error" role="alert">{{panelError}}</div><div class="playlist-heading"><div><h3>自建歌单</h3><p class="helper">只读展示当前账号创建的歌单</p></div><button class="button secondary compact" :disabled="panelBusy" @click="panel.loadPlaylists()">{{panelAction==='playlists'?'加载中…':panelError?'重试':'刷新'}}</button></div><p v-if="panelAction==='playlists'" class="inline-empty" role="status">正在加载当前账号的歌单…</p><p v-else-if="playlistsLoaded && !playlists.length" class="inline-empty">当前账号没有可展示的自建歌单。</p><div v-else class="playlist-list"><button v-for="item in playlists" :key="item.id" :class="{chosen:playlist?.id===item.id}" :disabled="panelBusy" @click="showPlaylist(item)"><PlaylistCover :src="item.cover_url"/><span>{{item.title}}<small>{{item.track_count}} 首歌曲</small></span><AppIcon :name="playlist?.id===item.id?'check':'arrow'"/></button></div><div v-if="playlist" ref="trackSection" class="track-list"><div class="playlist-heading"><h3>{{playlist.title}}</h3><span class="helper">{{tracks.length}} 首</span></div><p v-if="!tracks.length" class="inline-empty">此歌单暂无歌曲。</p><ol v-else :start="(trackPage-1)*50+1"><li v-for="track in visibleTracks" :key="track.id"><span>{{track.title}}</span><small>{{track.artists.join(' / ') || '未知歌手'}}</small></li></ol><div v-if="pageCount>1" class="pagination"><button class="button secondary compact" :disabled="trackPage<=1" @click="changeTrackPage(trackPage-1)">上一页</button><span>{{trackPage}} / {{pageCount}}</span><button class="button secondary compact" :disabled="trackPage>=pageCount" @click="changeTrackPage(trackPage+1)">下一页</button></div></div></div>
    <div class="dialog-footer"><button class="button secondary" @click="panel.close()">完成</button></div>
  </AppDialog>
</div>
</template>
