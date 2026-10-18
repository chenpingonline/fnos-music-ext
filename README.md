# fnMusic Go — v0.2.0-preview

面向 fnOS 官方飞牛音乐 `trim.music` 的 **Go + Vue 3** 原生扩展重构。上游功能基线：[fnmusic-ext](https://github.com/javycoder/fnos_music_ext)（MIT，第三方源码及服务并非官方产品）。完整功能清单及原理请见 [功能矩阵](docs/FEATURE_MATRIX.md)、[架构](docs/ARCHITECTURE.md)、[迁移计划](docs/MIGRATION_PLAN.md)。

> **这是开发预览，不建议直接在生产 NAS 上接管官方音乐。** 项目没有修改官方音乐二进制、nginx 配置或数据库的代码。所有系统层操作需要在隔离测试机上先完成验证；尚未获得飞牛或网易云的任何特殊授权。

## v0.2 新增功能（本地模拟测试）

- 保留 v0.1 的官方 Socket 接管/恢复、原生请求透传、健康检查、独立管理 Socket。
- **可选网易云 MusicBox 桥接**：Go 客户端请求本机运行的上游 `musicbox-service` HTTP 接口，实现歌曲搜索、信息、歌词、封面、音频取流。
- **本地优先搜索合并**：拦截官方 `/music/api/v1/search/track`，追加来源标记的在线曲目；支持 `q/keyword/query`、页号/页大小；非 200 响应和音源失败回退原生数据。
- **32 位虚拟曲目 GUID**：带校验的可逆映射，无需将在线歌曲写入官方曲库数据库。
- **播放**：对已识别的虚拟曲目提供 `GET/HEAD/Range` 流式响应；音源提供的未授权资源直接视为不可用，不绕过权限。
- **元数据/歌词/封面**：对齐官方字段结构；封面采用受限大小的同源代理而不是跨域 302。
- **单用户安全边界**：明确配置受益人 `FNMUSIC_GO_NETEASE_USER_GUID`，只有官方 `/music/api/v1/user/me` 验证匹配的账户才可访问桥接音源；不将 fnOS Cookie 转发给 MusicBox。
- **媒体 SSRF 防护**：网易云桥接 URL 限定 loopback，媒体链接拒绝内部网段、元数据地址、重定向到内网等目标。

## 当前还没有的功能

- 没有纯 Go 网易云私有协议实现；**MusicBox 是外部过渡服务，仍可能需要 Python**。Go 程序自身不依赖 Python、Docker，但使用在线音乐功能必须另行配置这项服务。
- 没有在此版本实现网易云扫码登录、分用户独立 Cookie、私人歌单双向同步、推荐、在线收藏、音乐下载/边听边存、HLS 转码或洛雪/musicdl。
- 搜索分页为基础分页适配，不保证真实音源的精确总数、跨页全面去重；上线前仍需补齐官方分页契约测试。
- 本机 `go test`、`-race`、Socket 模拟端到端测试不等于真实网易云或 fnOS App 可用；需要本地 NAS 验收。

## 本机运行与测试

需要 Go 1.23+（前端需 Node.js 和 npm）；测试不会接触真实 fnOS Socket。

```bash
go test ./...
go test -race ./...
go vet ./...
./scripts/e2e-local.sh
./scripts/build-binaries.sh
./scripts/build-fpk.sh --stage-only
```

Vue 源码位于 `web/`。有 npm 网络环境时，执行 `cd web && npm install && npm run build`，然后重新构建 Go 二进制以嵌入新页面。仓库附带无需 Node 运行时的后备管理页。**当前测试环境 npm 离线缓存缺少 Vite 依赖，没有完成 Vue 生产构建。**

## 网易云桥接开发配置（单用户限定）

1. 由使用者自行安装、启动并审核兼容 MusicBox REST 契约的本地服务（例如旧项目的 `musicbox-service`），仅监听 `127.0.0.1:8770`。上游 `musicbox-service` 和网易云平台需要的访问权限另行确认。**不要公开暴露桥接服务端口。**
2. 登录飞牛音乐后从**官方** `/music/api/v1/user/me` 响应确认当前账户的 `data.guid`，不要手填 `shared` 或凭空猜测。
3. 若通过 FPK 安装，在 `/var/lib/fnmusic-go/provider.env`（root 可读，0600）配置：

```ini
FNMUSIC_GO_NETEASE_URL=http://127.0.0.1:8770
FNMUSIC_GO_NETEASE_USER_GUID=<官方返回的真实用户 GUID>
```

4. 使用应用中心停止再启动扩展（或在测试机运行 `sudo systemctl restart fnmusic-go`）。不配置两项时始终保持 v0.1 原生透传行为。

手动测试亦可运行：

```bash
./fnmusic-go serve \
  --admin-socket /path/to/fnmusic-go.sock \
  --netease-url http://127.0.0.1:8770 \
  --netease-user-guid '真实飞牛用户 GUID'
```

默认的系统 Socket 路径指向实际 `trim.music`；请只在测试 NAS 上运行，且 **不能与上游 fnmusic-ext 同时接管**。

## FPK

- 通过 `scripts/build-fpk.sh` 使用 fnpack 打包，预装 AMD64 / ARM64 二进制。
- `packaging/fpk/cmd/` 管理启动、停止、升级、卸载；升级将保留已存在的本地 bridge 配置文件。
- 当前环境没有 `fnpack`，只生成了 staging，**没有声称已产出可安装 FPK**。
- 需要补测所有 fnOS 版本及 x86_64、ARM64 实机。参见 [安全与测试限制](docs/ARCHITECTURE.md)。

## 授权边界

源码基于 MIT，可遵守许可复用。第三方服务契约、网易云音乐内容使用和飞牛平台集成许可并不因源码开源而自动获得。请勿使用本扩展突破会员试听限制或进行未获授权的音频保存、传播。源项目归属说明见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
