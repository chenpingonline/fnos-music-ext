<script setup lang="ts">
import { onMounted, onUnmounted, watch } from 'vue'
import AppIcon from './AppIcon.vue'
import { useDownloadTasks, canRetry, formatBytes, type TaskFilter } from '../useDownloadTasks'
const tasks=useDownloadTasks()
const {jobs,loading,loaded,error,notice,ffmpeg,updated,pending,filter,query,page,counts,filtered,pageCount,currentPage,visible}=tasks
const tabs:{id:TaskFilter;label:string}[]=[{id:'all',label:'全部'},{id:'active',label:'进行中'},{id:'attention',label:'需处理'},{id:'completed',label:'已完成'},{id:'cancelled',label:'已取消'}]
const states:Record<string,string>={queued:'排队中',running:'下载中',paused:'已暂停',completed:'已完成',failed:'下载失败',cancelled:'已取消'}
const purposes:Record<string,string>={cache:'滚动缓存',save:'边听边存',bind:'收藏入库',download:'App 下载'}
const modes:Record<string,string>={standard:'MP3 320k',original:'原文件',high:'高音质',balanced:'平衡音质',smooth:'流畅音质'}
function date(value?:string){if(!value)return '—';const d=new Date(value);return Number.isNaN(d.getTime())?'—':d.toLocaleString()}
watch([filter,query],()=>{page.value=1})
onMounted(tasks.start)
onUnmounted(tasks.stop)
defineExpose({reload:tasks.refresh})
</script>
<template>
<section class="downloads" aria-labelledby="downloads-title">
 <div class="downloads-heading"><div><h2 id="downloads-title">下载任务</h2><p>查看在线歌曲的下载、缓存与入库进度。</p></div><button type="button" class="button secondary compact" :disabled="loading" @click="tasks.refresh"><AppIcon name="refresh" :class="{spinning:loading}"/>{{loading?'刷新中':'刷新任务'}}</button></div>
 <div class="task-overview" aria-label="任务统计"><button v-for="item in tabs.slice(1,4)" :key="item.id" type="button" :class="['overview-item',item.id,{selected:filter===item.id}]" :aria-pressed="filter===item.id" @click="filter=filter===item.id?'all':item.id"><span>{{item.label}}</span><strong>{{loaded?counts[item.id]:'—'}}</strong><small>{{item.id==='active'?'下载与排队':item.id==='attention'?'异常与提醒':'正常完成'}}</small></button></div>
 <div class="download-status"><span><i :class="{online:ffmpeg}" aria-hidden="true"/>{{!loaded?'FFmpeg 能力待检测':ffmpeg?'FFmpeg 可用':'FFmpeg 不可用，MP3 转码和封面嵌入受限'}}</span><span>{{updated?'更新于 '+updated.toLocaleTimeString():'正在连接任务服务'}} · 自动刷新</span></div>
 <div v-if="error" class="feedback error" role="alert"><AppIcon name="alert"/><span>{{error}}{{loaded?' 当前保留上次获取的任务。':''}}</span><button type="button" class="text-button" :disabled="loading" @click="tasks.refresh">重试</button></div>
 <p v-if="notice" class="feedback success" role="status">{{notice}}</p>
 <div class="task-toolbar"><div class="task-filters" role="group" aria-label="筛选任务状态"><button v-for="tab in tabs" :key="tab.id" type="button" :class="{active:filter===tab.id}" :aria-pressed="filter===tab.id" @click="filter=tab.id">{{tab.label}}<span>{{counts[tab.id]}}</span></button></div><label class="task-search"><span>搜索</span><input v-model="query" type="search" placeholder="歌曲、歌手或用户"/></label></div>
 <div v-if="!loaded" class="empty-state" role="status"><AppIcon :name="error?'alert':'download'"/><h3>{{error?'暂时无法读取任务':'正在读取下载任务'}}</h3><p>{{error?'检查服务状态后，点击重试。':'请稍候，正在获取最新进度。'}}</p></div>
 <div v-else-if="!jobs.length" class="empty-state"><AppIcon name="download"/><h3>还没有下载任务</h3><p>在飞牛音乐中下载在线歌曲后，可在这里查看进度。<br/>播放缓存和收藏入库任务也会显示在这里。</p></div>
 <div v-else-if="!filtered.length" class="empty-state"><AppIcon name="search"/><h3>没有符合条件的任务</h3><p>试试其他关键词，或查看全部任务。</p><button type="button" class="button secondary compact" @click="query='';filter='all'">重置筛选</button></div>
 <div v-else class="task-list" :aria-busy="loading">
  <article v-for="job in visible" :key="job.id" class="download-row" :class="'state-'+job.state" :aria-label="job.track.title+' · '+(states[job.state]||job.state)">
   <div class="download-top"><span class="task-symbol"><AppIcon :name="job.state==='completed'?'check':job.state==='failed'||job.state==='paused'?'alert':'download'"/></span><div class="download-identity"><h3>{{job.track.title||'未命名歌曲'}}</h3><p>{{job.track.artists?.join(' / ')||'未知歌手'}}<span> · {{purposes[job.purpose]||job.purpose}}</span><span v-if="job.track.source"> · {{job.track.source==='qqmusic'?'QQ 音乐':job.track.source==='netease'?'网易云音乐':job.track.source}}</span></p></div><span class="badge" :class="{accent:job.state==='running',success:job.state==='completed',danger:job.state==='failed'||job.state==='paused'}">{{states[job.state]||job.state}}</span></div>
   <div v-if="job.state==='running'||job.state==='queued'||job.state==='paused'" class="download-progress"><progress :value="job.percent" max="100" :aria-label="job.track.title+' 下载进度'"/><span>{{job.state==='queued'?'等待下载':job.state==='running'&&job.percent>=99?'校验或处理音频中':job.percent+'%'}}</span></div>
   <div class="download-foot"><span>{{formatBytes(job.bytes)}}<template v-if="job.mode"> · {{modes[job.mode]||job.mode}}</template></span><div class="row-actions"><button v-if="canRetry(job)" type="button" class="text-button" :disabled="!!pending[job.id]" :aria-label="(job.state==='completed'?'重试绑定 ':'重试 ')+job.track.title" @click="tasks.action(job,'retry')">{{pending[job.id]==='retry'?'提交中…':job.state==='completed'?'重试绑定':'重试下载'}}</button><button v-if="['running','queued','paused','failed'].includes(job.state)" type="button" class="text-button danger" :disabled="!!pending[job.id]" :aria-label="'取消 '+job.track.title" @click="tasks.action(job,'cancel')">{{pending[job.id]==='cancel'?'取消中…':'取消任务'}}</button></div></div>
   <p v-if="job.error" class="task-message danger"><AppIcon name="alert"/>{{job.error}}</p><p v-if="job.warning" class="task-message warning"><AppIcon name="alert"/>{{job.warning}}</p>
   <details class="download-details"><summary>任务详情</summary><dl><div><dt>所属用户</dt><dd>{{job.user}}</dd></div><div><dt>创建时间</dt><dd>{{date(job.created_at)}}</dd></div><div><dt>更新时间</dt><dd>{{date(job.updated_at)}}</dd></div><div v-if="job.path"><dt>文件位置</dt><dd><code>{{job.path}}</code></dd></div></dl></details>
  </article>
  <div v-if="pageCount>1" class="task-pagination"><span>共 {{filtered.length}} 个任务 · {{currentPage}} / {{pageCount}} 页</span><div class="row-actions"><button type="button" class="button secondary compact" :disabled="currentPage===1" @click="page=currentPage-1">上一页</button><button type="button" class="button secondary compact" :disabled="currentPage===pageCount" @click="page=currentPage+1">下一页</button></div></div>
 </div>
