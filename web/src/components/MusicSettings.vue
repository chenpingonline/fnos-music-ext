<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { request, errorMessage } from '../api'
import PlaylistCover from './PlaylistCover.vue'
import type { Account, MusicPlaylist } from '../useAccountPanel'

type Selection = {id:string;title:string;kind:string;cover_url?:string}
type Policy = {quality:string;download_quality:string;search_timeout:number;search_probe:boolean;deep_page:boolean;max_pages:number;personal_playlists:boolean;personal_selections?:Record<string,string[]>;hot:boolean;daily:boolean;public_enabled:boolean;public:Selection[];save_enabled:boolean;auto_cover:boolean;auto_lyrics:boolean;cache_max:number;cache_dir:string;cover_cache_dir:string;download_dir:string;auto_bind:boolean;bind_timeout:number;handoff_max:number;scan_path:string;filename:string}
type Task = {id:string;user:string;track:{title:string;artists:string[]};state:string;purpose:string;percent:number;bytes:number;error?:string;warning?:string;path?:string}
type Card = {id:string;title:string;cover_url?:string;track_count:number}
const emit=defineEmits<{dirty:[value:boolean];busy:[value:boolean];access:[]}>()
const props = defineProps<{page:'music'|'recommend'|'storage'|'tasks';accounts:Account[];hasAccess:boolean;pickDirectory:()=>Promise<string|undefined>}>()
const discoveryAccounts=computed(()=>props.accounts.filter(a=>!a.bridge_url.startsWith('native://qqmusic/')))
const policy = ref<Policy>(), baseline = ref(''), error = ref(''), notice = ref(''), busy = ref(false), loading = ref(true)
type CoverCache = {directory:string;files:number;bytes:number;max_bytes:number;ttl_days:number}
const coverCache=ref<CoverCache>(), confirmCoverClear=ref(false)
const jobs = ref<Task[]>([]), ffmpeg = ref(false), taskBusy = ref('')
const account = ref(''), kind = ref('chart'), category = ref('全部'), pageNo = ref(1), more = ref(false), cards = ref<Card[]>([]), categories = ref<string[]>(['全部']), discovering = ref(false)
const personalAccount = ref(''), personalCards = ref<MusicPlaylist[]>([]), personalLoading = ref(false), personalError = ref('')
let personalController:AbortController|undefined
const personalMode = computed(() => policy.value?.personal_selections?.[personalAccount.value] === undefined ? 'all' : 'selected')
function personalSelected(id:string) { return personalMode.value==='all' || !!policy.value?.personal_selections?.[personalAccount.value]?.includes(id) }
function togglePersonal(id:string) {
 if(!policy.value || !personalAccount.value)return
 policy.value.personal_selections ??= {}
 // Older settings mean all playlists. Start with the loaded list on first edit.
 const ids = policy.value.personal_selections[personalAccount.value] ?? personalCards.value.map(card=>card.id)
 policy.value.personal_selections[personalAccount.value] = ids
 const index=ids.indexOf(id)
 if(index>=0)ids.splice(index,1);else ids.push(id)
}
async function loadPersonal() {
 personalController?.abort();personalCards.value=[];personalError.value=''
 if(!personalAccount.value){personalLoading.value=false;return}
 const current=new AbortController();personalController=current;personalLoading.value=true
 try {
  const rows=await request<MusicPlaylist[]>('/accounts/'+encodeURIComponent(personalAccount.value)+'/playlists','GET',undefined,current.signal)
  if(!disposed && personalController===current)personalCards.value=rows??[]
 } catch(e) { if(!disposed && !current.signal.aborted && personalController===current)personalError.value=errorMessage(e) }
 finally {if(!disposed && personalController===current)personalLoading.value=false}
}
const dirty = computed(()=>!!policy.value && JSON.stringify(policy.value)!==baseline.value)
const mainController = new AbortController()
let discoveryController:AbortController|undefined, taskController:AbortController|undefined, timer:ReturnType<typeof setTimeout>|undefined, disposed=false, coverRevision=0, discoveryRevision=0, taskRevision=0
const qualityOptions = [{value:'high',label:'高音质',hint:'优先最高可用档位，失败逐级降档'},{value:'balanced',label:'平衡',hint:'优先中间档，失败先降低再尝试更高档'},{value:'smooth',label:'流畅',hint:'优先标准品质，缩短首次加载时间'}]
const downloadOptions = [{value:'app',label:'跟随 App',hint:'标准档提供 MP3 320k，其余提供原文件'},{value:'original',label:'无损原文件',hint:'始终交付音源授权的最高可用原文件'},{value:'standard',label:'MP3 320k',hint:'下载统一转换为 MP3，需要 FFmpeg'}]
const labels:Record<string,string>={queued:'排队中',running:'下载中',paused:'已暂停',completed:'已完成',failed:'失败',cancelled:'已取消',cache:'滚动缓存',save:'边听边存',bind:'收藏入库',download:'App 下载'}
const selectFields = computed(()=>props.page==='music'?[{key:'quality' as const,title:'播放音质',options:qualityOptions},{key:'download_quality' as const,title:'下载音质',options:downloadOptions}]:[])
function clear(){error.value='';notice.value=''}
async function load(){loading.value=true;clear();try{policy.value=await request<Policy>('/settings','GET',undefined,mainController.signal);policy.value.public??=[];policy.value.cover_cache_dir??='';baseline.value=JSON.stringify(policy.value);if(props.page==='storage')await loadCoverCache()}catch(e){if(!disposed)error.value=errorMessage(e)}finally{if(!disposed)loading.value=false}}
async function save(){if(!policy.value||busy.value)return;busy.value=true;clear();try{const value=await request<Policy>('/settings','PUT',policy.value,mainController.signal);if(disposed)return;policy.value=value;baseline.value=JSON.stringify(value);notice.value='设置已保存并生效。';if(props.page==='storage')await loadCoverCache()}catch(e){if(!disposed)error.value=errorMessage(e)}finally{if(!disposed)busy.value=false}}
function reset(){policy.value=JSON.parse(baseline.value) as Policy;clear()}
async function directoryAction(field:'cache_dir'|'download_dir'|'cover_cache_dir',action:'pick'|'check'|'detect'){
 if(!policy.value||busy.value)return;busy.value=true;clear();try{
  if(action==='pick'){const path=await props.pickDirectory();if(!path||disposed)return;policy.value[field]=path}
  if(action==='detect'){const result=await request<{path:string}>('/directories/detect','GET',undefined,mainController.signal);if(disposed)return;policy.value[field]=result.path}
  const result=await request<{path:string}>('/directories/check','POST',{path:policy.value[field]},mainController.signal);if(disposed)return;policy.value[field]=result.path;notice.value='目录可写。点击保存设置后使用此目录。'
 }catch(e){if(!disposed)error.value=errorMessage(e)}finally{if(!disposed)busy.value=false}
}
function formatBytes(bytes:number){return (bytes/1048576).toFixed(1)+' MiB'}
async function loadCoverCache(){const revision=++coverRevision;try{const value=await request<CoverCache>('/cover-cache','GET',undefined,mainController.signal);if(!disposed&&revision===coverRevision)coverCache.value=value}catch(e){if(!disposed&&revision===coverRevision)error.value=errorMessage(e)}}
async function clearCoverCache(){if(busy.value||dirty.value)return;busy.value=true;clear();confirmCoverClear.value=false;try{const result=await request<CoverCache>('/cover-cache/clear','POST',{},mainController.signal);if(disposed)return;await loadCoverCache();notice.value=`已清理 ${result.files} 个封面缓存文件，释放 ${formatBytes(result.bytes)}。`}catch(e){if(!disposed)error.value=errorMessage(e)}finally{if(!disposed)busy.value=false}}
function selected(id:string){return policy.value?.public.some(p=>p.id===id)}
function toggle(card:Card){if(!policy.value)return;const list=policy.value.public;const index=list.findIndex(p=>p.id===card.id);if(index>=0)list.splice(index,1);else if(list.length<100)list.push({id:card.id,title:card.title,kind:kind.value,cover_url:card.cover_url});else error.value='最多选择 100 个公共歌单。'}
function move(index:number,delta:number){const list=policy.value?.public;if(!list||index+delta<0||index+delta>=list.length)return;const [item]=list.splice(index,1);list.splice(index+delta,0,item!)}
async function discover(nextPage=1){
 discoveryController?.abort();const revision=++discoveryRevision;cards.value=[];if(!account.value){discovering.value=false;return}
 discoveryController=new AbortController();discovering.value=true;clear();try{
  const params=new URLSearchParams({account:account.value,kind:kind.value,category:category.value,page:String(nextPage)});
  const result=await request<{list:Card[];has_more:boolean}>('/discovery?'+params,'GET',undefined,discoveryController.signal)
  if(disposed||revision!==discoveryRevision)return;cards.value=result.list??[];more.value=result.has_more;pageNo.value=nextPage
 }catch(e){if(!disposed&&revision===discoveryRevision)error.value=errorMessage(e)}finally{if(!disposed&&revision===discoveryRevision)discovering.value=false}
}
async function loadCategories(){if(!account.value)return;const chosen=account.value;try{const result=await request<{list:string[]}>('/discovery?'+new URLSearchParams({account:chosen,kind:'categories'}),'GET',undefined,mainController.signal);if(!disposed&&chosen===account.value)categories.value=result.list??['全部']}catch{/* discovery presents a retriable error separately */}}
function refreshTasks(){clearTimeout(timer);void tasks()}
defineExpose({reload:load,reset})
watch(dirty,value=>{emit('dirty',value);confirmCoverClear.value=false})
watch(busy,value=>emit('busy',value))
async function tasks(){
 if(disposed||props.page!=='tasks')return
 taskController?.abort();const current=new AbortController();taskController=current;const revision=++taskRevision
 try{const result=await request<{list:Task[];ffmpeg:boolean}>('/tasks','GET',undefined,current.signal);if(disposed||revision!==taskRevision)return;jobs.value=result.list??[];ffmpeg.value=result.ffmpeg}catch(e){if(!disposed&&!current.signal.aborted&&revision===taskRevision)error.value=errorMessage(e)}finally{if(!disposed&&revision===taskRevision&&props.page==='tasks')timer=setTimeout(()=>void tasks(),3000)}
}
async function taskAction(job:Task,action:'retry'|'cancel'){if(taskBusy.value)return;taskBusy.value=job.id;clear();try{await request('/tasks/'+job.id+'/'+action,'POST',{},mainController.signal);if(disposed)return;notice.value=action==='retry'?'任务已提交重试。':'任务已取消。';clearTimeout(timer);await tasks()}catch(e){if(!disposed)error.value=errorMessage(e)}finally{if(!disposed)taskBusy.value=''}}
watch(()=>props.accounts,()=>{if(!props.accounts.some(a=>a.id===personalAccount.value))personalAccount.value=props.accounts[0]?.id??'';if(!discoveryAccounts.value.some(a=>a.id===account.value))account.value=discoveryAccounts.value[0]?.id??''},{immediate:true})
watch(personalAccount,()=>{if(props.page==='recommend')void loadPersonal()})
watch([account,kind,category],()=>{if(props.page==='recommend'){void discover();if(account.value)void loadCategories()}})
watch(()=>props.page,()=>{clear();confirmCoverClear.value=false;if(props.page==='storage')void loadCoverCache();clearTimeout(timer);taskRevision++;taskController?.abort();discoveryController?.abort();discoveryRevision++;if(props.page==='tasks')void tasks();if(props.page==='recommend'){void discover();void loadPersonal()}else personalController?.abort()})
onMounted(()=>{void load();if(props.page==='tasks')void tasks();if(props.page==='recommend'){void discover();void loadCategories();void loadPersonal()}})
onUnmounted(()=>{emit('dirty',false);emit('busy',false);disposed=true;personalController?.abort();mainController.abort();discoveryController?.abort();taskController?.abort();clearTimeout(timer)})
</script>
<template>
<div class="music-settings">
 <div v-if="error" class="feedback error" role="alert">{{error}}</div><div v-if="notice" class="feedback success" role="status">{{notice}}</div>
 <div v-if="loading" class="empty-state">正在读取设置…</div>
 <div v-else-if="!policy" class="empty-state"><p>设置读取失败</p><button class="button secondary" @click="load">重新读取</button></div>
 <template v-else>
 <form v-if="page!=='tasks'" id="music-settings-form" @submit.prevent="save">
 <fieldset :disabled="busy" class="settings-fields">
 <template v-if="page==='music'">
 <section v-for="field in selectFields" :key="field.key" class="setting-section"><h2>{{field.title}}</h2><div class="options"><label v-for="option in field.options" :key="option.value" class="option" :class="{chosen:policy[field.key]===option.value}"><input v-model="policy[field.key]" type="radio" :name="field.key" :value="option.value"/><span><strong>{{option.label}}</strong><small>{{option.hint}}</small></span></label></div></section>
 <section class="setting-section"><h2>搜索策略</h2><label class="toggle-row"><span><strong>逐曲探活</strong><small>搜索前检查播放链接，可能增加等待时间。</small></span><input v-model="policy.search_probe" type="checkbox"/></label><label class="toggle-row"><span><strong>搜索深分页</strong><small>继续获取更多候选结果；按匹配程度排序，同层来源交错展示。</small></span><input v-model="policy.deep_page" type="checkbox"/></label><div class="field-grid"><label>搜索超时（秒）<input v-model.number="policy.search_timeout" type="number" min="1" max="60" required/></label><label>每个来源最多取页数<input v-model.number="policy.max_pages" type="number" min="1" max="50" required :disabled="!policy.deep_page"/></label></div></section>
 </template>
 <template v-if="page==='recommend'">
 <section class="setting-section"><h2>音乐页推荐</h2><label class="toggle-row"><span><strong>显示音源个人歌单</strong><small>在飞牛音乐中显示当前音源账号的个人歌单。只想显示排行榜时可关闭，不影响平台原有歌单。</small></span><input v-model="policy.personal_playlists" type="checkbox"/></label><label class="toggle-row"><span><strong>热门推荐</strong><small>在官方音乐页添加独立的热歌榜歌单。</small></span><input v-model="policy.hot" type="checkbox"/></label><label class="toggle-row"><span><strong>每日推荐</strong><small>使用当前用户绑定的网易云登录态，按账号隔离缓存。</small></span><input v-model="policy.daily" type="checkbox"/></label><label class="toggle-row"><span><strong>公共歌单</strong><small>选中的歌单按下方顺序显示，在线公共歌单只读。</small></span><input v-model="policy.public_enabled" type="checkbox"/></label></section>
 <section class="setting-section">
 <h2>选择个人歌单</h2>
 <p class="muted">按音源账号设置在飞牛音乐中显示的个人歌单，同一账号的用户共用此选择。上方总开关关闭时不显示个人歌单。</p>
 <div class="discovery-filters"><label>音源账号<select v-model="personalAccount"><option v-if="!accounts.length" value="">请先添加音源账号</option><option v-for="a in accounts" :key="a.id" :value="a.id">{{a.name}}</option></select></label>

 <button type="button" class="button secondary" :disabled="personalLoading||!personalAccount" @click="loadPersonal">刷新歌单</button></div>
 <p v-if="personalError" class="feedback error" role="status">{{personalError}}</p>
 <p v-else-if="personalLoading" class="inline-empty" role="status">正在加载个人歌单…</p>
 <p v-else-if="!personalCards.length" class="inline-empty">暂无个人歌单，请确认账号已登录后刷新。</p>
 <div v-else class="card-grid"><label v-for="card in personalCards" :key="personalAccount+':'+card.id" class="playlist-card" :class="{chosen:personalSelected(card.id)}"><input type="checkbox" :checked="personalSelected(card.id)" @change="togglePersonal(card.id)"/><PlaylistCover :src="card.cover_url"/><span><strong>{{card.title}}</strong><small>{{card.track_count}} 首</small></span></label></div>
 <p v-if="personalAccount" class="muted">已选 {{personalCards.filter(card=>personalSelected(card.id)).length}} 个，未勾选任何歌单时该账号不显示个人歌单。修改后请保存设置并刷新飞牛音乐。</p>
 </section>
 <section class="setting-section"><div class="section-heading"><h2>发现公共歌单</h2><span class="muted">已选 {{policy.public.length}} / 100</span></div><div class="discovery-filters"><label>预览账号<select v-model="account"><option v-if="!discoveryAccounts.length" value="">需添加网易云账号</option><option v-for="a in discoveryAccounts" :key="a.id" :value="a.id">{{a.name}}</option></select></label><label>类型<select v-model="kind"><option value="chart">排行榜</option><option value="recommend">推荐歌单</option><option value="category">分类歌单</option><option value="highquality">精品歌单</option></select></label><label v-if="kind==='category'||kind==='highquality'">分类<select v-model="category"><option v-for="c in categories" :key="c">{{c}}</option></select></label><button type="button" class="button secondary" :disabled="discovering||!account" @click="discover()">刷新</button></div><p class="muted">公共歌单、榜单与每日推荐目前仅支持网易云。预览账号仅用于浏览歌单，不会自动绑定飞牛用户。音乐页使用该用户在“访问设置”中配置的音源。</p><div v-if="!hasAccess" class="feedback error" role="status">尚未确认可用的用户音源访问。请配置账号分配，或让已分配用户打开飞牛音乐确认身份。<button type="button" class="text-button" @click="emit('access')">配置访问设置</button></div><p v-if="policy.public.length&&!policy.public_enabled" class="feedback" role="status">已选择 {{policy.public.length}} 个歌单，但“公共歌单”尚未开启。请开启上方开关并保存设置。</p><p v-else-if="dirty" class="feedback" role="status">选择尚未保存，请点击下方“保存设置”后，再刷新飞牛音乐。</p><p v-else-if="policy.public_enabled&&policy.public.length" class="muted" role="status">已保存 {{policy.public.length}} 个公共歌单。请刷新或重新打开飞牛音乐；仅已配置音源访问的用户可见。</p><div v-if="discovering" class="empty-state" role="status">正在获取歌单…</div><div v-else-if="!cards.length" class="empty-state">暂无歌单，请选择账号后刷新。</div><div v-else class="card-grid"><label v-for="card in cards" :key="card.id" class="playlist-card" :class="{chosen:selected(card.id)}"><input type="checkbox" :checked="selected(card.id)" @change="toggle(card)"/><PlaylistCover :src="card.cover_url"/><span><strong>{{card.title}}</strong><small>{{card.track_count}} 首 · 网易云</small></span></label></div><div class="pagination"><button type="button" class="button secondary compact" :disabled="discovering||pageNo===1" @click="discover(pageNo-1)">上一页</button><span>{{pageNo}}</span><button type="button" class="button secondary compact" :disabled="discovering||!more" @click="discover(pageNo+1)">下一页</button></div></section>
 <section class="setting-section"><h2>已选歌单 · 音乐页展示顺序</h2><p v-if="!policy.public.length" class="muted">在上方勾选想要显示的歌单。</p><div v-for="(item,index) in policy.public" :key="item.id" class="selected-row"><span class="muted">{{index+1}}</span><PlaylistCover :src="item.cover_url"/><div><strong>{{item.title}}</strong><small>网易云 · {{item.id}}</small></div><button type="button" class="button secondary compact" :aria-label="'上移 '+item.title" :disabled="index===0" @click="move(index,-1)">↑</button><button type="button" class="button secondary compact" :aria-label="'下移 '+item.title" :disabled="index===policy.public.length-1" @click="move(index,1)">↓</button><button type="button" class="text-button danger" :aria-label="'移除 '+item.title" @click="policy.public.splice(index,1)">移除</button></div></section>
 </template>
 <template v-if="page==='storage'">
 <section class="setting-section"><h2>保存与缓存</h2><label class="toggle-row"><span><strong>边听边存</strong><small>完整下载后保存到曲库；切歌后继续下载。关闭时仅保留滚动缓存。</small></span><input v-model="policy.save_enabled" type="checkbox"/></label><label class="toggle-row"><span><strong>自动嵌入封面</strong><small>音频保存成功后嵌入封面，需要系统 FFmpeg。</small></span><input v-model="policy.auto_cover" type="checkbox"/></label><label class="toggle-row"><span><strong>自动下载歌词</strong><small>音频保存后在相同目录生成同名 .lrc 文件。</small></span><input v-model="policy.auto_lyrics" type="checkbox"/></label><div class="field-grid"><label>每位用户保留的滚动缓存数<input v-model.number="policy.cache_max" type="number" min="1" max="100" required/></label><label>切歌续传并行数<input v-model.number="policy.handoff_max" type="number" min="0" max="20" required/><small>设为 0 时，试听缓存随播放请求中断。</small></label></div></section>
 <section class="setting-section"><h2>目录设置</h2><div v-for="field in (['cache_dir','download_dir'] as const)" :key="field" class="directory-field"><label>{{field==='cache_dir'?'歌曲缓存目录':'曲库下载目录'}}<input v-model="policy[field]" :placeholder="field==='cache_dir'?'留空使用应用数据目录':'留空尝试探测官方共享曲库'"/></label><div class="row-actions"><button type="button" class="button secondary compact" @click="directoryAction(field,'pick')">从 NAS 选择</button><button type="button" class="button secondary compact" :disabled="!policy[field]" @click="directoryAction(field,'check')">检查可写性</button><button v-if="field==='download_dir'" type="button" class="text-button" @click="directoryAction(field,'detect')">探测官方曲库</button></div></div><p class="muted">更换缓存目录时保留去重记录，旧音频缓存不自动迁移。下载目录应纳入官方音乐的扫描范围。</p><label class="template-field">文件名模板<input v-model="policy.filename" required maxlength="160"/><small>支持 {artist}、{title}、{source}，自动附加去重标识。</small></label></section>
 <section class="setting-section"><h2>封面缓存</h2><p class="muted">封面保留 7 天，过期后自动刷新；刷新失败时继续使用已有图片。最多占用 256 MiB，空间不足时清理最旧的缓存。</p><div class="directory-field"><label>封面缓存目录<input v-model="policy.cover_cache_dir" placeholder="留空使用应用数据目录"/></label><div class="row-actions"><button type="button" class="button secondary compact" @click="directoryAction('cover_cache_dir','pick')">从 NAS 选择</button><button type="button" class="button secondary compact" :disabled="!policy.cover_cache_dir" @click="directoryAction('cover_cache_dir','check')">检查可写性</button></div><small>保存后生效。缓存存放在所选目录下的 .fnmusic-covers 文件夹；更换目录不迁移旧缓存。</small></div><template v-if="coverCache"><p class="muted" role="status">当前缓存：{{coverCache.files}} 个文件 · {{formatBytes(coverCache.bytes)}} / {{formatBytes(coverCache.max_bytes)}}</p><small class="cache-directory">当前生效位置：{{coverCache.directory}}</small></template><p v-if="dirty" class="muted">请先保存或撤销修改，再清理当前生效目录的缓存。</p><div class="row-actions"><template v-if="confirmCoverClear"><span class="muted">清理后封面会在下次访问时重新下载。</span><button type="button" class="button secondary compact" :disabled="busy||dirty" @click="clearCoverCache">确认清理封面缓存</button><button type="button" class="text-button" @click="confirmCoverClear=false">取消</button></template><button v-else type="button" class="button secondary compact" :disabled="busy||dirty||!coverCache||coverCache.files===0" @click="confirmCoverClear=true">清理封面缓存</button><button type="button" class="text-button" :disabled="busy" @click="loadCoverCache">刷新缓存统计</button></div></section>
 <section class="setting-section"><h2>收藏与歌单自动入库</h2><label class="toggle-row"><span><strong>收藏自动绑定本地</strong><small>在线收藏立即生效，后台下载后等待官方收录，再用原用户身份绑定本地歌曲。</small></span><input v-model="policy.auto_bind" type="checkbox"/></label><div class="field-grid"><label>官方绑定等待（秒）<input v-model.number="policy.bind_timeout" type="number" min="10" max="3600" required/></label><label>曲库重扫接口（选填）<input v-model="policy.scan_path" placeholder="/music/api/v1/…"/><small>留空由官方自动扫描。超时可在任务中重试。</small></label></div></section>
 </template>
 </fieldset><Teleport defer to="#settings-footer"><div class="save-bar" role="region" aria-label="保存设置"><span class="muted">{{dirty?'有尚未保存的修改':'设置已同步'}}</span><button type="button" class="button secondary compact" :disabled="busy||!dirty" @click="reset">撤销修改</button><button type="submit" form="music-settings-form" class="button primary compact" :disabled="busy||!dirty">{{busy?'保存中…':'保存设置'}}</button></div></Teleport>
 </form>
 <template v-else><div class="context-bar"><span class="badge" :class="{accent:ffmpeg}">{{ffmpeg?'FFmpeg 可用':'FFmpeg 不可用'}}</span><span class="muted">自动刷新 · {{jobs.length}} 个任务</span><button class="text-button" @click="refreshTasks">立即刷新</button></div><div v-if="!jobs.length" class="empty-state"><h3>暂无下载任务</h3><p>播放在线歌曲、下载或收藏自动入库后，任务会出现在这里。</p></div><article v-for="job in jobs" :key="job.id" class="task-row"><div class="task-heading"><div><h3>{{job.track.title}}</h3><small>{{job.track.artists.join(' / ')}} · {{labels[job.purpose]||job.purpose}} · {{job.user}}</small></div><span class="badge" :class="{accent:job.state==='running',success:job.state==='completed'}">{{labels[job.state]||job.state}}</span></div><progress :value="job.percent" max="100" :aria-label="job.track.title+' 下载进度'"/><div class="task-foot"><span>{{job.percent}}% · {{(job.bytes/1048576).toFixed(1)}} MiB</span><div class="row-actions"><button v-if="job.state!=='running'&&job.state!=='queued'" class="text-button" :disabled="!!taskBusy" @click="taskAction(job,'retry')">{{job.state==='completed'?'重试绑定':'重试'}}</button><button v-if="job.state==='running'||job.state==='queued'||job.state==='paused'||job.state==='failed'" class="text-button danger" :disabled="!!taskBusy" @click="taskAction(job,'cancel')">取消</button></div></div><p v-if="job.error" class="task-error" role="status">{{job.error}}</p><p v-if="job.warning" class="task-error">{{job.warning}}</p><details v-if="job.path" class="task-details"><summary>文件位置</summary><code>{{job.path}}</code></details></article></template>
 </template>
