# EasyClash 开发约定（给维护者和 AI 助手）

极简 Clash 客户端，mihomo 内核，安卓 + Windows + macOS。面向普通用户：每个按钮旁边都有一句人话说明。
用户文档见 `README.md`、`desktop/README.md`；本文件只写**改代码、发版本时必须遵守的约定**。

## 架构

| 部分 | 目录 | 技术 |
|---|---|---|
| 安卓 App | `app/src/main/kotlin/com/easyclash/` | Kotlin + Jetpack Compose。`core/` 配置生成与地区逻辑，`data/` 设置与订阅，`ui/` 界面，`vpn/` VpnService |
| 安卓内核 | `core/`（`core.go`、`fallback.go`） | gomobile 把 mihomo 编成 `app/libs/core.aar`，App 进程内运行；编译必须带 `-tags with_gvisor,cmfa` |
| 电脑版 | `desktop/` | 纯 Go，一份源码同时编 Windows 和 macOS；平台代码在 `*_windows.go` / `*_darwin.go`；界面是内嵌网页 `desktop/ui/`（`app.js`、`style.css`） |
| CI / 发布 | `.github/workflows/build.yml` | 三个平台云端编译，macOS 版在云端 Mac 上跑 `desktop/macos/ci-test.sh` 冒烟测试 |

App 与内核之间：启动时生成完整的 mihomo YAML，之后切节点、测速、看日志都走内核的 REST 接口（安卓 `CoreApi.kt`，电脑版 `coreapi.go`）。

## 最重要的约定：两边同步改

业务逻辑在 **Kotlin（安卓）和 Go（电脑版）里各实现了一遍**，改一个功能必须两边一起改：

| 功能 | 安卓 | 电脑版 |
|---|---|---|
| 配置生成（地区组、规则、DNS、TUN） | `core/ConfigBuilder.kt` | `config.go` |
| 地区表与节点归属 | `core/Regions.kt` | `regions.go` |
| 锁定出口地区 | `core/RegionLock.kt`、`vpn/EasyClashVpnService.kt` | `app.go` |
| 设置项（含默认值、JSON 字段名） | `data/Models.kt` | `store.go`（`server.go` 的设置白名单也要同步） |
| 界面文字 | `ui/screens/*.kt` | `desktop/ui/app.js` |
| 出境直连失败转代理 + 国际 AI 服务禁止直连 | `core/fallback.go` | `desktop/fallback.go`（**逐字相同，只有包名不同**） |
| 国际 AI 服务强制走代理的域名表 | `core/ConfigBuilder.kt` 的 `AI_DOMAINS` | `config.go` 的 `aiDomains`（界面说明用的副本在 `ui/app.js` 的 `AI_DOMAINS`） |

删功能时，在全仓库 grep 相关字段名、函数名和中文说明文字，确认两边、界面、README 都清干净。v1.0.25 删除「优先 / 备用」分级时，就因为漏改了 `server.go` 里的一处调用，导致编译失败。

## 发布

- **只在云端发布。** 改 `app/build.gradle.kts` 里的 `versionCode`（每次 +1）和 `versionName`（三段数字，如 `1.0.26`），可选写 `release-notes/v<版本>.md`，推到 `main`。CI 会编译三个平台、测试 macOS 版、给 APK 签名并核对指纹，全部通过后自动打 tag、发布 Release，并更新 README 里「最新版」直链指向的文件。
- **版本号没变的推送只编译、不发布**，不会覆盖已发布的版本。所以只改文档可以放心推 main。
- `main` 以外的分支只编译，产物在 Actions 的 Artifacts 里。
- 判断「发布了没有」以 GitHub 上实际存在的 tag 和 Release 为准（`git ls-remote origin refs/tags/v<版本>`）。经过代理调用 Actions 接口时，进度可能是缓存的旧数据。
- CI 日志另存在 `ci-logs-windows`、`ci-logs-macos` 分支，不登录也能 `git fetch` 下来看。

## 安卓签名（不能换）

