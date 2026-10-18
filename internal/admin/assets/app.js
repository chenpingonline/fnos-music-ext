'use strict';
const base='/app/fnmusic-go/api';let cfg=null;
const $=id=>document.getElementById(id);
function feedback(message,error=false){$('feedback').textContent=message;$('feedback').style.color=error?'#af3030':'#287847'}
async function get(path){const r=await fetch(base+path,{credentials:'same-origin',cache:'no-store'});if(!r.ok)throw Error('HTTP '+r.status);return r.json()}
async function write(method,path,obj){try{const r=await fetch(base+path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json','X-FnMusic-Admin':'1'},body:JSON.stringify(obj)});if(!r.ok){const data=await r.json().catch(()=>({error:'HTTP '+r.status}));throw Error(data.error||r.status)}await refresh();feedback('保存成功，已生效')}catch(e){feedback(String(e),true)}}
const option=(v,label)=>{const x=document.createElement('option');x.value=v;x.textContent=label;return x};
async function refresh(){try{const [s,c]=await Promise.all([get('/status'),get('/accounts')]);cfg=c;
$('diagnostic').textContent=JSON.stringify(s,null,2);$('isolation').checked=c.isolation;
$('shared-panel').hidden=c.isolation;$('private-panel').hidden=!c.isolation;
$('shared').replaceChildren(option('','不启用在线账号'),...c.accounts.map(a=>option(a.id,a.name)));$('shared').value=c.shared_account;
$('bind-account').replaceChildren(option('','解除绑定'),...c.accounts.map(a=>option(a.id,a.name)));
$('accounts').replaceChildren();for(const a of c.accounts){const row=document.createElement('div');row.className='entry';const title=document.createElement('span');title.textContent=`${a.name} (${a.id}) · ${a.bridge_url}`;const btn=document.createElement('button');btn.textContent='删除';btn.onclick=()=>{if(confirm('删除账号并清除绑定？'))write('DELETE','/accounts/'+encodeURIComponent(a.id),{})};row.append(title,btn);$('accounts').append(row)}
$('bindings').replaceChildren();for(const [user,id] of Object.entries(c.bindings)){const row=document.createElement('div');row.className='entry';row.textContent=user+' → '+(c.accounts.find(a=>a.id===id)?.name||id);$('bindings').append(row)}}catch(e){feedback(String(e),true)}}
$('refresh').onclick=refresh;
$('isolation').onchange=e=>write('PUT','/accounts/mode',{isolation:e.target.checked});
$('shared').onchange=e=>write('PUT','/accounts/shared',{account_id:e.target.value});
$('add').onclick=()=>write('POST','/accounts',{id:$('account-id').value.trim(),name:$('account-name').value.trim(),bridge_url:$('account-url').value.trim()});
$('bind').onclick=()=>write('PUT','/accounts/bindings/'+encodeURIComponent($('user-guid').value.trim()),{account_id:$('bind-account').value});
refresh();
