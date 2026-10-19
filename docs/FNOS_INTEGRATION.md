# fnOS 集成与本地验收

## 包、版本与权限

- `packaging/fpk/manifest.in` 的 `version` 是唯一版本源。`platform` 是构建占位符，生成的 manifest 分别填写 `x86`、`arm`；每个包仅包含对应架构的 ELF。
- `scripts/build-fpk.sh` 使用 npm 锁文件构建 Vue 页面，再将版本注入 Go CLI、健康接口、管理 API、页面及 FPK 文件名。构建不会自动提交、推送或发布。
- 本项目是自用扩展，按部署要求保留 root 服务；`config/privilege` 和 systemd `User=root` 一致。
- 原生网易云登录凭证仍只在本机私有目录保存，文件 0600、目录 0700。

## 应用目录与旧数据迁移

| 内容 | 新位置 |
| --- | --- |
| 账号配置 | `${TRIM_PKGVAR}/accounts.json` |
| 原生登录会话 | `${TRIM_PKGVAR}/netease-sessions/` |
| MusicBox 兼容配置 | `${TRIM_PKGETC}/provider.env` |
| 接管 journal、临时 Socket | `/run/fnmusic-go/` |
| 管理网关 Socket | `${TRIM_APPDEST}/fnmusic-go.sock` |

环境变量缺失时，脚本使用应用目录中的 `target`、`var`、`etc`，不根据其他架构二进制是否存在来猜测安装目录。

安装/升级将旧 `/var/lib/fnmusic-go/` 中上述已知数据复制到应用目录，保留旧目录作为恢复备份，不覆盖不同内容。`.legacy-migrated` 标记防止重复安装重新导入已经删除或修改的账号。中途失败可以重试；冲突需要先备份并人工确认。迁移来源及凭证不接受软链接；fnOS 托管目录本身允许系统创建的软链接，写服务配置前会解析为实际路径。

运行状态是路径规范的必要例外：Socket 发布依赖 `renameat2`，临时 Socket 必须和官方 `/var/run` Socket 在同一文件系统，所以继续使用 `/run/fnmusic-go/`，不能放在可能位于数据卷的 `TRIM_PKGTMP` 中。

升级前先使用旧服务停止并恢复 Socket；若 `/run/fnmusic-go/ownership.json` 仍在，则尝试按旧 journal 安全恢复。无法验证恢复时中止升级或卸载。

卸载脚本清除本应用的 unit 和已知 provider drop-in，不主动删除用户数据或旧备份。fnOS 应用中心是否保留应用目录仍取决于卸载选项及系统行为；卸载前需要保留数据时先备份 `var`、`etc`。

## 启停与失败恢复

- 安装/升级回调仅写入服务配置，不调用 `enable --now`。旧服务的开机自启会被禁用，由应用中心调用 `cmd/main` 控制服务。
- `main start` 等待管理接口确认 journal 存在、公开 Socket 属于当前代理、上游 Socket 可连接。失败时停止并尝试恢复官方 Socket；未就绪不会报告已运行。
- `main status` 在服务运行且健康时返回 0，否则返回 3。
- `ExecStopPost` 使用幂等的 `restore-if-needed`，正常退出已恢复 Socket 时不会再报 journal 缺失。
- 修改和停止 unit 前先检查安装目录归属。旧版已知 drop-in 可以清理；同名文件被手动修改时拒绝覆盖。重载配置失败时恢复旧 unit/drop-in。错误写入 `TRIM_TEMP_LOGFILE`。

systemd 仍是本应用的进程管理器，但不独立维护开机启用状态。真正的 fnOS 安装后启停顺序、重启和升级状态恢复需要目标设备验证。

## 网关与主题

管理 Socket 使用 `SO_PEERCRED`。root 连接始终可用；其他连接必须来自 root 所有、组/其他用户不可写的 nginx 程序，其父进程需为同一程序的 root 主进程。无法识别时拒绝连接，不信任任意本地进程伪造的管理员 Header。非 nginx 网关可在实测 UID 后配置 `--gateway-uid`，该显式配置也允许 root 执行就绪检查。

业务管理员鉴权继续使用官方网关的 `X-Trim-Isadmin`。写请求还需 JSON、`X-FnMusic-Admin`，以及同源 Origin 或通过管理员接口取得的用户绑定写入令牌；明确的跨站浏览器请求仍会拒绝。网关实际使用的进程模型及 Header 注入仍需真机确认。