</section>
</template>
<style scoped>
.downloads {
  min-width:0;
}
.downloads-heading {
  display:flex;
  justify-content:space-between;
  align-items:center;
  gap:16px;
  margin-bottom:24px;
}
.downloads-heading h2 {
  font-size:20px;
}
.downloads-heading p {
  font-size:12px;
  color:var(--muted);
  margin-top:8px;
}
.downloads-heading button {
  flex-shrink:0;
}
.task-overview {
  display:grid;
  grid-template-columns:repeat(3,minmax(0,1fr));
  gap:12px;
  margin-bottom:16px;
}
.overview-item {
  text-align:left;
  background:var(--canvas);
  color:var(--text);
  border:1px solid var(--line-soft);
  border-radius:10px;
  padding:18px 20px;
  cursor:pointer;
}
.overview-item.selected {
  border-color:var(--accent);
  background:var(--selected);
}
.overview-item>span {
  display:block;
  font-size:12px;
  color:var(--muted);
}
.overview-item strong {
  display:block;
  font-size:28px;
  font-weight:600;
  font-variant-numeric:tabular-nums;
  margin:8px 0;
}
.overview-item small {
  font-size:11px;
  color:var(--muted);
}
.overview-item.active strong {
  color:var(--accent);
}
.download-status {
  display:flex;
  align-items:center;
  justify-content:space-between;
  gap:12px;
  color:var(--muted);
  font-size:11px;
  margin-bottom:24px;
  flex-wrap:wrap;
}
.download-status>span:first-child {
  display:flex;
  align-items:center;
  gap:6px;
}
.download-status i {
  width:6px;
  height:6px;
  border-radius:50%;
  background:var(--muted);
  flex-shrink:0;
}
.download-status i.online {
  background:var(--success,#389b75);
}
.task-toolbar {
  display:flex;
  justify-content:space-between;
  gap:16px;
  align-items:center;
  flex-wrap:wrap;
  margin-bottom:16px;
}
.task-filters {
  display:flex;
  flex-wrap:wrap;
  gap:4px;
}
.task-filters button {
  display:flex;
  align-items:center;
  gap:6px;
  border:0;
  border-radius:6px;
  padding:8px 10px;
  background:transparent;
  color:var(--muted);
  cursor:pointer;
  font-size:12px;
}
.task-filters button.active {
  color:var(--accent);
  background:var(--selected);
}
.task-filters button span {
  font-size:10px;
  font-variant-numeric:tabular-nums;
}
.task-search {
  display:grid;
  grid-template-columns:32px minmax(0,1fr);
  gap:10px;
  align-items:center;
  font-size:12px;
  color:var(--muted);
  max-width:300px;
  flex:1;
  min-width:220px;
}
.task-search input {
  width:100%;
  min-width:0;
  border:1px solid var(--line);
  border-radius:6px;
  padding:8px 10px;
  background:var(--canvas);
  color:var(--text);
  font-size:12px;
}
.download-row {
  border:1px solid var(--line-soft);
  border-radius:10px;
  padding:20px;
  margin-bottom:12px;
  background:var(--canvas);
}
.download-top {
  display:flex;
  gap:12px;
  align-items:center;
}
.task-symbol {
  display:grid;
  place-items:center;
  flex-shrink:0;
  width:36px;
  height:36px;
  border-radius:8px;
  background:var(--selected);
  color:var(--accent);
}
.state-completed .task-symbol {
  color:var(--success,#389b75);
  background:var(--line-soft);
}
.state-failed .task-symbol,.state-paused .task-symbol {
  color:var(--danger);
  background:var(--line-soft);
}
.download-identity {
  flex:1;
  min-width:0;
}
.download-identity h3 {
  font-size:14px;
  font-weight:600;
  overflow-wrap:anywhere;
  line-height:1.5;
}
.download-identity p {
  font-size:11px;
  line-height:1.6;
  color:var(--muted);
  margin-top:4px;
  overflow-wrap:anywhere;
}
.download-top .badge {
  flex-shrink:0;
}
.download-progress {
  display:flex;
  align-items:center;
  gap:14px;
  margin:16px 0 12px;
}
.download-progress span {
  font-size:11px;
  color:var(--muted);
  font-variant-numeric:tabular-nums;
  flex-shrink:0;
}
progress {
  display:block;
  appearance:none;
  flex:1;
  min-width:0;
  border:0;
  border-radius:8px;
  overflow:hidden;
  width:100%;
  height:4px;
  background:var(--line-soft);
  accent-color:var(--accent);
}
progress::-webkit-progress-bar {
  background:var(--line-soft);
}
progress::-webkit-progress-value {
  background:var(--accent);
  border-radius:8px;
}
progress::-moz-progress-bar {
  background:var(--accent);
  border-radius:8px;
}
.download-foot {
  display:flex;
  align-items:center;
  justify-content:space-between;
  gap:12px;
  margin-top:14px;
  font-size:11px;
  color:var(--muted);
  font-variant-numeric:tabular-nums;
}
.download-foot .row-actions {
  flex-shrink:0;
}
.task-message {
  display:flex;
  align-items:flex-start;
  gap:8px;
  font-size:12px;
  line-height:1.6;
  margin-top:12px;
  overflow-wrap:anywhere;
}
.task-message svg {
  flex-shrink:0;
  width:15px;
  height:18px;
}
.danger {
  color:var(--danger);
}
.warning {
  color:var(--muted);
}
.download-details {
  border-top:1px solid var(--line-soft);
  padding-top:12px;
  margin-top:14px;
  font-size:11px;
  color:var(--muted);
}
.download-details summary {
  cursor:pointer;
  width:fit-content;
}
.download-details dl {
  display:grid;
  gap:8px;
  margin:12px 0 0;
}
.download-details dl>div {
  display:grid;
  grid-template-columns:72px minmax(0,1fr);
  gap:12px;
}
.download-details dd {
  margin:0;
  overflow-wrap:anywhere;
  line-height:1.6;
}
.task-pagination {
  display:flex;
  justify-content:space-between;
  align-items:center;
  gap:12px;
  font-size:12px;
  color:var(--muted);
  padding:12px 0;
}
.empty-state svg {
  color:var(--muted);
  width:32px;
  height:32px;
  margin-bottom:12px;
}
.task-filters button:focus-visible,.overview-item:focus-visible {
  outline:2px solid var(--accent);
  outline-offset:2px;
}
@media(max-width:640px) {
  .downloads-heading {
    align-items:flex-start;
  }
  .downloads-heading h2 {
    font-size:18px;
  }
  .downloads-heading p {
    max-width:190px;
    line-height:1.6;
  }
  .task-overview {
    gap:8px;
  }
  .overview-item {
    padding:14px 12px;
  }
  .overview-item strong {
    font-size:24px;
  }
  .overview-item small {
    font-size:10px;
  }
  .task-search {
    max-width:none;
    flex-basis:100%;
  }
  .task-filters {
    gap:0;
  }
  .task-filters button {
    padding:8px;
  }
  .download-row {
    padding:16px;
  }
  .task-symbol {
    display:none;
  }
  .download-top {
    align-items:flex-start;
  }
  .download-progress {
    gap:10px;
  }
  .download-foot {
    gap:8px;
    flex-wrap:wrap;
  }
  .download-status {
    line-height:1.7;
  }
  .task-pagination {
    font-size:11px;
  }
}
</style>
