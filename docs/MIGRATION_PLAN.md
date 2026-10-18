# Go + Vue 重构开发路线

## v0.1：透明代理与原生基础（当前）
- [x] Go 纯标准库 Unix Socket HTTP 网关，GET/POST/Range 透传
- [x] Unix peer creds、inode 验证、renameat2 原子接管、ownership journal
- [x] SIGTERM 显式安全恢复（不覆盖未知 Socket）
- [x] 单独的管理 UDS + Peer Credentials UID 门禁
- [x] 健康检查与只读运行状态 API
- [x] Vue 3 + TypeScript + Vite 管理端源代码
- [x] 无 Vue 依赖时可用的静态管理页（后备产物）
- [x] x86_64/ARM64 Go 交叉编译及 FPK staging
- [x] 本机假后端自动化测试、E2E 测试
- [ ] 构建真实 FPK（环境需要 fnpack）
- [ ] fnOS 实机安装、桌面网关 UID、HTTPS、音乐播放验证
- [ ] SIGKILL 后恢复守护进程验证

## v0.2：网易云在线查询和播放（待开发）
- [ ] 确认平台接入许可；使用授权明确的服务接口
- [ ] Provider 接口：Search / Track / Lyric / Cover / Play
- [ ] 本地优先合并及真实 Track VO、虚拟 GUID 结构校验
- [ ] 分页、去重、来源标记、超时/防抖/负缓存
- [ ] 音频 URL 正确授权、过期刷新、Range / HEAD / 206
- [ ] Web、移动端真实请求与响应契约测试

## v0.3：账号、歌单、多用户、转码（待开发）
- [ ] 扫码账号绑定、Cookie 密钥保护、退出/过期处理
- [ ] 强制由官方鉴权查询识别用户，失败不得使用 shared
- [ ] 网易云只读歌单、公共歌单、收藏和用户状态隔离
- [ ] FFmpeg AAC/HLS 与播放品质配置

## v0.4：扩展能力与发布（待开发）
- [ ] 受版权/平台许可允许的下载与缓存功能
- [ ] 可选 musicdl / LX 适配器（自定义脚本必须隔离风险）
- [ ] 每日/热门/AI 推荐
- [ ] 配置/日志/诊断/版本管理、升级恢复演练
- [ ] 管理权限模型、真实 fnOS 兼容矩阵和安全审查

## 合规门槛

参考上游 MIT 源码不自动授予飞牛音乐系统内部协议使用权、网易云第三方曲库播放权或音乐内容下载权。面向公众分发前必须单独核查 fnOS 和音源平台的协议及授权。
