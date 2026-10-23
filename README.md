# fnMusic Go

为飞牛音乐补充在线搜索、播放、推荐与歌单能力的 Go 原生扩展。

继续使用熟悉的飞牛音乐 Web 端或 App 听歌，通过独立管理面板配置网易云账号、音质、推荐和存储。后端使用 Go，管理界面使用 Vue 3 + TypeScript，支持 x86_64 与 ARM64 NAS。

本项目参考 [fnmusic-ext](https://github.com/javycoder/fnos_music_ext) 的功能设计与集成方式重新实现，网易云协议适配参考 [MusicBox](https://github.com/darknessomi/musicbox)。感谢原项目作者与贡献者。

> 当前为开发预览版，真实 fnOS 安装、官方客户端与音源兼容性仍需设备验收。版本以 [`manifest.in`](packaging/fpk/manifest.in) 为准，近期变化见 [v0.5.35 并发搜索说明](docs/V0_5_35_RELEASE_NOTES.md)。

## 主要功能

- **搜索与播放**：本地与所选在线音源按匹配程度分层，同层来源交错展示；支持深分页、歌词、封面、播放音质选择与失败降档。
- **账号与共享**：网易云扫码登录、独立会话保存；可选择全员共享一个账号，或按飞牛用户分配独立账号。
- **推荐与歌单**：每日推荐、热门推荐、自建歌单只读展示，以及公共歌单的浏览、选择与排序。
- **下载与存储**：App 下载、MP3 转码、边听边存、滚动缓存、封面与歌词保存，以及收藏自动落库绑定。
- **原生集成**：通过可恢复的 Unix Socket 代理适配官方音乐服务，提供接管、恢复与健康检查；管理面板跟随 fnOS 主题。

功能细节和默认设置见[播放与存储说明](docs/MUSIC_FEATURES.md)。已加入 [Go 原生 QQ 音乐（实验性）](docs/QQ_MUSIC_NATIVE.md)，支持手机 QQ 扫码；公共推荐目前仍仅支持网易云。未接入 musicdl、洛雪音源。

## 运行条件

| 项目 | 要求 |
| --- | --- |
| 系统 | fnOS 1.2.0 或更高版本 |
| 官方应用 | 已安装飞牛音乐（`trim.music`） |
| 架构 | x86_64 或 ARM64，安装对应架构的 FPK |
| 运行权限 | 当前按 root 运行，由 fnOS 应用中心管理启停 |
| FFmpeg | AAC/HLS、MP3 转码与封面嵌入需要设备提供可用 FFmpeg；FPK 不附带 |

支持以下原生音源：

- **Go 原生网易云**：直接在管理面板添加账号并扫码登录，无需 Python、MusicBox 或 Docker。
- **Go 原生 QQ 音乐（实验性）**：选择 QQ 音乐并使用手机 QQ 扫码，详情及验证边界见 [QQ 音乐接入说明](docs/QQ_MUSIC_NATIVE.md)。

账号在管理页扫码添加，不支持 MusicBox HTTP 桥接。

## 使用流程

1. 在测试 NAS 上安装对应架构的 FPK，并从应用中心启动扩展。首次使用需验证停用扩展后官方音乐能否恢复。
2. 打开 fnMusic Go 管理面板，在“音源账号”中添加 Go 原生网易云账号，完成扫码登录并检查登录状态。
3. 在“访问设置”中选择账号使用方式：关闭隔离并选择共享账号，或开启隔离、搜索飞牛用户名并分配账号。用户分配若显示等待音乐身份确认，需待官方音乐身份匹配后才能生效。
4. 返回飞牛音乐 Web 端或 App 搜索、播放；需要推荐、公共歌单或自动保存时，再到对应管理页面配置。

管理入口为 `/app/fnmusic-go/`，需通过可信 fnOS 管理网关访问。更多安装、升级、数据迁移与卸载说明见 [fnOS 集成文档](docs/FNOS_INTEGRATION.md)。

## 开发与验证

搜索并行获取本地、网易云和 QQ 音乐结果，每个来源每批最多取 20 条。显式传入 `page` / `size` 的客户端按需取数，够当前页面就返回；当前官方 Web 的歌曲搜索不请求后续页，因此仅传 `q` 时使用快速模式：每个来源最多扫描前 100 条候选，整个搜索阶段最多等待 3 秒（设置更短时遵循设置），返回截止时已取得的结果；结果可能少于完整搜索，也可能超过 50 条。快速模式先查每个音源第一页以确认总量，再并发获取其余分页：每次搜索最多 4 个在线请求、每个音源最多 2 个。按页序合并成功结果后，统一按“歌名与歌手完全匹配、歌名完全匹配、歌名部分匹配、歌手匹配、其他”分层，同层来源轮询；显式分页模式后续批次追加到缓存末尾，保留已展示结果的顺序。不同来源、专辑、时长及 Live／翻唱／伴奏版本保留独立结果。

缓存按用户、所选账号和搜索设置隔离，保留 5 分钟；改变页大小不会重排已取得的结果。显式分页请求开启深分页时，每个来源的候选预算为设置页数 × 100 条，关闭时预算为 100 条；快速模式固定最多 100 条候选。显式分页请求不会在首屏一次取完，总数先采用各平台返回的数量估算，过滤完成或达到预算后校准；仅传 `q` 的请求以实际返回条数作为总数。单个来源每批最多等待 3 秒，快速模式另有覆盖所有批次的 3 秒总预算，本地查询也纳入搜索超时；中断前取得的结果保留。不完整搜索在 10 秒冷却后，从第一页重新搜索时可重试失败来源，继续翻页时保持原顺序。快速模式与深分页缓存隔离；已取得的本地响应会复用，避免逐批重复查询。响应的 `Server-Timing` 区分本地响应和来源合并耗时，快速模式标记 `X-FnMusic-Search-Mode: quick`。搜索失败仍保留官方响应回退。

可在“播放与搜索 → 搜索来源”选择网易云和 QQ 音乐；在“访问设置 → 账号组合”明确授权附加平台账号后，支持本地与两个在线平台同时搜索。歌单显示来源独立设置。

### 本地验证

Go 测试与 Socket E2E 需在 Linux 环境执行；E2E 使用 `tests/fakeofficial` 模拟官方服务，不接触真实的 `/var/run/trim_music.socket`。

```bash
go test ./...
./scripts/e2e-local.sh
```

前端验证：

```bash
cd web
npm ci
npm test
npm run build
```

前端源码位于 `web/src/`，构建输出到 `internal/admin/assets/`。修改前端后需重新编译 Go，才能更新程序内嵌的管理页面。主题对接官方 `@trimjs/web-app` SDK，宿主不可用时跟随浏览器偏好。

### 构建二进制与 FPK

构建需要 Go 1.27.3 或更高版本、Node.js 22.23.4 / 24.22.1 / 26.12.1 或相应系列更高安全版本、npm 和 Python 3，生成 FPK 还需安装官方 `fnpack`。构建脚本会先检查工具链安全版本。Go 二进制内嵌标准库，修复 CVE-2026-111943 必须重新编译并更新 FPK；仅升级 NAS 上的 Go 无效。Node.js 仅用于构建，不随 FPK 分发。脚本会先通过锁文件安装前端依赖并构建页面，再交叉编译 Linux 二进制，可在 macOS 执行。

```bash
# 只构建前端和双架构二进制
./scripts/build-binaries.sh

# 构建双架构 FPK
./scripts/build-fpk.sh

# 仅构建指定架构
./scripts/build-fpk.sh --arch x86
./scripts/build-fpk.sh --arch arm

# 构建并准备打包目录，不调用 fnpack
./scripts/build-fpk.sh --stage-only
```

| 产物 | 路径 |
| --- | --- |
| x86_64 二进制 | `dist/linux-amd64/fnmusic-go` |
| ARM64 二进制 | `dist/linux-arm64/fnmusic-go` |
| x86_64 安装包 | `dist/fnmusic-go-<版本>-x86.fpk` |
| ARM64 安装包 | `dist/fnmusic-go-<版本>-arm.fpk` |
| 打包目录 | `dist/fpk-stage/x86/`、`dist/fpk-stage/arm/` |

每次执行实际 FPK 打包时，脚本会自动将 `packaging/fpk/manifest.in` 的补丁版本号加 1，再构建前端与二进制；双架构同一次打包共用一个新版本号。重新打包也必须使用新版本号，构建失败已递增的版本号不回退。`--stage-only` 和单独构建二进制不递增版本号。

版本统一读取 `packaging/fpk/manifest.in`，并注入程序、管理页面和产物文件名。生命周期脚本位于 `packaging/fpk/cmd/`；安装和升级准备服务配置，由应用中心调用 `cmd/main` 启停。

### Linux 调试命令

以下以 x86_64 为例，ARM64 使用 `dist/linux-arm64/`。`serve` 会接管官方 Socket，应先在测试 NAS 上验证；日常安装后通过应用中心启停。

```bash
# 检查官方 Socket 状态，不修改系统
./dist/linux-amd64/fnmusic-go status

# 启动服务，需要 root
sudo ./dist/linux-amd64/fnmusic-go serve --admin-socket /var/apps/fnmusic-go/target/fnmusic-go.sock

# 意外退出且仍有 journal 时，核实归属后手动恢复
sudo ./dist/linux-amd64/fnmusic-go restore
```

`serve` 退出时会尝试恢复官方 Socket；`restore` 遇到未知进程或目标 inode 时拒绝操作。不要同时运行多个接管同一 Socket 的扩展，也不要手动删除仍被进程使用的 Socket。特殊网关的 `--gateway-uid` 配置与检查机制见 [fnOS 集成文档](docs/FNOS_INTEGRATION.md)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [播放与存储](docs/MUSIC_FEATURES.md) | 当前功能、默认值、资源限制与验收边界 |
| [fnOS 集成](docs/FNOS_INTEGRATION.md) | 安装、升级、数据目录、网关与恢复 |
| [架构与安全](docs/ARCHITECTURE.md) | 代理、账号隔离与服务边界 |
| [功能迁移矩阵](docs/FEATURE_MATRIX.md) | 参考项目对照及历史实现进度 |
| [迁移计划](docs/MIGRATION_PLAN.md) | 分阶段设计与后续计划 |

## 参考项目与致谢

| 项目 | 参考内容 |
| --- | --- |
| [javycoder/fnos_music_ext](https://github.com/javycoder/fnos_music_ext) | 主要功能设计与协议行为，包含 Socket 接管/恢复、搜索与播放、MusicBox 桥接、歌单注入、转码存储及 fnOS FPK 集成 |
| [darknessomi/musicbox](https://github.com/darknessomi/musicbox) | 网易云接口调用、EAPI/WEAPI 加密、扫码登录与会话处理，主要参考 `NEMbox/api.py` 和 `NEMbox/encrypt.py` |

本项目以 Go + Vue 3 独立重构，具体参考与迁移记录见[功能矩阵](docs/FEATURE_MATRIX.md)、[fnOS 集成说明](docs/FNOS_INTEGRATION.md)和[第三方来源记录](THIRD_PARTY_NOTICES.md)。

## 许可与使用边界

本项目采用 [MIT License](LICENSE)。代码许可证不包含音乐内容或平台 API 的使用授权；音频可用性取决于源站、歌曲资源和账号权益，本项目不绕过会员限制或 DRM。

网易云接口可能随上游调整而失效。当前本地及模拟服务验证不能替代真实设备验收；播放 403 等兼容性修正仍需在 NAS 上复测，详见[发布说明](docs/V0_5_25_RELEASE_NOTES.md)。

## 历史开发阶段

<details>
<summary>展开 v0.3–v0.5 初期开发记录</summary>

以下内容描述对应阶段的能力与限制，保留供迁移追溯。当前行为以[播放与存储说明](docs/MUSIC_FEATURES.md)及最新发布说明为准。

### v0.3.0-preview · 网易云账号管理

本阶段新增 **可开关的用户隔离**：默认按飞牛用户 GUID 一对一绑定独立网易云账号；关闭隔离后，管理员可以在管理页面选择一个共享网易云账号，所有经飞牛身份验证的用户可使用该账号。切回隔离模式保留历史绑定，配置持久化在 `0600` 的 `${TRIM_PKGVAR}/accounts.json`，无需重启。

新增管理界面包括账号添加/删除、共享账号选择、飞牛用户绑定及配置错误提示。当前仅支持原生账号 `native://netease/<账号 ID>`，无需运行 Python。 详情参见 [docs/ACCOUNT_MODES.md](docs/ACCOUNT_MODES.md)。

运行版本以 `packaging/fpk/manifest.in` 为准，构建时注入程序与管理页面。真实 fnOS 与音乐源的兼容性仍需设备验证。

### v0.4.0-preview · 登录与歌单（历史实现，桥接已移除）

- 在管理员控制台为单个 MusicBox 实例生成网易云扫码挑战、手动检查扫码状态和登录状态。挑战密钥仅保留于 Go 内存 3 分钟，不存入账户文件；扫码二维码包含二维码密钥是扫码协议自身要求。服务端限制轮询频率，管理员管理接口必须经可信 fnOS 管理网关。
- 支持只读列出当前实例登录的网易云账号**自己创建**的歌单、歌单歌曲（最多 1000 首）；不会把“收藏的其他人歌单”混入列表。
- 对飞牛官方 `/music/api/v1/playlist/list`、`/playlist/detail`、`/track/playlist-detail/list` 进行只读注入，原始本地歌单照常透传。虚拟歌单 GUID 绑定已选择的实例，切换账户后不再识别前一实例的 GUID。
- 账号隔离模式和共享账号选择保持 v0.3 行为；媒体文件不自动下载、歌单修改不写入网易端。
- 当时需要事先单独启动各账户的 MusicBox HTTP 进程；当前版本已移除这一桥接实现。扫码挑战通过原 MusicBox 的 `POST /api/v1/auth/login` 和 `GET /api/v1/auth/login/check` 实现；单独端口的实例必须具有独立 cookie 保存目录。
- 后端保留 `qr_ascii` 兼容字段；当前 Vue 管理页根据官方登录链接本地生成图形二维码，并提供登录链接备用入口。
- 本机测试不等于音乐版权方授权，也不等于实际飞牛手机客户端适配完成；仍无真实 fnOS FPK 实机验收。

详细 API 和边界见 [v0.4 开发说明](docs/V0_4_RELEASE_NOTES.md)。


### v0.5.0-preview · Go 原生网易云适配

添加账号时填写显示名称并使用网易云 App 扫码。后端自动生成账号 ID 和 `native://netease/<账号ID>`，无需 Python 或 Docker。旧版 HTTP 桥接已移除。

#### 原生实现范围

- 纯标准库 Go 的 EAPI 请求格式，提供搜索/分页、详情、歌词、音频 URL 获取、GET/HEAD/Range 转发（仅使用服务端根据当前账号权益返回的地址，试听/不可用内容拒绝）。
- 管理端生成扫码挑战、轮询扫码状态、验证登录状态；持久化 `MUSIC_U` 登录会话于 `accounts.json` 同级的 `netease-sessions/<accountID>.json`（0600，目录 0700）。后端返回官方登录链接，管理页在本机生成可扫描二维码，不将登录链接发送给外部二维码服务。
- 账号的自建歌单只读展示，先核对所登录网易云的账户 UID 再查歌单所有权，最多拉取 1000 首；每个账户独立 session/cookie；删除账号清理私有凭证。
- 继续沿用 fnOS 已验证用户 GUID 的隔离/共享开关与原生 Socket 的客户端适配。

#### 重要限制

网易云 EAPI/WEAPI 属于**未公开、未承诺稳定**的协议。本地集成测试使用的是可控模拟响应，**尚未验证真实账号手机授权、VIP 权益、真实 fnOS App/FPK**（已验证匿名二维码生成和待扫码状态查询）。如上游接口变更，原生模式可能返回 401、403 或无法登录。软件不附送音源凭证、不绕过收费或 DRM；仅在已授权范围内使用。实际互联网生产请求和版权/条款需用户自行确认。

详细见 [docs/V0_5_RELEASE_NOTES.md](docs/V0_5_RELEASE_NOTES.md)。

</details>
