# QQ 音乐原生接入（实验性）

本开发分支增加 `native://qqmusic/<账号 ID>`。管理页添加账号时选择 QQ 音乐，使用手机 QQ 扫码；网易云账号、配置及旧歌曲 GUID 保持兼容。两个平台均由 Go 直接连接，不运行 Python、Node.js 或 HTTP 桥接服务。

## 实现范围

- QQ 二维码生成、轮询、QQ OAuth 授权码交换，完整凭证及服务端登录状态双重确认后保存账号。
- 独立的 `qqmusic-sessions/<账号 ID>.json`，文件 0600、目录 0700；取消草稿不落盘，保存配置失败回滚凭证，删除账号撤销其凭证。
- 带 zzc 请求签名的桌面客户端 CGI 搜索与 20 首分页、歌曲详情、普通 LRC 歌词、封面。
- `music.vkey.GetVkey / UrlGetVkey` 普通文件取流，支持 MP3 128/320 与 FLAC；最高档失败可沿用通用音质降档。拒绝试听、加密类型、其他文件及无权限响应。
- 自建歌单及曲目只读展示，按当前凭证 UIN 过滤所有权，列表最多读取 1000 首曲目。
- 沿用用户隔离、共享账号、播放缓存、HLS、App 下载和收藏入库流程。跨平台歌曲 GUID 不会被发送至当前所选的另一平台。

## 当前边界

- 第一版仅提供手机 QQ 扫码。微信、QQ 音乐 App 的 MQTT 扫码、短信验证码及自动刷新凭证尚未接入；登录态过期后可在账号的歌单面板点击“重新扫码登录”。
- QQ 公共歌单发现、榜单及每日推荐尚未实现。公共歌单预览仅列出网易云账号，个人歌单筛选可使用两种平台账号。
- 不实现 QMC/MFLAC 等音频文件解密。播放权限、可取得音质及是否返回完整文件取决于服务端账号权益。
- 当前客户端 CGI 使用桌面配置 `ct=19, cv=2201`，不混用 Android 设备身份；没有声称完整实现 Android 的 QIMEI、Session 与设备注册。真实联网测试已确认带签名的桌面搜索返回歌曲；每页 100 首会收到空列表，故使用每页 20 首。

## 协议参考

对以下仓库固定提交进行接口、参数及返回形态核对。Go 适配器独立实现，没有引入其 Python/JavaScript 运行时或源码文件；不将 GPL 源码翻译后并入本项目。

- [L-1124/QQMusicApi @ 27861e5](https://github.com/L-1124/QQMusicApi/tree/27861e51432ea6b6e88dc35c4e8c8e239c0339e8)：登录交换、客户端 CGI、VKey、歌曲和歌单响应结构；上游 GPL-3.0-or-later。
- [sansenjian/qq-music-api @ 45d0d27](https://github.com/sansenjian/qq-music-api/tree/45d0d275b590d8c3f99270e7f74135873172c71a)：QQ 授权流程和 Cookie 传递的交叉核对；上游 MIT。
- [jsososo/QQMusicApi @ 13b08af](https://github.com/jsososo/QQMusicApi/tree/13b08afd3180cc74d76fff208956b77a560abd22)：仅作旧接口历史对照。

## 验证

- 常规测试使用受控响应，检查 OAuth 目标、业务码及凭证验证、原子保存/回滚、歌单所有权、跨平台 GUID、拒绝试听及 CDN 不携带 Cookie。
- 前端测试检查 QQ PNG 二维码、平台选择、指引和保存状态；保留网易云二维码测试。
- 显式联网探测：`FNMUSIC_QQ_LIVE=1 go test ./internal/qqmusic -run TestLiveAnonymousProtocol -v`。只验证匿名二维码生成/待扫码轮询、歌曲详情和匿名搜索返回形态，不输出凭证。
- 真实手机 QQ 扫码已完成：登录状态确认成功，读取到 2 个自建歌单。测试账号无绿钻，付费歌曲《晴天》未取得音频。随后在用户 NAS 上使用当前登录会话验证：QQ《义勇军进行曲（合唱）》封面 HTTP 200、Range 音频 HTTP 206，成功读取 MP3 字节；AAC/HLS 转码准备返回成功。QQ《晴天》普通取流返回 HTTP 502，无绿钻账号未取得音频；不能把该歌曲失败当作 QQ 全部不可播放。切换平台前残留的网易云封面 ID 被当前 QQ 音源拒绝（403），与 QQ 音频权限失败分开记录。
