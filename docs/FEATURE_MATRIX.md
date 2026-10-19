# fnmusic-ext v2.9.0 功能与原理清单 → fnMusic Go 迁移矩阵

**最新进度（2026-10-19）**：播放、搜索、推荐、公共歌单、下载与存储本轮增量见 [MUSIC_FEATURES.md](MUSIC_FEATURES.md)。下面的 v0.1 表保留为历史迁移基线，不能用来判断当前实现状态。

基线：<https://github.com/javycoder/fnos_music_ext> main / VERSION=2.9.0（审查时间：2026-10-18）。本文件描述上游已声明/可见的实现功能，不意味着它们在所有 NAS 型号、客户端、音源和账号下均已得到实机验证。特别是商业音乐资源的合法访问、复制和再分发权限需要另外确认。

**状态约定**：
- **V0.1 已编码 + 本机测试**：有源码、单测与测试服务 E2E，通过本机验证；还没有 fnOS 实机验收。
- **仅接口/框架**：预留扩展入口，并未实际处理业务。
- **待迁移**：新项目尚未实现，不应展示为“支持”。

| ID | 功能 | 上游实现原理和参考文件 | 新项目 v0.1 状态 | 目标阶段 |
|---|---|---|---|---|
| SYS-01 | 官方 Web / App 集成 | `/var/run/trim_music.socket` 透明代理；`proxy/takeover.py` | 已编码 + 测试（模拟官方服务） | v0.1 实机 |
| SYS-02 | 官方数据不修改 | 官方业务透传；不写音乐 SQLite | 已编码：无业务写入逻辑，原请求透传 | v0.1 |
| SYS-03 | Socket 所有权验证 | `SO_PEERCRED`、进程可执行文件、inode、状态日志 | 已编码 + 测试（系统调用和身份） | v0.1 实机 |
| SYS-04 | 原子接管与回退 | Linux `renameat2(RENAME_NOREPLACE)`、预启动、退出恢复 | 已编码 + 测试；意外崩溃需 `restore`，不保证自动 | v0.1 实机 |
| SYS-05 | 健康探测和日志 | `/ _ext/healthz`（实际无空格）、`fnlog` | 已编码：健康和状态 JSON；日志轮换待迁移 | v0.1 / v0.4 |
| SYS-06 | 管理员网关 | `/app/fnmusic-ext`、`gatewaySocket`、飞牛管理员注入头 | 已编码：单独管理 UDS + 对端 UID 检查、只读接口 | v0.1 实机 |
| SEARCH-01 | 本地+在线同搜 | 官方搜曲响应包裹合并；`proxy/app.py:search_track` | v0.2：MusicBox 过渡桥接下基本合并已实现（模拟服务测试） | v0.2 |
| SEARCH-02 | 在线结果来源标记 | 给官方 Track VO 注入虚拟 GUID / source | v0.2：虚拟 GUID / 来源标记已实现 | v0.2 |
| SEARCH-03 | 防抖、超时、缓存 | `_wait_search_debounce`、搜索会话缓存 | 待迁移 | v0.2 |
| SEARCH-04 | 深分页与本地优先 | `docs/SEARCH_PAGINATION.md` 虚拟列表窗口及在线池 | 待迁移，需协议契约测试 | v0.2 |
| SOURCE-01 | 网易云查询/详情 | `musicbox-service/app.py` 封装 NEMbox CLI | v0.2：Go MusicBox HTTP 桥接，非纯 Go 网易协议实现 | v0.2 |
| SOURCE-02 | 网易云扫码和账户 | musicbox QR + cookie jar（实例共享） | 待迁移，并改为用户级隔离 | v0.3 |
| SOURCE-03 | 网易云音频链接/品质 | `musicbox-service/playback.py` EAPI + 授权检测 | 待迁移；须核实使用许可 | v0.2 |
| SOURCE-04 | musicdl 聚合搜索 | `musicdl-service` + Python `musicdl/curl_cffi` | 待迁移，初期非重点 | v0.4 |
| SOURCE-05 | 洛雪多源 | `lxmusic-service`、Node.js lxserver、用户 JS | 待迁移，要求脚本风险隔离 | v0.4 |
| STREAM-01 | 音频流 GET/HEAD/Range | `proxy/app.py:stream_track`、`serve_file_with_range` | v0.2：原生透传 + 在线 GET/HEAD/Range 模拟测试已通过 | v0.1 / v0.2 |
| STREAM-02 | 取流超时/重试/断点 | `_open_online_stream`、失败备选、URL pin | 待迁移 | v0.2 |
| STREAM-03 | 在线 HLS/AAC 转码 | `proxy/transcode.py` FFmpeg fMP4 | 待迁移；保持 ffmpeg 外部进程 | v0.3 |
| STREAM-04 | 播放和下载音质独立 | `FNMUSIC_QUALITY_MODE` / `FNMUSIC_DL_QUALITY` | 待迁移 | v0.3 |
| META-01 | 歌词、封面、元数据 | /lyric、/metadata、/static/cover 拦截及 CDN 缓存 | v0.2：元数据、歌词、封面适配已编码，未实机 | v0.2 |
| LIST-01 | 网易自建歌单 | `proxy/nmplaylists.py`：只读虚拟歌单 | 待迁移 | v0.3 |
| LIST-02 | 公共推荐/榜单 | `proxy/publicplaylists.py`，可选择注入 | 待迁移 | v0.3 |
| LIST-03 | 网易歌单常驻 | 非当前主音源时额外运行 musicbox | 待迁移 | v0.4 |
| FAV-01 | 在线收藏/独立歌单映射 | `online_favorites/` 以飞牛 user_guid 存储 | 待迁移，禁止回退 shared 用户身份 | v0.3 |
| FAV-02 | 收藏自动落库并绑定 | `_register_fav_autobind`、官方收藏/歌单写入 API | 待迁移；严格显式动作 | v0.4 |
| RECO-01 | 每日与热门推荐 | `proxy/recommend.py` + 源站 + 本地随机 | 待迁移 | v0.4 |
| RECO-02 | AI 智能推荐 | OpenAI 兼容 API、种子推荐、结果可播性验证 | 待迁移 | v0.4 |
| CACHE-01 | 边听边存 | tee 缓存 + 后台续传 + 保存入库 | **不支持**，重构默认关闭 | v0.4（需授权） |
| CACHE-02 | 自动封面嵌入和歌词保存 | mutagen ID3/FLAC + `.lrc` sidecar | 不支持 | v0.4（需授权） |
| CACHE-03 | 自定义文件名和路径 | `{artist}`、`{title}`、`{source}` 等 | 不支持 | v0.4 |
| ADMIN-01 | Web 管理面板 | `webui-service` 管理配置、音源、登录、日志 | 首阶段只读 UI + Vue 3 工程；离线后备静态页可运行 | v0.1 |
| ADMIN-02 | 保存配置与热重载 | `.env` 修改、白名单热重载 | 不支持，避免危险配置写入 | v0.2 |
| ADMIN-03 | 日志脱敏、诊断导出 | `proxy/fnlog.py` 等 | 仅基础状态，不含脱敏导出 | v0.4 |
| ADMIN-04 | GitHub/Gitee 更新检查 | `webui-service/app.py` Release API | 不支持 | v0.4 |
| FPK-01 | x86_64 / ARM64 | 原版 `manifest platform=all` 与构建 | Go 双架构二进制可编译；FPK staging 已生成 | v0.1 实机 |
| FPK-02 | 原生无 Docker 运行 | 源码模式支持 Native，但原 FPK install_init 要求 Docker | **新 FPK 生命周期脚本无 Docker/Python 依赖**，未实机 | v0.1 实机 |
| FPK-03 | 安装、升级、卸载 | `packaging/fpk/cmd/*`、备份/还原 | 生命周期脚本已编写，尚未通过 fnpack 与 fnOS 实机 | v0.1 实机 |

