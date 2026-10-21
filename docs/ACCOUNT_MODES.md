# 原生音源账号管理

支持 Go 原生网易云与实验性 QQ 音乐。搜索、播放、扫码登录和会话持久化均由扩展直接处理，无需额外服务。QQ 音乐使用手机 QQ 扫码，详细范围见 [QQ 音乐说明](QQ_MUSIC_NATIVE.md)。

## 添加与登录

在管理页点击“添加账号”，选择音乐平台、填写别名，使用对应客户端扫码并确认。后端自动生成账号 ID；只有验证登录成功且凭证与配置保存成功后，账号才出现在列表中。取消、过期或保存失败不会留下半成品账号。

账号配置保存于 `${TRIM_PKGVAR}/accounts.json`，内部地址为 `native://netease/<账号 ID>` 或 `native://qqmusic/<账号 ID>`。为兼容已有原生配置，地址字段仍名为 `bridge_url`，但不再接受 HTTP 服务地址。

登录会话按平台位于同目录的 `netease-sessions/<账号 ID>.json` 或 `qqmusic-sessions/<账号 ID>.json`，文件权限为 0600，目录权限为 0700。每个账号独立保存凭证。删除账号会清理对应凭证及用户绑定。

## 用户隔离与共享

默认开启隔离：在“访问设置”选择飞牛用户并分配网易云账号，音乐身份确认后生效。单个账号不能同时私有分配给多个用户。

关闭隔离后，管理员选择一个共享账号，所有通过身份验证的飞牛音乐用户使用该账号。切回隔离模式保留原有分配。共享的是该网易云账号的访问权限，使用前应取得账号持有者许可。

## 管理接口

接口前缀为 `/app/fnmusic-go/api`，写入需管理员身份及 CSRF 校验。

- `POST /account-enrollments`：提交 `{ "name": "家庭" }`，创建扫码草稿；可选的 `provider` 接受 `native`/`netease`（网易云，默认）或 `qqmusic`。
- `POST /account-enrollments/<id>/check`：检查授权，成功后持久化账号。
- `DELETE /account-enrollments/<id>`：取消草稿。
- `GET /accounts`：查询账号与访问设置。
- `DELETE /accounts/<id>`：移除账号及凭证。
- `PUT /accounts/mode`：设置隔离模式。
- `PUT /accounts/shared`：选择共享账号。
- `PUT /accounts/bindings/<guid>`：管理已确认用户的账号绑定。

## 旧桥接配置

MusicBox HTTP 适配器和 `--netease-url`、`--netease-user-guid` 启动参数已移除，旧环境变量不再导入账号。原生账号配置和凭证继续使用。

如已有配置包含 HTTP 桥接账号，启动时会明确报错且不修改配置文件。升级前请在旧版管理页移除这些账号，再在新版管理页重新扫码添加；不要将桥接 Cookie 当作原生登录态直接迁移。