</div>
</template>
<style scoped>
.music-settings { min-width: 0; }
.settings-fields { border: 0; padding: 0; margin: 0; min-width: 0; }
.setting-section { padding: 0 0 24px; margin-bottom: 24px; border-bottom: 1px solid var(--line-soft); }
.setting-section:last-child { border-bottom: 0; margin-bottom: 0; padding-bottom: 4px; }
.setting-section h2 { margin-bottom: 16px; }
.setting-section > p { font-size: 12px; line-height: 1.75; }
.options { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 12px; }
.option { display: flex; flex-direction: row-reverse; align-items: flex-start; justify-content: space-between; gap: 12px; border: 1px solid var(--line); border-radius: 9px; padding: 16px; cursor: pointer; transition: border-color 120ms, background 120ms; }
.option > span { min-width: 0; }
.option strong { font-weight: 600; }
.option input { margin: 3px 0 0; width: 16px; height: 16px; }
.option.chosen { background: var(--selected); border-color: var(--accent); }
.option:hover, .playlist-card:hover { border-color: var(--accent); }
input[type=checkbox], input[type=radio] { accent-color: var(--accent); flex: 0 0 auto; }
small { display: block; font-size: 12px; color: var(--muted); margin-top: 5px; line-height: 1.65; font-weight: 400; }
.toggle-row { display: flex; align-items: center; justify-content: space-between; gap: 24px; min-height: 76px; padding: 16px 0; border-bottom: 1px solid var(--line-soft); cursor: pointer; }
.toggle-row strong { font-weight: 500; }
.toggle-row:last-of-type { border-bottom: 0; }
.toggle-row > span { min-width: 0; }
.toggle-row input { appearance: none; width: 34px; height: 20px; border: 0; border-radius: 20px; background: var(--switch-off); margin: 0; position: relative; cursor: pointer; transition: background 120ms; }
.toggle-row input::before { content: ''; position: absolute; width: 16px; height: 16px; left: 2px; top: 2px; border-radius: 50%; background: white; box-shadow: 0 1px 3px #0002; transition: transform 120ms; }
.toggle-row input:checked { background: var(--accent); }
.toggle-row input:checked::before { transform: translateX(14px); }
.field-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 20px; margin-top: 18px; }
.field-grid label, .directory-field label, .template-field, .discovery-filters label { display: grid; grid-template-columns: minmax(90px, 140px) minmax(0,1fr); align-items: center; gap: 8px 16px; font-size: 12px; color: var(--muted); }
input:not([type=checkbox]):not([type=radio]), select { width: 100%; min-width: 0; min-height: 36px; background: var(--canvas); color: var(--text); border: 1px solid var(--line); border-radius: 6px; padding: 7px 10px; font-size: 13px; }
input:disabled, select:disabled { opacity: .5; cursor: not-allowed; }
.directory-field { margin: 20px 0 24px; }
.directory-field .row-actions { margin: 10px 0 0 156px; flex-wrap: wrap; }
.directory-field > small { margin-left: 156px; }
.template-field { margin-top: 20px; }
.field-grid label small, .template-field small { grid-column: 2; margin-top: 0; }
.save-bar { background: var(--canvas); padding: 16px 32px; display: flex; align-items: center; gap: 10px; width: 100%; max-width: 1400px; margin: 0 auto; }
.save-bar > span { margin-right: auto; font-size: 12px; display: flex; align-items: center; gap: 8px; }
.save-bar > span::before { content: ''; width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
.discovery-filters { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; margin: 18px 0 12px; }
.discovery-filters label { flex: 1; min-width: 240px; grid-template-columns: 72px minmax(0,1fr); }
.card-grid { display: grid; grid-template-columns: repeat(3,minmax(0,1fr)); gap: 10px; margin: 18px 0 12px; }
.playlist-card { display: flex; gap: 12px; align-items: center; border: 1px solid var(--line); border-radius: 8px; padding: 12px; min-width: 0; cursor: pointer; }
.playlist-card.chosen { background: var(--selected); border-color: var(--nav-active); }
.playlist-card > input { order: 3; margin-left: auto; width: 15px; height: 15px; }
.playlist-card > span:last-child { min-width: 0; }
.playlist-card strong { display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; font-size: 12px; font-weight: 500; }
.playlist-card small { margin-top: 3px; font-size: 11px; }
.selected-row { display: flex; gap: 12px; align-items: center; padding: 12px 0; border-bottom: 1px solid var(--line-soft); }
.selected-row > div:not(.playlist-cover) { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.pagination { display: flex; align-items: center; gap: 12px; margin-top: 14px; }
.task-row { padding: 20px; margin-bottom: 12px; border: 1px solid var(--line-soft); border-radius: 10px; }
.task-heading, .task-foot { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.task-heading > div { min-width: 0; overflow-wrap: anywhere; }
.task-heading small { font-size: 11px; }
.task-heading h3 { font-size: 14px; font-weight: 600; }
.task-heading .badge { flex-shrink: 0; }
.task-foot { font-size: 12px; color: var(--muted); font-variant-numeric: tabular-nums; }
.task-error, .danger { color: var(--danger); }
.task-error { font-size: 12px; margin-top: 8px; }
progress { display: block; appearance: none; border: 0; border-radius: 8px; overflow: hidden; width: 100%; height: 4px; background: var(--line-soft); accent-color: var(--accent); margin: 18px 0 12px; }
progress::-webkit-progress-bar { background: var(--line-soft); }
progress::-webkit-progress-value { background: var(--accent); border-radius: 8px; }
progress::-moz-progress-bar { background: var(--accent); border-radius: 8px; }
.task-details { margin-top: 12px; padding-top: 10px; border-top: 1px solid var(--line-soft); }
.task-details summary { color: var(--muted); font-size: 11px; }
.task-row code { display: block; margin-top: 8px; color: var(--muted); font-size: 11px; }
.cache-directory { overflow-wrap: anywhere; margin-bottom: 12px; }
@media(max-width:1100px) { .field-grid { grid-template-columns: 1fr; gap: 14px; } .card-grid { grid-template-columns: repeat(2,minmax(0,1fr)); } }
@media(max-width:800px) { .options { grid-template-columns: 1fr; gap: 8px; } .option { padding: 12px 14px; } }
@media(max-width:640px) {
  .card-grid { grid-template-columns: 1fr; }
  .field-grid label, .directory-field label, .template-field { grid-template-columns: 104px minmax(0,1fr); gap: 8px 12px; }
  .directory-field .row-actions, .directory-field > small { margin-left: 116px; }
  .save-bar { padding: 12px 20px; gap: 8px; }
  .save-bar > span { font-size: 11px; }
  .save-bar > span::before { display: none; }
  .save-bar .button { padding: 6px 9px; }
  .selected-row { gap: 8px; flex-wrap: wrap; }
  .task-row { padding: 16px; }
  .task-heading { align-items: flex-start; }
}
</style>