## 发现的上游行为边界

1. 当前 `FNMUSIC_TEE_SAVE_ENABLED=true` 默认保存完整音频；我们改为禁用，先明确权限后实现。
2. 当前 `FNMUSIC_NETEASE_MY_PLAYLISTS` 是“网易账号实例共享歌单”，不等于每飞牛用户拥有独立网易账号。
3. 上游 `proxy/app.py::_maybe_retry_official_binds` 在启用自动绑定时，存在**读列表触发后台下载**路径（与 issue #67 对应）；新项目禁止这个副作用。
4. 网关层透传和网易播放是两个不同的验收目标；通过本地网关单测并不等于已证明在线视频可播放。
5. 本项目当前没有获取飞牛或者网易云的额外内容授权，开发计划不包含会员限制绕过。

## 源码与许可

- 上游：<https://github.com/javycoder/fnos_music_ext>
- 主要参考：`proxy/app.py`、`proxy/takeover.py`、`musicbox-service/`、`proxy/transcode.py`、`proxy/nmplaylists.py`、`docs/SEARCH_PAGINATION.md`、`packaging/fpk/`。
- 上游 MIT，Copyright (c) 2026 fnmusic-ext contributors。若后续实质性复用上游源文件或衍生代码，保留 MIT 通知和版权声明；新项目 `THIRD_PARTY_NOTICES.md` 载明研究来源。

