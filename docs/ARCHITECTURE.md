# fnMusic Go v0.1 设计与运行安全

```
              官方 fnOS Web / App
                       │
                fnOS 网关 / nginx
                       │
       /var/run/trim_music.socket
                       │
                 Go Gateway
                     │
   /var/run/trim_music_upstream.socket
                     │
                  trim-music

管理页（管理员） -- fnOS gatewaySocket --> <FPK target>/fnmusic-go.sock
                                                 │
                                      Go 只读 Admin Handler
```

## 1. 为什么两条 Socket

- 官方 Socket 只提供**原生业务透传**与本扩展本地 health/livez，不提供音源配置或宿主机文件读取。
- 管理 Socket 单独监听在 FPK `target/` 目录，使用 `SO_PEERCRED` 核对同伴身份：允许 root，或运行 root 所有且不可被组/其他用户写入的 nginx 程序、父进程为同一程序的 root 主进程的工作进程。API 另检查官方网关 `X-Trim-Isadmin`；写操作还需要 JSON 与自定义管理 Header，并校验 Origin。
- 默认自动识别网关进程，不固定 UID。特殊部署可以在确认实际网关 UID 后配置 `--gateway-uid`。非 nginx 网关默认拒绝，需实机确认。

## 2. Socket 接管状态机

1. 获取独占 flock，检查状态目录非宽松权限。
2. 拒绝非空 upstream、已有 journal、不属于预期官方程序的 socket（校验 `SO_PEERCRED` + `/proc/<pid>/exe` + inode）。
3. 私有 staging socket 成功监听、自测 livez，写 journal 到受保护文件并 fsync。
4. `renameat2(RENAME_NOREPLACE)`：official → upstream；staging → official。
5. 代理正常接收业务流量；SIGTERM 时优雅退出，验证官方进程与两个 inode，再恢复 upstream → official。
6. 如果代理被 SIGKILL，journaling 和目标 inode 用于显式 `fnmusic-go restore` 恢复。**当前版本不承诺崩溃自动恢复**；后续将引入经验证的 systemd 守护恢复器。

任何未知 socket 归属、同名路径被其他进程重新占用、官方进程身份或 inode 发生意外变化，都选择停止接管/拒绝破坏，而不是强行 unlink。**该设计不能在所有宕机状态保证业务自动恢复**。

## 3. API 保真原则

- v0.1 默认把全部业务 API 原样转发官方 Unix Socket。
- `http.Transport` 的连接在 Unix Socket 上建立；ReverseProxy 处理 hop-by-hop HTTP 头，保留业务方法、body、query、Range/Content-Range、响应状态。
- `/ _ext/livez` 与 `/ _ext/healthz` 是唯一本地扩展路由（实际路径不包含空格）。
- 无第三方音源访问，无缓存音频写入、无官方数据库写入。

## 4. 已验证与未验证

验证环境：通用 Linux/amd64 CI 类环境，Go 1.23，本机假官方 Unix HTTP 后端。`go test ./...` + `scripts/e2e-local.sh` 完成相应测试。

未验证：实际 fnOS 网关、真实 `trim-music` 可执行文件识别、HTTPS、不同设备的官方音乐协议、fnpack 构建、应用中心安装/升级/卸载、手机端播放、ARM64 真机；这些不能声称生产就绪。

## 5. 风险优先级

1. 在隔离 NAS 或快照环境做 v0.1 真机兼容测试，重点检查 Socket 恢复。
2. 重构音源授权适配器，以用户身份为强制参数，禁止共享 cookie。
3. 实现搜曲 schema 兼容数据映射、搜索缓存，再考虑 Range 音频在线播放与转码。
4. 未获得授权时不要开放未授权获取/保存受保护音乐的功能。
