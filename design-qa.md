# fnMusic Go 前端改造 · 视觉与交互复查

## 对照基准

- 设计图：用户选择的蓝白浅色账号管理页，原文件 `/var/folders/sp/90pk0ss17bj66n2wcc3fgx1h0000gn/T/codex-clipboard-4bf304e8-9801-4054-a10d-f689909d573a.png`；本地副本 `dist/ui-preview/reference.png`。
- 实现：`web/src/App.vue`、`web/src/app.css`、`web/src/theme.css`，保留现有账号、登录、歌单、设置及鉴权逻辑。
- 预览：`npm run preview:ui --prefix web` → `http://127.0.0.1:4173/app/fnmusic-go/`。独立开发服务使用内存示例数据，不连接 NAS 或网易云，不打入生产前端。默认浅色，`?theme=dark` 可检查深色。
- 设计图 1487 × 1058；对照时等比例归一到 1440 × 1024（宽高比差异小于 0.1%）。浏览器截图 1440 × 1024，CSS viewport 1440 × 1024，截图密度 1。
- 对照状态：账号页、共享模式、三个示例账号、无弹窗。账号名称、数量及共享账号一致；手机号、登录状态、歌单数量等设计图数据不作为真实接口数据使用。
- 最终实现截图：`dist/ui-preview/accounts-desktop.png`。
- 全屏同图对比：`dist/ui-preview/comparison-final.jpg`（左为设计，右为实现）。
- 账号行局部同图对比：`dist/ui-preview/comparison-rows.png`（上为设计，下为实现）；检查头像、字体、分隔线、操作与访问范围。

## 发现与修正

1. [P1，已修正] 右侧使用指引原先通过负 margin 上移，标题及第一步被滚动区域裁切。改为宽屏固定侧栏，与页头对齐（top 48px），完整显示标题和三步内容。
   - 修正前：`dist/ui-preview/accounts-before.png`、`dist/ui-preview/comparison-before.jpg`。
   - 修正后：`dist/ui-preview/accounts-desktop.png`、`dist/ui-preview/comparison-final.jpg`；DOM 几何检查 top=48，无遮挡。
2. [P2，已修正] 搜索框独占一行使账号表整体向下偏移。桌面搜索框与访问模式行对齐，窄屏恢复独立一行；最终同图比较确认。
3. [P2，已修正] 右侧指引栏过窄，提示文案频繁折行。1440px 下调整为 272px，恢复接近设计图的区域比例。
4. 本地预览歌单数据最初使用 `{list:...}`，与现有数组契约不符。已修正为数组，实际打开歌单和曲目详情验证通过；未修改生产 API。

## 五项视觉检查

- 字体与层级：系统无衬线字体，28px 页标题、14–16px 主要内容、12–13px 辅助说明；账号行与导航层级清楚，无不可读截断。
- 间距与布局：222px 分组侧栏、白色主工作区、列表行分隔、右侧三步指引和蓝色提示条；无嵌套卡片。985px 隐藏指引，390px 导航横向滚动且账号改为双列布局。两处宽度的 document scrollWidth 均未超过 viewport。
- 颜色：浅灰框架、白色工作区、#0067d9 蓝色主色、淡蓝选中态；深色模式沿用独立语义色变量，文字及操作可辨识。
- 图像与图标：网易云标志已按用户最新反馈替换为根据用户提供参考重新绘制的透明背景 PNG，正常加载；MusicBox 使用 Lucide Box 连接类型图标，未仿制设计图中的虚构 M 标志。导航和按钮统一使用 Lucide 图标。完整许可与来源已保留。
- 文案与数据：接口未提供列表登录状态、手机号或歌单计数，因此不伪造；保留“登录与歌单”真实操作入口。共享模式仅选中的账号显示共享，其他账号显示“未启用”，符合当前账号选择逻辑。

## 交互与验证

