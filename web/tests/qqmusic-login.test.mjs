import {test} from 'node:test'
import assert from 'node:assert/strict'
import {renderLoginQR} from '../src/loginQR.ts'
import {useAccountEnrollment} from '../src/useAccountEnrollment.ts'
const png='data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aOuoAAAAASUVORK5CYII='
const reply=data=>new Response(JSON.stringify(data))
test('QQ login accepts bounded PNG data and rejects remote or active content',async()=>{
 assert.equal((await renderLoginQR({qr_image:png},'qqmusic')).image,png)
 for(const image of ['https://evil.example/qr','data:image/svg+xml,<svg/>','data:text/html,test',png+'!','data:image/png;base64,'+'A'.repeat(720001)]){
  await assert.rejects(renderLoginQR({qr_image:image},'qqmusic'))
 }
 await assert.rejects(renderLoginQR({qr_image:png,qr_url:'https://evil.example/'},'netease'))
})
test('QQ draft submits provider, shows QQ instructions and waits for durable save',async t=>{
 let saved=false;const payloads=[]
 t.mock.method(globalThis,'fetch',async(url,opts)=>{
  if(url.endsWith('/csrf'))return reply({token:'q'.repeat(43)})
  if(url.endsWith('/account-enrollments')){payloads.push(JSON.parse(opts.body));return reply({id:'qqdraft',qr_image:png})}
  return reply({state:'success',logged_in:true,saved})
 })
 const flow=useAccountEnrollment();t.after(()=>flow.close())
 await flow.start('QQ测试','qqmusic')
 assert.deepEqual(payloads,[{name:'QQ测试',provider:'qqmusic'}]);assert.equal(flow.qrImage.value,png);assert.match(flow.status.value,/手机 QQ/)
 await flow.check();assert.equal(flow.saved.value,false)
 saved=true;await flow.check();assert.equal(flow.saved.value,true)
})
