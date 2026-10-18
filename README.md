# fnMusic Go v0.1.0

飞牛音乐（`trim.music`）原生扩展的 Go + Vue 3 重构起点。上游功能与实现清单请读 [docs/FEATURE_MATRIX.md](docs/FEATURE_MATRIX.md)，架构与安全见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)，分阶段任务见 [docs/MIGRATION_PLAN.md](docs/MIGRATION_PLAN.md)。

**当前可用：官方 Unix Socket 透明代理、所有权身份校验、原子接管、优雅退出恢复、只读管理页、健康检查、x86_64/ARM64 编译和 FPK staging。**

**当前不支持：网易云搜索/登录/播放、musicdl、洛雪、歌词/封面注入、在线收藏/歌单、音乐下载。** 这不是已经完成的飞牛云音乐播放器。

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
- 由于构建环境不能访问 npm，本仓库随附了可离线使用的静态后备管理页，**当前二进制内嵌的是后备管理页，不是已经编译完成的 Vue 运行时**。
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
