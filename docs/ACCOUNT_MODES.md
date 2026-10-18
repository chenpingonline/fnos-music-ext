# 网易云账号访问模式（v0.3.0-preview）

此功能作用于 fnMusic Go 的 MusicBox HTTP 桥接适配器。每个配置项代表一个**独立的 MusicBox 实例/登录态**，而不是 Go 内部的一套多账号 Cookie。Go 目前不实现网易云扫码、登录或第三方服务的启动；配置前请在不同本机端口启动相互隔离的 MusicBox 服务，且仅接入拥有使用权限的账号。不同账号不应填写相同 HTTP 地址。

## 模式

- **用户隔离（默认开启）**：管理员以 fnOS 用户 GUID 将每位用户绑定到不同网易云账号；同一个账号不能绑定给两个 fnOS 用户。没有绑定的用户只能看到本地音乐。
- **共享账号（隔离关闭）**：管理员在下拉框选择一个已添加的网易云账号，所有经飞牛官方 `/music/api/v1/user/me` 身份验证的用户都可使用该账号的在线能力，**但未验证者仍不能访问**。未选择账号时在线能力关闭。
- 模式切换无需重启；独立模式绑定不会因为切到共享模式而删除。切回独立模式立即恢复原绑定。
- 共享的是在线 MusicBox 实例及其云端登录态，不包括本地飞牛用户数据库。**启用共享即表示允许所有飞牛音乐用户使用该网易云账号的资源和权益，请先取得账号持有者许可。**

## 管理页面

飞牛管理员从桌面 `fnMusic Go` 进入：

1. 先添加账号标识、显示名和 `http://127.0.0.1:<port>` MusicBox 地址。
2. 关闭“账号隔离”开关后，在“共享网易云账号”中选择一个账号即可对全部经验证的飞牛用户启用。
3. 开启“账号隔离”后，为每个飞牛用户 GUID 选择独立账号。未绑定用户仅使用官方本地音乐。
4. 删除一个账号会删除相应绑定，并清空对它的共享选择；MusicBox 实例和登录态不会自动删除。

管理 API（仅可信本地网关管理员请求）：

- `GET /app/fnmusic-go/api/accounts` 获取模式/账户/绑定。
- `POST /app/fnmusic-go/api/accounts` 增加账号；JSON `{ "id":"family", "name":"家庭", "bridge_url":"http://127.0.0.1:8770" }`。
- `DELETE /app/fnmusic-go/api/accounts/{id}` 删除。
- `PUT /app/fnmusic-go/api/accounts/mode` JSON `{ "isolation":false }`。
- `PUT /app/fnmusic-go/api/accounts/shared` JSON `{ "account_id":"family" }`。
- `PUT /app/fnmusic-go/api/accounts/bindings/{fnosGUID}` JSON `{ "account_id":"family" }`；空值解除绑定。

写操作必须来自通过 fnOS 网关身份验证的管理员、携带 `X-FnMusic-Admin: 1` 与 `Content-Type: application/json`。这些 HTTP 头不是独立鉴权凭据；必须配合可信本地 Unix Socket 同伴 UID 校验，并确保网关剥离外部注入的管理员标记。

配置会原子写入 `/var/lib/fnmusic-go/accounts.json`，目录 0700、文件 0600。**不保存网易云密码或 Cookie**，Cookie 应由每个 MusicBox 实例独立保存。升级迁移逻辑支持以前单账号的 URL + fnOS GUID 环境变量一次性导入为 `legacy` 账号。

## 已知限制

- 目前账号是独立 MusicBox 实例的映射，尚不支持在 Go 管理端完成扫码登录。
- 登录后的私人歌单、收藏/推荐数据接入官方飞牛音乐的功能尚未迁移完成。
- 仍需真机验证 fnOS 网关管理员头的可信链路、不同设备的手机 App 行为。
- 对在线歌曲仅提供用户有权使用的访问，不能绕过会员、DRM 或平台授权。