## v0.2 开发预览增量

参见 [V0_2_RELEASE_NOTES.md](V0_2_RELEASE_NOTES.md)。阶段性实现的网易云依赖外部 MusicBox HTTP bridge；生产环境适配和账号独立登录未实现。

### v0.3.0-preview 新增

- ✅ 多 MusicBox 实例账号注册与选择（每个账号独立回环地址和登录态）。
- ✅ 管理页面开启/关闭飞牛用户隔离；关闭时通过下拉框选择共享账号。
- ✅ 隔离模式按飞牛用户 GUID 绑定不同实例，未绑定用户仅透传本地音乐。
- ✅ 配置原子持久化（账户文件 0600），跨重启保留，在线接口实时生效。
- ✅ 按账号命名空间隔离歌曲元数据缓存，验证匿名请求不可获取在线内容。
- ⏳ 真正的 Go 原生网易云扫码和独立 MusicBox 启停管理未实现。
- ⏳ 私人歌单、收藏同步仍在后续阶段。

### v0.4.0-preview 新增（仅本地模拟验证）

- ✅ MusicBox 登录挑战（发起、查询、成功/扫描/过期状态）、登录状态查询，独立账户登录桥接。
- ✅ 基于账号 UID 过滤自建歌单，提供只读歌单及曲目管理 API。
- ✅ 飞牛音乐歌单列表、详情、曲目列表的只读虚拟注入；保留原生歌单响应和未知字段。
- ✅ 虚拟歌单 ID 按 MusicBox 实例命名空间签名；跨账号、切换共享账号不复用旧 ID。
- ✅ 管理页面及离线内嵌后备页面均有登录与歌单只读预览入口。
- ⏳ 不具备纯 Go 网易云接口、二维码图形兜底及 MusicBox 多实例自动拉起。
- ⏳ 尚未通过 fnOS 真机、网易云真账号、Vue npm 生产构建及 FPK 安装验收。


### v0.5.0-preview 新增（仅假 EAPI 服务测试，未经实网验收）

- ✅ 基于 Go 标准库 AES-128-ECB + MD5 的 EAPI 封装；原生 `native://netease/<账号ID>` 账号类型与 MusicBox 兼容桥接并存。
- ✅ 原生搜索、详情、LRC 歌词、授权 URL 和 Range 流式转发；拒绝上游返回的试听/权益不足链接；歌单按登录 UID 过滤。
- ✅ 原生扫码挑战、轮询、状态检查与私有 Cookie 0600 持久化；删除账号会撤销本地会话，不复用另一账号缓存。
- ✅ Vue 源码和内嵌后备管理页均可创建原生账号（不需要 Python）或桥接账号。
- ⏳ 内嵌 QR 图片、真实网易云网络协议测试、实机 fnOS App/安装验收尚未完成。
- ⏳ musicdl/洛雪/转码/下载/收藏等后续功能未实现。