前端使用官方 `@trimjs/web-app`：等待初始化后调用 `getPlatformConfig()`，Web 宿主监听 `os/theme`，卸载用原回调解绑；较新的事件优先于初始请求的旧响应。独立浏览器、旧宿主或初始化失败时使用浏览器主题及其 change 事件，没有主题轮询。移动端只承诺初始化主题。旧 fnOS 可使用浏览器兜底，官方主题能力要求 fnOS 1.2.0401 / App 1.34.0 及以上。

## 验收边界

本地可以验证：前端类型检查/构建、主题生命周期测试、Linux Go 单元测试、模拟官方服务的 Socket E2E、隔离容器的生命周期/迁移测试、双架构 ELF 和官方 fnpack 打包。

`tests/lifecycle.sh` 只允许在可丢弃 Linux 容器中设置 `FNMUSIC_LIFECYCLE_TEST=isolated-container` 后执行；它使用假的 systemctl 和服务程序，在容器内验证脚本，不证明真实 systemd/fnOS 调度行为。

界面验证使用实际编译后的页面及本地假 API、模拟浏览器主题。它不证明真实宿主主题事件已通过验收。

仍需真机：首次安装、停止后重启设备、升级运行中/已停止的应用、卸载保留数据、nginx 网关连接及宿主主题切换、官方音乐升级或重启后的接管兼容性。官方音乐 Socket 接管依赖内部实现，无法由官方开放 API 文档保证稳定；身份/inode 变化时安全恢复会拒绝操作，不能通过删除未知 Socket 强行继续。

## 0.5.2 启动配置修正

0.5.1 生成的 `WorkingDirectory="/…"` 会被 systemd 当作包含双引号的路径，导致 `bad unit file setting`，应用中心仅显示“脚本第 12 行”失败。`EnvironmentFile=-"/…"` 同样会被判为非绝对路径而忽略。0.5.2 改为这两个配置项所需的路径写法，`ExecStart`/`ExecStopPost` 的命令参数仍按 systemd 的命令行语法保留引号。

启动失败时会将 systemctl 的实际错误和 LoadState、ActiveState、Result、ExecMainStatus 写入应用中心错误详情。生命周期测试现在必须提供 `systemd-analyze`，并要求生成的 unit 通过 `systemd-analyze verify`，且没有警告输出；不再因容器缺少此工具而跳过验证。已在 systemd 255 容器验证，仍需 fnOS 实机确认。

## 0.5.3 网关写入校验修正

网关改写后端 Host 时，浏览器的外部 Origin 可能无法通过原有比较，正常添加账号被返回 403。此前前端又把所有 403 显示为管理员权限不足，混淆了身份验证与请求校验。现在浏览器每次写入前获取绑定当前用户、随服务重启失效的 HMAC 校验令牌；有效令牌允许 Host 改写，管理员验证和跨站请求限制继续保留。两类失败分别返回错误码和提示。

已通过 20 项前端测试、Linux 管理接口测试（含用户隔离、非管理员拒绝、跨站拒绝和重启失效），并在实际 Go 接口改写 Host 的本地环境用浏览器添加账号、刷新确认持久化。此验证未连接真实 NAS，实际网关 Header 和安装后的行为仍需设备确认。

## 0.5.4 扫码授权结果修正

生成二维码后每 2.5 秒串行查询授权结果；成功、过期、关闭或切换账号时停止。活动二维码期间“检查状态”也查询扫码挑战，而不是仅查询尚未保存的会话。修复原生 EAPI 从 Set-Cookie 响应头读取 MUSIC_U 等登录 Cookie，继续兼容桥接接口的 JSON cookie 字段；缺少有效会话时不报告登录成功。授权凭证仍只保存在 NAS 私有目录。

本地验证包含原生响应头 Cookie 的保存与无凭证拒绝、自动检查从待扫码到已扫码/成功、关闭停止检查，以及模拟接口的浏览器流程。未使用用户真实账号扫码，真实网易云授权仍需升级后验证。

## 0.5.5 按 MusicBox 对齐原生扫码协议

对照原项目的 MusicBox 调用与 MusicBox 的 `login_qr_key` / `login_qr_check`，将原生二维码生成、授权检查及账号信息查询改为 `music.163.com/weapi/`。搜索与播放继续使用原有 EAPI。新增标准库实现的 WEAPI 加密（双层 AES-CBC / RSA），账号各自使用独立 Cookie jar，在同一设备上下文中携带二维码响应 Cookie；成功后保存有效 MUSIC_U 私有会话，重启加载恢复。账号查询兼容 `/weapi/w/nuser/account/get` 回退。

