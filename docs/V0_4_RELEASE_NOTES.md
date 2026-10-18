# v0.4.0-preview · 网易云扫码与歌单

## 架构

- `internal/netease/auth_playlists.go`：桥接现有 MusicBox HTTP `/api/v1/auth/login`、`/api/v1/auth/login/check`、`/api/v1/auth/status`、`/api/v1/user/playlists` 和 `/api/v1/user/playlists/{id}/tracks`。
- `internal/admin/login.go`：独立 QR 会话表，按账号选择 MusicBox 实例，仅在管理员网关开放接口。扫码会话 3 分钟失效；最短轮询间隔 2 秒。
- `internal/gateway/playlists.go`：官方歌单的只读注入、详情、曲目列表；保留本地歌单原始内容并在音源故障时透传。
- `internal/fnmusic/playlist.go`：歌曲 ID 与歌单 ID 分离，虚拟歌单 ID 对账号服务地址及配置命名空间绑定。

## 管理 API

管理 API 仅能通过已验证 fnOS 管理员页面访问，前缀为 `/app/fnmusic-go/api`。下面的 `:id` 是本项目中配置的账号标识，不是网易云 UID。

| 方法 | 相对 URL | 功能 |
|---|---|---|
| GET | `/accounts/:id/auth/status` | 登录状态查询 |
| POST | `/accounts/:id/auth/qr` | 发起扫码挑战；返回 `qr_url`、可选 `qr_ascii`，不返回单独 `unikey` 字段 |
| POST | `/accounts/:id/auth/check` | 根据服务端暂存的 key 检查扫码结果（801 待扫 / 802 已扫码 / 803 成功 / 800 过期） |
| GET | `/accounts/:id/playlists` | 当前账号自建歌单（只读） |
| GET | `/accounts/:id/playlists/:playlistID/tracks` | 检查歌单所有权后返回曲目（只读） |

扫码中 `qr_url` 本身包含挑战参数，这是第三方扫码协议要求；只能在经管理员鉴权的页面展示，不能写入日志。管理 API 所有 POST 均要求 `Content-Type: application/json` 和 `X-FnMusic-Admin: 1`，后台还依赖网关已验证的 `X-Trim-Isadmin`，必须确保来自客户端的同名头不能伪造。

## 已知限制

1. 本版仍是 Go + Vue 管理与网关 + Python MusicBox 音源服务的过渡架构；不包含网易云直接协议实现或实例自动化创建。
2. 歌单只读，不提供在网易云上的增删修改，不进行自动下载、会员权限绕过。
3. Vue 生产构建需要 npm 依赖；当前环境离线无 npm 缓存，随程序提供功能同步的内嵌管理页面。
4. 官方飞牛音乐 API 非公开契约，`isDaily`、歌单详情及手机端兼容需要真实 fnOS 版本回归测试。
5. FPK staging 双架构构建成功不代表已在 fnOS 应用中心验证安装、权限和自动恢复。
