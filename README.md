# fnMusic Go v0.4.0-preview

飞牛音乐（`trim.music`）原生扩展的 Go + Vue 3 重构起点。上游功能与实现清单请读 [docs/FEATURE_MATRIX.md](docs/FEATURE_MATRIX.md)，架构与安全见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)，分阶段任务见 [docs/MIGRATION_PLAN.md](docs/MIGRATION_PLAN.md)。

**当前已编码：** 官方 Unix Socket 透明代理、安全接管/恢复、健康检查、网易云 MusicBox 桥接搜索/播放/歌词/封面（需要单独运行 MusicBox）、可配置飞牛用户隔离/共享账号选择、动态账号绑定、MusicBox 桥接扫码登录、网易云自建歌单只读展示、管理 UI、x86_64/ARM64 构建和 FPK staging。

**当前尚未实现：** 纯 Go 网易云协议与二维码图形生成、独立 MusicBox 实例自动启动、musicdl、洛雪、在线收藏、下载与推荐。音乐播放集成尚未经实际 fnOS 和源站验证，不能视为可直接替换原项目的正式版。

## 本机开发验证（不需要真正 NAS）

```bash
GOPROXY=off go test ./...
./scripts/e2e-local.sh
./scripts/build-binaries.sh
./scripts/build-fpk.sh --stage-only
```

测试 E2E 使用 `tests/fakeofficial` 创建可验证的 Unix Socket HTTP 服务，不接触 `/var/run/trim_music.socket`。

## UI

- Vue 3 + TypeScript 源码：`web/src/App.vue`。
- 有网络和 npm 环境时：`cd web && npm install && npm run build`。输出到 `internal/admin/assets/`，然后 **重新编译 Go** 以嵌入新的 UI。
- 由于构建环境不能访问 npm，本仓库随附了可离线使用的静态后备管理页，**当前二进制内嵌的是已具备账号管理功能的原生后备管理页，不是已经编译完成的 Vue 运行时**。
- 后备页/正式 Vue 都通过 `/app/fnmusic-go/` 的管理 Socket 提供。

## FPK

`./scripts/build-fpk.sh` 构建双架构 Go 二进制并组装 `dist/fpk-stage/`，然后调用系统已安装的 `fnpack`。缺少 `fnpack` 时明确失败，不会伪造 `.fpk`。

FPK 声明了官方应用依赖 `trim.music`；生命周期脚本位于 `packaging/fpk/cmd/`。**未经 fnOS 实机安装验收，请不要在生产 NAS 上直接安装**。v0.1 的系统接管服务仍以 root 身份运行，正式版需要拆分特权辅助进程。默认管理网关 UID 为 0，不保证匹配真实 fnOS Nginx worker，真机验证时需要测得 UID 后传入 `--gateway-uid`。

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

本阶段新增 **可开关的用户隔离**：默认按飞牛用户 GUID 一对一绑定独立 MusicBox 实例；关闭隔离后，管理员可以在管理页面选择一个共享网易云账号，所有经飞牛身份验证的用户可使用该账号。切回隔离模式保留历史绑定，配置持久化在 `0600` 的 `/var/lib/fnmusic-go/accounts.json`，无需重启。

新增管理界面包括账号添加/删除、共享账号选择、飞牛用户绑定及配置错误提示。所有账号都必须预先运行在各自的本机 MusicBox HTTP 服务中；**v0.4 可调用 MusicBox 的扫码接口，但并非纯 Go 网易云协议实现。** 详情参见 [docs/ACCOUNT_MODES.md](docs/ACCOUNT_MODES.md)。

运行版本：`v0.4.0-preview`。仍需真实 fnOS 与音乐源授权、兼容验证。

## v0.4.0-preview · 登录与歌单

- 在管理员控制台为单个 MusicBox 实例生成网易云扫码挑战、手动检查扫码状态和登录状态。挑战密钥仅保留于 Go 内存 3 分钟，不存入账户文件；扫码二维码包含二维码密钥是扫码协议自身要求。服务端限制轮询频率，管理员管理接口必须经可信 fnOS 管理网关。
- 支持只读列出当前实例登录的网易云账号**自己创建**的歌单、歌单歌曲（最多 1000 首）；不会把“收藏的其他人歌单”混入列表。
- 对飞牛官方 `/music/api/v1/playlist/list`、`/playlist/detail`、`/track/playlist-detail/list` 进行只读注入，原始本地歌单照常透传。虚拟歌单 GUID 绑定已选择的实例，切换账户后不再识别前一实例的 GUID。
- 账号隔离模式和共享账号选择保持 v0.3 行为；媒体文件不自动下载、歌单修改不写入网易端。
- **依旧需要事先单独启动各账户的 MusicBox HTTP 进程，Python 音源依赖未被移除**。扫码挑战通过原 MusicBox 的 `POST /api/v1/auth/login` 和 `GET /api/v1/auth/login/check` 实现；单独端口的实例必须具有独立 cookie 保存目录。
- 如果 MusicBox 返回 `qr_ascii`，管理端展示可扫描的字符二维码；否则只展示官方扫码链接，尚未内置 Go QR 图片生成器。
- 本机测试不等于音乐版权方授权，也不等于实际飞牛手机客户端适配完成；仍无真实 fnOS FPK 实机验收。

详细 API 和边界见 [v0.4 开发说明](docs/V0_4_RELEASE_NOTES.md)。