扫码状态兼容顶层与 `data.code`，非 800–803 返回明确的数字诊断错误，不再静默转成 unknown，也不透传上游原始内容或凭证。验证包含独立 Python 参考算法的固定向量、Cookie/设备连续性、待扫码→待确认→成功、会话重载、账号查询回退和异常状态码。还通过真实网易云的匿名生成二维码与状态查询（返回 pending）；未进行真实用户授权，不能由此证明手机确认后的流程已经实机通过。

参考： https://github.com/javycoder/fnos_music_ext/blob/main/musicbox-service/app.py 、 https://github.com/darknessomi/musicbox/blob/master/NEMbox/api.py 、 https://github.com/darknessomi/musicbox/blob/master/NEMbox/encrypt.py 。

## 0.5.6 公共歌单空列表注入修正

对照原项目 `playlist_list`，兼容官方空曲库响应的 `list: null`、缺少 list、data 为 null 或省略 data；规范化后将选中的公共歌单按配置顺序加到列表前，保留官方歌单与原有字段。支持 `/playlist/list/…` 路由变体。非空错误类型和官方错误响应继续原样返回，不以在线列表覆盖官方错误。

勾选仍需开启公共歌单开关并保存，预览账号不会隐式绑定用户。推荐页明确区分未配置音源访问、公共歌单关闭、选择未保存和已保存状态，并提供访问设置入口。隔离模式仍只对已绑定用户注入，未绑定用户不会借用其他人的网易云凭证。

回归测试覆盖八种官方响应/路由、选中顺序、详情打开、曲目列表、关闭开关后的恢复，以及既有跨用户隔离。本地模拟接口与浏览器验证不替代真实 fnOS 音乐版本的验收。参考： https://github.com/javycoder/fnos_music_ext/blob/main/proxy/app.py 。

## 0.5.7 个人歌单独立开关

推荐歌单页新增“显示网易云个人歌单”，独立于热门推荐、每日推荐及公共歌单。关闭并保存后不再获取或注入网易云个人歌单，已注入的个人歌单详情也不再提供；公共榜单按已保存的选择继续显示，官方本地歌单仍透传。此设置只影响飞牛音乐里的展示，不修改网易云歌单。旧配置缺少该字段时默认开启以保留现有行为，显式关闭后持久保存。

已验证旧配置兼容、关闭后保存重载、个人歌单与排行榜的独立控制，以及浏览器开关保存流程。真实 NAS 上需保存设置后刷新或重新打开飞牛音乐，让客户端重新读取列表。

## 0.5.8 歌单批量详情与封面链路

补齐飞牛音乐使用的 `playlist/batch-detail?guids=…`（兼容 guid 和逗号列表），返回当前用户可见的在线歌单元数据；混合本地歌单时仅将本地 ID 交给官方接口，保留本地条目与额外字段，官方错误仍原样返回。个人歌单开关和账号切换同样约束批量详情与封面，不暴露隐藏歌单。公共歌单缺少数量时按实际曲目补齐详情计数。

歌单 coverId 按参考项目使用 `track_<32hex>`，同源 static/cover 接口识别作用域并转发真实封面；没有独立封面时使用歌单曲目封面。Native 和 MusicBox 都复用受限图片传输（仅公网地址、图片类型、6 MiB 上限、不向图片站发送登录凭证），支持 GET/HEAD。

新增列表→批量详情→曲目→封面完整合同测试，含混合官方歌单、参数别名、关闭个人歌单和跨用户拒绝，以及图片地址/类型/大小校验。已实际验证匿名热歌榜返回 200 首曲目；真实用户歌单及 fnOS 客户端仍需设备验收，不能由本地模拟响应证明。参考： https://github.com/javycoder/fnos_music_ext/blob/main/proxy/app.py 。

## 0.5.9 播放会话

新增播放准备、AAC/HLS 分片、心跳/退出和独立于下载任务的会话管理；说明和验收边界见 MUSIC_FEATURES.md 的 0.5.9 节。私有工作目录位于系统临时目录，应用关闭清理，空闲和容量超限时回收。源站地址和凭证不传入 FFmpeg 参数，媒体由 Go 按已验证用户获取；仅允许 file/pipe 输入协议。

音乐业务写请求允许浏览器明确的 same-origin Fetch Metadata 通过网关 Host 改写，仍拒绝 cross-site / same-site；没有该 Header 时继续核对 Origin，原生客户端仍通过官方网关身份验证。此调整不移除管理 API 的管理员/CSRF 校验。

## 0.5.10 用户名分配

访问设置提供本机用户名搜索和选择、当前用户标记、音乐身份待确认状态。用户名分配与 GUID 映射、原有 GUID 绑定都保存在私有 accounts.json 中；不得把统一网关 UID 直接作为音乐 GUID。详细规则、共享模式和本机目录范围见 ACCOUNT_MODES.md。