- 使用 Computer Use / Codex 内置浏览器验证：账号添加表单提交、成功提示、账号筛选、登录状态弹窗、加载歌单、打开曲目、播放音质修改及保存，以及全部八个导航页面切换。
- 响应式：1440 × 1024、985 × 781、390 × 844；手机截图 `dist/ui-preview/accounts-mobile.png`。
- 深色模式：`dist/ui-preview/accounts-dark.png`。
- 浏览器控制台检查：无 warning/error。
- 前端 TypeScript + Vite 生产构建通过；23 项现有前端测试通过；`git diff --check` 通过。
- 本地示例验证不覆盖真实 fnOS 网关、NAS 目录选择器、网易云扫码授权或客户端播放。

## 可接受差异与后续微调

- 保留搜索、刷新和删除操作，区别于设计图中的省略菜单，便于沿用现有可用流程。
- 头像使用真实品牌图标及标准连接图标；实际数据字段决定辅助文案，不复制设计图虚构状态。
- [P3] 若后续为账号列表增加真实登录状态和歌单数量接口，可在现有行内扩展；本轮未增加额外源站请求。


## 输入控件焦点样式修正

- 用户反馈图：`/var/folders/sp/90pk0ss17bj66n2wcc3fgx1h0000gn/T/codex-clipboard-6e3ab6a8-cfe1-4730-9124-c7492ee5d8fb.png`。原样式为控件边框叠加带 3px 间距的 outline，产生分离的双层边框。
- 所有文本、数字、URL、目录输入和下拉框统一使用原边框变色与内侧连续加粗，取消外部 outline；搜索组合框仅由外层呈现边框，内部 input 保持无描边。按钮、开关的键盘轮廓取消额外间距。
- 使用 Computer Use 实际复查：添加账号表单自动聚焦、Tab 切换到账号标识、超长名称错误状态、账号搜索、播放设置数字输入、存储目录输入、推荐页下拉选择，以及深浅主题。
- 运行时样式确认：文本/数字/目录/select 的 outline-style 为 none，焦点边框和 inset shadow 使用相同主题色；错误字段沿用 danger 色；搜索内层 outline/shadow 均为 none，仅外层高亮。控制台无 warning/error。
- 修正后完整弹窗截图：`dist/ui-preview/input-focus-fixed.png`；局部截图：`dist/ui-preview/input-focus-detail.png`。局部截图与用户图均清楚显示标签及聚焦输入框，已确认分离的外层轮廓消失。
- 前端生产构建及 diff 格式检查通过。


## 紧凑密度调整（按用户最新反馈）

用户明确要求降低上一版过大的字号与留白，因此本轮密度以该反馈为准，不再追求初始设计图的宽松比例。

- 检查到预览 CSS zoom=1、visualViewport.scale=1、DPR=2；未发现额外的 CSS 或视口缩放。原样式本身为 28px 标题、48px 导航、约 100px 账号行。
- 改为 13px 正文、22px 标题、190px 侧栏、36px 导航、36px 账号头像、约 70px 账号行，按钮和表单高度约 34–36px；同步缩减页头、提示条和设置分组间距。
- 音质选项在 985px 窗口仍保留三列，800px 以下再切换两列，650px 以下一列，避免中等窗口过早堆叠。
- Computer Use 检查账号页、播放设置与添加账号弹窗；聚焦输入框仍无外层描边。1440px、985px 和 390px 下未检测到横向溢出；控制台无 warning/error。
- 截图：`dist/ui-preview/accounts-compact.png`（985×781）、`dist/ui-preview/accounts-compact-desktop.png`（1440×1024）、`dist/ui-preview/accounts-compact-mobile.png`（390×844）。
- 前端类型检查、生产构建与 diff 检查通过。本次为样式和布局修正，没有新增业务逻辑。

## 网易云图标替换

使用用户提供的 184×176 PNG，保留原图比例，替换旧 SVG 及圆形红底。Computer Use 确认两个账号图标加载完成，36px 尺寸下显示正常；截图 `dist/ui-preview/netease-icon-updated.png`。前端生产构建及 diff 检查通过。

## 图标重绘修正

用户澄清原图包含多截的灰色区域，要求基于红色图标重新绘制。使用 Image Gen 以所提供图片为参考重绘，透明圆角背景、无灰色外框；替换 `web/public/images/netease-cloud-music.png`。确认图片为 1254×1254 RGBA 且左上角透明；Computer Use 确认列表加载新版图片，截图 `dist/ui-preview/netease-icon-redrawn.png`。前端生产构建及 diff 检查通过。

