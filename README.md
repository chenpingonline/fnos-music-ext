# fnMusic Go

飞牛音乐（`trim.music`）原生扩展的 Go + Vue 3 重构起点。上游功能与实现清单请读 [docs/FEATURE_MATRIX.md](docs/FEATURE_MATRIX.md)，架构与安全见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)，分阶段任务见 [docs/MIGRATION_PLAN.md](docs/MIGRATION_PLAN.md)。

**当前已编码：** 纯 Go 原生网易云 EAPI/WEAPI（搜索、详情、歌词、音频链接、扫码登录、账号私有会话与自建歌单，扫码生成与待扫码查询已做匿名在线验证，其余以假服务测试验证）、官方 Unix Socket 透明代理、安全接管/恢复、健康检查、网易云 MusicBox 桥接搜索/播放/歌词/封面（需要单独运行 MusicBox）、可配置飞牛用户隔离/共享账号选择、动态账号绑定、MusicBox 桥接扫码登录、网易云自建歌单只读展示、管理 UI、x86_64/ARM64 独立 FPK 构建。

本轮已增加音质策略、App 下载与转码、搜索深分页、每日/热门推荐、公共歌单选择排序、边听边存、滚动缓存、封面/歌词落盘、目录设置和收藏自动绑定。入口位于管理面板新增的四个页面；实现、默认值与实机验收边界见 [播放与存储功能说明](docs/MUSIC_FEATURES.md)。

**当前尚未实现：** 独立 MusicBox 实例自动启动、musicdl、洛雪。音乐播放集成尚未经实际 fnOS 和源站验证，不能视为可直接替换原项目的正式版。

## 本机开发验证（不需要真正 NAS）

```bash
# Go 测试和 E2E 需要 Linux；构建可在 macOS 交叉编译
GOPROXY=off go test ./...
./scripts/e2e-local.sh
./scripts/build-binaries.sh
./scripts/build-fpk.sh --stage-only
```

测试 E2E 使用 `tests/fakeofficial` 创建可验证的 Unix Socket HTTP 服务，不接触 `/var/run/trim_music.socket`。

## UI

- Vue 3 + TypeScript 源码：`web/src/App.vue`；布局与主题位于 `app.css` / `theme.css`，账号登录请求生命周期位于 `useAccountPanel.ts`。
- 页面包含音源账号、访问设置、运行概览与帮助；支持账号搜索、对话框操作、本地二维码生成及歌单曲目分页。
- 前端验证：`cd web && npm ci && npm test && npm run build`。
- 有网络和 npm 环境时：`cd web && npm install && npm run build`。输出到 `internal/admin/assets/`，然后 **重新编译 Go** 以嵌入新的 UI。
- 当前内嵌的是编译后的 Vue 管理页。构建脚本使用锁文件执行 `npm ci`，先构建前端，再编译 Go；直接 `go build` 使用仓库内已生成的页面。
- 管理页通过 `/app/fnmusic-go/` 的管理 Socket 提供，主题使用官方 `@trimjs/web-app` SDK；宿主不可用时跟随浏览器偏好。

## FPK

`./scripts/build-fpk.sh` 构建前端及两种架构 Go 二进制，分别组装 `dist/fpk-stage/x86/`、`dist/fpk-stage/arm/`，然后调用官方 `fnpack`。产物为 `dist/fnmusic-go-<版本>-x86.fpk` 和 `dist/fnmusic-go-<版本>-arm.fpk`，不再生成 `platform=all` 包。可用 `--arch x86` 或 `--arch arm` 仅构建一种架构；`--stage-only` 仅准备目录。缺少 `fnpack` 时明确失败，不会伪造 `.fpk`。版本唯一来源是 `packaging/fpk/manifest.in` 的 `version`，构建脚本注入 CLI、API、页面和文件名。

FPK 声明了官方应用依赖 `trim.music`；生命周期脚本位于 `packaging/fpk/cmd/`。**未经 fnOS 实机安装验收，请不要在生产 NAS 上直接安装**。本项目按自用部署要求保留 root 运行。安装与升级仅准备服务配置，由应用中心调用 `cmd/main` 启停，不独立注册开机自启动。管理 Socket 自动检查 root 同伴或 nginx 工作进程及其 root 主进程；特殊网关可在实测后使用 `--gateway-uid` 显式指定。数据迁移、升级卸载和验收说明见 [fnOS 集成说明](docs/FNOS_INTEGRATION.md)。

## 命令（仅限 Linux）

```bash
# 查看目标 Socket 是否属于官方服务（不修改系统）
./dist/linux-amd64/fnmusic-go status

# 运行（触碰官方服务，需要 root；请先在隔离测试 NAS 验证！）
sudo ./dist/linux-amd64/fnmusic-go serve --admin-socket /var/apps/fnmusic-go/target/fnmusic-go.sock

# 意外退出而状态仍有 journal 时，人工验证归属再恢复
sudo ./dist/linux-amd64/fnmusic-go restore
```