- 所有版本都用同一把发布密钥签名，证书 SHA-256：`ada95c194e88ce73bf7bea727b02a8231496fa96ec87935e50c3860321b5a861`。**换了密钥，用户就无法覆盖升级**（必须卸载重装，丢失设置）。
- 云端：仓库 Secrets 里有 `ANDROID_KEYSTORE_B64`、`ANDROID_KEYSTORE_PASSWORD`、`ANDROID_KEY_ALIAS`、`ANDROID_KEY_PASSWORD`。CI 签完会用 apksigner 核对指纹（`build.yml` 里的 `RELEASE_CERT_SHA256`），不一致就不发布。
- 本机：`app/build.gradle.kts` 依次查找 `EASYCLASH_KEYSTORE_PROPS` 环境变量、仓库根目录的 `keystore.properties`、`D:/EasyClash/signing/keystore.properties`；都没有时退回 debug 签名，这种包只能自测。
- 密钥文件（`*.jks`、`keystore.properties`）绝不能进仓库，已写入 `.gitignore`。
- 历史：v1.0.24 及更早的版本是 CI 用临时 debug 密钥签的，每版签名都不同；从 v1.0.25 起固定。

## 关键设计决定（改之前先想清楚）

- **地区自动组**：`url-test`，测速地址 `https://www.gstatic.com/generate_204`，`tolerance: 50`，`lazy: true`，间隔由用户设置（安卓默认 5 分钟、电脑版默认 2 分钟）。规则是：当前节点测速失败，或者比最快的节点慢超过 50 ms，才切换。2026-11 评估过「连续两轮失败才切」「多次采样取中位数」，决定不做。
- **`unified-delay: true`**：只计连接建好后的第二次请求，各协议的握手开销不计入，让 SS / Trojan / VLESS / Hysteria2 等不同协议之间的比较公平。不要关。
- **v1.0.25 去掉了「备用节点关键字 / 优先·备用分级」**（原本用来把「中转」节点降级），判断为没有必要。旧版本保存的 `backupKeyword`、`backupPenaltyMs` 设置会被忽略。
- **出境直连失败自动转代理**：只做「直连 → 代理」这一个方向，代理失败绝不改走直连（避免暴露真实 IP）。
- **国际 AI 服务强制走代理（v1.0.29，默认开）**：`AI_DOMAINS` 里的域名有 `DOMAIN-SUFFIX` 规则指向主组，并写进配置 `easyclash-proxy-only`（`via` 主组、`domains`），由 `fallback.go` 在 DIRECT 出口处拦截：万一要直连就改走主组，主组当前也是直连 / 拒绝就拒绝。只比域名，不查连接属于哪个 App。规则顺序：屏蔽 QUIC → 用户附加规则 → 国际 AI 服务 → 微软走国内 → …… → 订阅规则（电脑版分程序规则在最前）。
- **教训：不要按连接查所属 App。** v1.0.28 做过「每个 App 三选一（仅直连 / 按规则 / 仅代理）+ 一键推荐」，「仅代理」靠 `find-process-mode: always` 和规则最前面的 `PROCESS-NAME`，每条连接都要跨进程问一次安卓系统，用户反馈手机变卡；维护者决定 v1.0.29 整体退回 1.0.27 的分应用方式（全部 / 仅选中 / 排除选中），只保留按域名的「国际 AI 服务强制走代理」。1.0.28 保存的设置在 `Models.kt` / `store.go` 里换算回来（仅直连 → 排除名单）。以后若再做按 App 的强制代理，只能在准备直连时按需查，并先在真机上确认不卡。
- **后台常驻（v1.0.30，仅安卓）**：前台服务 + 常驻通知 + `START_STICKY`（被杀后系统重新拉起，`onStartCommand` 收到空 intent 会自动重连）；支持系统「始终开启的 VPN」（action 为 `VpnService.SERVICE_INTERFACE`）。`vpn/KeepAlive.kt` 负责检测电池优化、跳转系统设置、按品牌提示自启动菜单；高级页「后台常驻」卡片 + 首次连接成功后提醒一次（`keepAliveAsked`）。**`onRevoke` 不自动重连**：那是用户手动断开或被另一个 VPN 顶替，此时已无 VPN 权限，抢回只会互相打架。不建议用户勾「屏蔽未使用 VPN 的连接」（排除的 App 会断网）。
- **检查更新与原地升级（v1.0.31，三端）**：读 GitHub Releases 接口最近 3 个版本（`?per_page=3`），按**安装包文件名**找本平台的包——`EasyClash-v<版本>.apk`、`EasyClash-Windows-v<版本>.zip`（内含 `EasyClash.exe`）、`EasyClash-macOS-v<版本>.dmg`（根目录是 `EasyClash.app`）。**改 `build.yml` 里的产物命名或打包结构会让旧版本没法自动升级。**
  - 安卓 `data/Updater.kt`：下载 APK 后经 FileProvider（`${applicationId}.files`，`res/xml/file_paths.xml`）交给系统安装器覆盖安装；需要「安装未知应用」授权。本应用流量不进 VPN 隧道，所以连接时经内核本地端口下载，失败再直连。
  - 电脑版 `updater.go`（共用）+ `update_windows.go`（运行中的 exe 改名为 `.old`，新 exe 放回原名）/ `update_darwin.go`（挂载 dmg，`ditto` 复制 `.app` 后换下旧的）；`UI.RestartForUpdate` 断开后用 `--wait-instance` 启动新版本；启动时 `cleanupAfterUpdate` 删旧文件。线路同下载订阅（`subRoutes`）。
  - 自动检查：安卓打开 App 20 秒后、连接成功后（之前没查到时）；电脑版启动 1 分钟后、之后每 12 小时。失败都静默，手动检查才报错。所有失败提示都要带「需要能连上 GitHub，建议先连接 EasyClash」。