## 账号列表文案精简

按用户标注移除连接类型中的“原生账号／MusicBox 账号”重复行，保留具体音源说明；列标题与操作改为“歌单”，移除音符图标并同步入口指引。Computer Use 确认内容精简，歌单入口仍正常打开原有面板。截图 `dist/ui-preview/accounts-simplified.png`；生产构建和 diff 检查通过。

## 账号别名与扫码合并

只保留“账号别名”，随机生成内部标识；提交后自动打开扫码面板，跳过单独状态检查和生成按钮。Computer Use 在本地示例服务填写“新网易云账号”后，点击一次“创建并扫码”即见二维码及“等待扫码”状态；截图 `dist/ui-preview/account-create-and-scan.png`。新增入口测试及既有测试共 24 项通过。

## 最终 FPK 交付

最终版本 0.5.22。24 项前端测试通过，双架构 FPK 构建完成；包内 manifest、ELF 架构、完整嵌入的前端 JS/CSS/图标/许可、应用目录内来源说明、生命周期脚本执行权限与 SHA-256 均已校验。两个架构二进制分别在禁网 Docker 容器执行 version，均返回 `fnmusic-go v0.5.22`。报告 `dist/fpk-audit-0.5.22.json`。尚未进行 NAS 安装与真实网易云授权验收。

最终版本示例界面：`dist/ui-preview/account-alias-only.png`、`dist/ui-preview/account-create-and-scan.png`。

## 同一弹窗与登录后保存（最新修改，尚未重新打包）

- 别名、音源选择、二维码和状态合并在同一“添加音源账号”弹窗。旧的“先 POST /accounts 再扫码”创建流程已由临时 enrollment 替代。
- 原生临时登录凭证仅在内存中，成功授权后还需要 AuthStatus 验证；验证和持久化完成才返回 saved=true。失败、取消、过期不发布账号，配置保存失败回滚新写入凭证，成功轮询支持幂等响应。
- 临时会话绑定管理身份、最多 32 个、5 分钟清理，二维码 3 分钟过期，轮询限 2 秒；继续由已有管理网关、CSRF 与同源 JSON 写校验保护。
- Computer Use：等待扫码时账号数 3；取消后仍为 3。显式成功模拟场景在等待时仍为 3，成功保存后变为 4，弹窗关闭，新别名出现在列表。示例二维码与授权结果均为本地模拟。
- 窄屏 390×844：弹窗宽 358px，无横向溢出，底部按钮完整可见。桌面及窄屏截图 `dist/ui-preview/account-enrollment-unified.png`、`dist/ui-preview/account-enrollment-mobile.png`。
- 29 项前端测试及 TypeScript/Vite 构建通过；accounts、netease 测试通过；admin、netease 测试二进制在禁网 Linux 容器通过。新增测试覆盖待扫码、取消、过期、仅登录未保存、保存后确认、晚到成功响应、失败重试、跨用户草稿访问、幂等保存、私有凭证及回滚。
- 本轮修改尚未生成新的 FPK，既有 0.5.22 安装包不包含该流程。

## 当前账号歌单与列表精简

- “歌单”入口直接请求所选账号的歌单，移除弹窗中的登录状态、检查状态和手动加载步骤；保留刷新、失败重试及歌曲详情。
- 删除账号名下方的音源账号说明，以及访问范围下方的使用权限补充说明。
- 新增立即加载所选账号及切换账号时忽略旧响应的回归测试，前端测试共 31 项通过；类型检查、生产构建与 diff 检查通过。
- Computer Use 在本地模拟服务验证：家庭账号自动显示两张歌单并能展开歌曲，私人账号只显示自己的歌单；1440px 与 720px 窗口布局正常，控制台无警告或错误。
- 截图：`dist/ui-preview/accounts-clean-fields.png`、`dist/ui-preview/account-playlists-only.png`。真实网易云账号数据未在本轮验证，尚未重新打包。

final result: passed