`serve` 在退出时自行尝试恢复官方 Socket；`restore` 在未知进程/目标 inode 下会拒绝执行。不要运行两个彼此竞争的飞牛音乐扩展，不要手工删除仍有进程使用的 Socket。

## 许可与来源

独立重构实现基于公开的 fnmusic-ext 技术结构进行研究；原项目 MIT 许可不包含平台商业音乐版权/授权。详细见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## v0.3.0-preview · 网易云账号管理

本阶段新增 **可开关的用户隔离**：默认按飞牛用户 GUID 一对一绑定独立 MusicBox 实例；关闭隔离后，管理员可以在管理页面选择一个共享网易云账号，所有经飞牛身份验证的用户可使用该账号。切回隔离模式保留历史绑定，配置持久化在 `0600` 的 `${TRIM_PKGVAR}/accounts.json`，无需重启。

新增管理界面包括账号添加/删除、共享账号选择、飞牛用户绑定及配置错误提示。v0.5 推荐原生账号 `native://netease/<账号 ID>`，无需运行 Python；兼容模式仍可连接独立 MusicBox。 详情参见 [docs/ACCOUNT_MODES.md](docs/ACCOUNT_MODES.md)。

运行版本以 `packaging/fpk/manifest.in` 为准，构建时注入程序与管理页面。真实 fnOS 与音乐源的兼容性仍需设备验证。

## v0.4.0-preview · 登录与歌单

- 在管理员控制台为单个 MusicBox 实例生成网易云扫码挑战、手动检查扫码状态和登录状态。挑战密钥仅保留于 Go 内存 3 分钟，不存入账户文件；扫码二维码包含二维码密钥是扫码协议自身要求。服务端限制轮询频率，管理员管理接口必须经可信 fnOS 管理网关。
- 支持只读列出当前实例登录的网易云账号**自己创建**的歌单、歌单歌曲（最多 1000 首）；不会把“收藏的其他人歌单”混入列表。
- 对飞牛官方 `/music/api/v1/playlist/list`、`/playlist/detail`、`/track/playlist-detail/list` 进行只读注入，原始本地歌单照常透传。虚拟歌单 GUID 绑定已选择的实例，切换账户后不再识别前一实例的 GUID。
- 账号隔离模式和共享账号选择保持 v0.3 行为；媒体文件不自动下载、歌单修改不写入网易端。
- **依旧需要事先单独启动各账户的 MusicBox HTTP 进程，Python 音源依赖未被移除**。扫码挑战通过原 MusicBox 的 `POST /api/v1/auth/login` 和 `GET /api/v1/auth/login/check` 实现；单独端口的实例必须具有独立 cookie 保存目录。
- 后端保留 `qr_ascii` 兼容字段；当前 Vue 管理页根据官方登录链接本地生成图形二维码，并提供登录链接备用入口。
- 本机测试不等于音乐版权方授权，也不等于实际飞牛手机客户端适配完成；仍无真实 fnOS FPK 实机验收。

详细 API 和边界见 [v0.4 开发说明](docs/V0_4_RELEASE_NOTES.md)。


## v0.5.0-preview · Go 原生网易云适配

添加账号时选择 **Go 原生网易云（实验性）**，输入账号 ID（仅允许字母/数字/下划线/减号）和显示名称。后端生成 `native://netease/<账号ID>`，不启动 Python、MusicBox 或 Docker。原有 `http://127.0.0.1:<端口>` MusicBox 桥接账号保持兼容，允许混合使用两类账号。

### 原生实现范围

- 纯标准库 Go 的 EAPI 请求格式，提供搜索/分页、详情、歌词、音频 URL 获取、GET/HEAD/Range 转发（仅使用服务端根据当前账号权益返回的地址，试听/不可用内容拒绝）。
- 管理端生成扫码挑战、轮询扫码状态、验证登录状态；持久化 `MUSIC_U` 登录会话于 `accounts.json` 同级的 `netease-sessions/<accountID>.json`（0600，目录 0700）。后端返回官方登录链接，管理页在本机生成可扫描二维码，不将登录链接发送给外部二维码服务。
- 账号的自建歌单只读展示，先核对所登录网易云的账户 UID 再查歌单所有权，最多拉取 1000 首；每个账户独立 session/cookie；删除账号清理私有凭证。
- 继续沿用 fnOS 已验证用户 GUID 的隔离/共享开关与原生 Socket 的客户端适配。

### 重要限制

网易云 EAPI/WEAPI 属于**未公开、未承诺稳定**的协议。本地集成测试使用的是可控模拟响应，**尚未验证真实账号手机授权、VIP 权益、真实 fnOS App/FPK**（已验证匿名二维码生成和待扫码状态查询）。如上游接口变更，原生模式可能返回 401、403 或无法登录。切换到 MusicBox 桥接是手动兼容措施，并非保证可用。软件不附送音源凭证、不绕过收费或 DRM；仅在已授权范围内使用。实际互联网生产请求和版权/条款需用户自行确认。

详细见 [docs/V0_5_RELEASE_NOTES.md](docs/V0_5_RELEASE_NOTES.md)。
