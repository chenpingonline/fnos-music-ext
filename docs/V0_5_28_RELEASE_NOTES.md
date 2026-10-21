# v0.5.28 · 安全版本重新打包

使用 Go 1.27.3 和 Node.js 22.23.4 重新构建 x86_64、ARM64 安装包，沿用 v0.5.27 的功能与安全修复。管理界面、程序版本与 FPK 清单统一为 0.5.28。

## 官方修复依据

- **CVE-2026-111497**：Node.js 官方发布 22.23.4、24.22.1、26.12.1 安全版本，包含 Brotli 解码器越界写入修复。本次前端使用 22.23.4 构建；FPK 不包含 Node.js 运行时。[Node.js 官方公告](https://nodejs.org/en/blog/vulnerability/october-2026-security-releases)
- **CVE-2026-111943**：Go 官网确认 1.27.3、1.26.10 包含 crypto/x509 安全修复。官方补丁依据 SHA-256 SPKI 哈希过滤包含不可信公钥的证书链；本次使用 Go 1.27.3 重新编译，使修复进入程序内嵌标准库。[Go 版本说明](https://go.dev/doc/devel/release#go1.27.0) · [官方补丁](https://github.com/golang/go/commit/90617fb)

## 升级说明

安装对应架构的 0.5.28 FPK，并确认应用完成重启。现有账号、登录凭证和用户配置继续使用。仅上传 FPK 或更新系统 Go 不会修复仍在运行的旧程序。

实验性 QQ 音乐功能与 v0.5.27 一致，播放权限和音质取决于账号权益。[功能与限制](QQ_MUSIC_NATIVE.md)

真实 NAS 安装及上游账号兼容性仍需在设备上验收。

## 安装包

- `fnmusic-go-0.5.28-x86.fpk`：x86_64。
- `fnmusic-go-0.5.28-arm.fpk`：ARM64。
- `FPK-SHA256SUMS` 和各包 `.sha256` 文件：下载完整性校验。