- **网络状态悬浮球（v1.0.32，仅 Windows）**：来自维护者的独立小工具 NetGateShow（`D:\NetGateShow`），代码搬进 `desktop/floatball/`（`ball.go` 窗口与绘制、`net.go` 查出口 IP 用 ip-api.com、`win32.go` 系统调用），由 `floatball_windows.go` 按设置 `floatBall`（默认关）启停；macOS 的 `floatball_darwin.go` 是空实现，界面按状态里的 `floatBallSupported` 隐藏开关。展开后的「线路」「节点」两行（`Node.Text` / `Node.Detail`）每 5 秒调 `App.floatNode()`：EasyClash 已连接且（TUN 开着或系统代理指向本软件的端口）才显示节点；否则显示「未使用代理」或「其他代理软件（地址）」。右键「隐藏悬浮球」会把设置关掉。悬浮球自己的位置等存在数据目录的 `floatball.json`。安卓没有这个功能，不用同步。
- 一键推荐之类的功能**不接任何 AI**（维护者决定：不上传用户的 App 列表）。
- **内核版本**：mihomo 固定为 `v1.19.33`（`build.yml` 的 `MIHOMO_VERSION`、`desktop/go.mod`）。电脑版的 `core.go` 用到了它的地理数据库更新接口，升级内核要两边一起测。`core/go.mod` 里写的 v1.19.12 已经过时，编译时会被 CI 覆盖。

## 验证

- 唯一的自动测试是 macOS 冒烟测试（CI 里）。安卓版和 Windows 版只能保证编译通过，功能要装到真机上看。
- 本地快速检查：`node --check desktop/ui/app.js`；`gofmt -l desktop core`。
- 中文 Windows 上，`.ps1` 脚本必须保存为 UTF-8 BOM。

## 维护者与环境

- 仓库：`icangotoanywhere/EasyClash`，默认分支 `main`。界面文字和文档都用中文。
- 维护者本机 Windows 的 `D:\EasyClash` 里有签名密钥原件（`signing\`）和完整的交接文档（`项目交接文档.md`）。
- 维护者的偏好：发布全部走云端；AI 在维护者电脑上只通过文件后台操作，不用鼠标点桌面。
