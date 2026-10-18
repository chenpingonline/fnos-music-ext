'use strict';
async function loadStatus(){
 const el=document.getElementById('diagnostic');el.textContent='正在检测…';
 try{const r=await fetch('/app/fnmusic-go/api/status',{credentials:'same-origin',cache:'no-store'});if(!r.ok)throw new Error('HTTP '+r.status);
  const x=await r.json();document.getElementById('socket').textContent=x.socket?.journal?'已接管':'未接管';
  document.getElementById('source').textContent=x.source_enabled?'已配置':'未配置';
  document.getElementById('version').textContent=x.version||'—';el.textContent=JSON.stringify(x,null,2)
 }catch(e){el.textContent='状态获取失败：'+String(e)}
}
document.getElementById('refresh').addEventListener('click',loadStatus);loadStatus();
