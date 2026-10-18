# v0.2.0-preview：网易云适配器迁移笔记

## 已实现

- `internal/netease/bridge.go`：以原项目 MusicBox HTTP 服务为过渡，封装搜索、歌曲详情、歌词、音频链接、有限大小封面获取。
- `internal/netease/media.go`：媒体目标 public IP 约束、非公网地址阻止、重定向防护。
- `internal/fnmusic/track.go`：校验型 32 hex 虚拟 ID，生成兼容官方前端预期的歌曲 VO / 元数据 / 歌词响应。
- `internal/gateway/online.go`：可选在线路由、fnOS `/user/me` 后端鉴权、绑定账号校验、本地优先合并、音频 HEAD/GET/Range。
- `cmd/fnmusic-go/main.go`：新增 `--netease-url` 和 `--netease-user-guid`，对应环境变量；默认不启用。
- `packaging/fpk/cmd`：首次安装及旧版升级时引入受限的 `/var/lib/fnmusic-go/provider.env` 环境配置。
- Vue 源码与后备静态管理页面同步提示桥接/绑定状态。

## 实现限制和已知风险

- MusicBox HTTP bridge 仍依赖上游 Python。实现纯 Go Provider 前不能声称去除了全部 Python 依赖。
- 歌曲搜索总量：MusicBox 的搜索返回数组可能不携带真实 `total`，本实现用当前页是否满页进行保守分页估计；深分页和跨页去重需进一步完善。
- 部分网易云曲目的元数据返回格式可能不一致，真机上还要验证更多样例。
- 流媒体使用上游合法返回的链接；若上游仅给出试播放资源，不做绕过。
- 现阶段仅允许一名明确配置的 fnOS 用户调用 bridge，防止实例级网易登录态外泄给其他用户；多用户需要重新实现每用户凭证和服务会话隔离。
- 尚未实测真实 fnOS Web/手机端、官方音乐更新、ffmpeg 兼容及 ARM64 环境。
- 不实现收藏、下载、持久化缓存。**读取任何列表都不会触发下载。**

## 测试覆盖

- MusicBox mock search、歌曲详情、歌词、链接解析格式，以及非法/内网 URL 拒绝。
- 官方 UDS mock 的原生 + 在线结果合并、身份隔离、metadata/lyrics、HEAD/GET/Range、音源故障回退。
- `go test -race ./...`、`go vet ./...`、`scripts/e2e-local.sh`、双架构 Go 编译与 FPK staging。

## 下一步

- 本机 mock source 扩展至真实流媒体、DNS 变更与链接过期测试。
- 通过正式授权方式实现每用户账号认证的原生 Go Provider。
- 深分页、搜索缓存、跨页去重和多客户端真实请求回放。
- 使用 fnpack 生成并在隔离 fnOS 机器上测试 FPK。
