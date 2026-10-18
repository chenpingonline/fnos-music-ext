# fnmusic-ext v2.9.0 功能与原理清单 → fnMusic Go 迁移矩阵

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
| SEARCH-01 | 本地+在线同搜 | 官方搜曲响应包裹合并；`proxy/app.py:search_track` | 本地原生搜索完全透传；在线合并待迁移 | v0.2 |
| SEARCH-02 | 在线结果来源标记 | 给官方 Track VO 注入虚拟 GUID / source | 待迁移 | v0.2 |
| SEARCH-03 | 防抖、超时、缓存 | `_wait_search_debounce`、搜索会话缓存 | 待迁移 | v0.2 |
| SEARCH-04 | 深分页与本地优先 | `docs/SEARCH_PAGINATION.md` 虚拟列表窗口及在线池 | 待迁移，需协议契约测试 | v0.2 |
| SOURCE-01 | 网易云查询/详情 | `musicbox-service/app.py` 封装 NEMbox CLI | Provider 接口骨架；音源未接入 | v0.2 |
| SOURCE-02 | 网易云扫码和账户 | musicbox QR + cookie jar（实例共享） | 待迁移，并改为用户级隔离 | v0.3 |
| SOURCE-03 | 网易云音频链接/品质 | `musicbox-service/playback.py` EAPI + 授权检测 | 待迁移；须核实使用许可 | v0.2 |
| SOURCE-04 | musicdl 聚合搜索 | `musicdl-service` + Python `musicdl/curl_cffi` | 待迁移，初期非重点 | v0.4 |
| SOURCE-05 | 洛雪多源 | `lxmusic-service`、Node.js lxserver、用户 JS | 待迁移，要求脚本风险隔离 | v0.4 |
| STREAM-01 | 音频流 GET/HEAD/Range | `proxy/app.py:stream_track`、`serve_file_with_range` | **本地官方请求透明透传**测试通过；在线转发待实现 | v0.1 / v0.2 |
| STREAM-02 | 取流超时/重试/断点 | `_open_online_stream`、失败备选、URL pin | 待迁移 | v0.2 |
| STREAM-03 | 在线 HLS/AAC 转码 | `proxy/transcode.py` FFmpeg fMP4 | 待迁移；保持 ffmpeg 外部进程 | v0.3 |
| STREAM-04 | 播放和下载音质独立 | `FNMUSIC_QUALITY_MODE` / `FNMUSIC_DL_QUALITY` | 待迁移 | v0.3 |
| META-01 | 歌词、封面、元数据 | /lyric、/metadata、/static/cover 拦截及 CDN 缓存 | 待迁移 | v0.2 |
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
