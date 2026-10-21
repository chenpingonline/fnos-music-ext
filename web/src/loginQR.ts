export type MusicSource = 'netease' | 'qqmusic'
export type QRChallenge = {qr_url?: string; qr_image?: string}
export function accountSource(address: string): MusicSource { return address.startsWith('native://qqmusic/') ? 'qqmusic' : 'netease' }
export function sourceName(source: MusicSource) { return source === 'qqmusic' ? 'QQ 音乐' : '网易云音乐' }
export function scanClient(source: MusicSource) { return source === 'qqmusic' ? '手机 QQ' : '网易云音乐 App' }
export async function renderLoginQR(result: QRChallenge, source: MusicSource): Promise<{image: string; marker: string}> {
 if (source === 'qqmusic') {
  const value = result.qr_image ?? ''
  if (value.length > 720000 || !/^data:image\/png;base64,iVBORw0KGgo[A-Za-z0-9+/]*={0,2}$/.test(value)) throw new Error('QQ 音乐登录二维码无效，请重试。')
  return {image: value, marker: 'qqmusic-qr'}
 }
 const url = new URL(result.qr_url ?? '')
 if (url.protocol !== 'https:' || url.hostname !== 'music.163.com') throw new Error('音源返回的登录链接无效，请重试。')
 const {default: QRCode} = await import('qrcode')
 return {image: await QRCode.toDataURL(url.href, {width:224,margin:3,errorCorrectionLevel:'M',color:{dark:'#111111',light:'#ffffff'}}), marker: url.href}
}
