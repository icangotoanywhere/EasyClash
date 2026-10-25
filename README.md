# EasyClash

一个极简的安卓 Clash（mihomo 内核）客户端：**只做必要的事，每个按钮旁边都有一句人话说明。**

[⬇️ 下载最新 APK](../../releases/latest)（arm64，安卓 8.0 以上；装不上请先允许"未知来源"）

## 为什么又做一个 Clash 客户端

市面上的客户端功能强大，但对普通用户来说门槛不低：覆写、YAML、策略组……名词一堆，想干一件简单的事得先学半天。EasyClash 把常用操作压缩成五个页面，复杂的东西收进「高级」页，看不懂的地方点 ⓘ 就有解释。

## 三个特别之处

**1. 锁定出口国家，自动用该国最快节点**
很多服务（账号绑定地区的网站、流媒体、支付工具）要求你的出口 IP 稳定在同一个国家。EasyClash 按节点名自动把订阅里的节点分成「🇸🇬 新加坡」「🇯🇵 日本」等地区组，首页点一下国家，就固定在那个国家内自动挑最快的节点——节点坏了换同国的另一个，不会跳到别的国家去。还能单独对当前地区手动测速、立即切换。

**2. 解决 Google Play 下载一直"等待中"**
国内用户常见的怪事：Google Play 页面能打开，一下载 App 就卡在"等待中"，关掉代理反而能下。原因是 Play 下载优先走 QUIC（UDP 443），很多线路对 UDP 支持不好。EasyClash 默认拦截 QUIC 让它回退到普通 TCP，顺手把 Play 的下载域名直连——装上就好，不用改规则。

**3. 必应始终用国内版**
必应在国内有正常服务，直连稳定好用。开启这个选项后 bing 相关域名一律直连，切换代理国家也不会变成当地版本的必应。默认开启，想用国际版关掉即可。

## 功能一览

| 页面 | 功能 |
|---|---|
| 首页 | 一键连接/断开；规则 / 全局 / 直连；按国家锁定出口；单地区或全部地区测速；实时速度 |
| 订阅 | 添加机场链接（支持 `clash://` 一键导入、剪贴板、本地 .yaml）；更新、重命名、删除；流量与到期显示 |
| 节点 | 树状显示策略组（主组 → 子组 → 节点），延迟测速，手动切换 |
| 应用 | 全部 / 仅选中 / 排除选中 三种分应用代理模式 |
| 高级 | 拦截 QUIC、IPv6、DNS、TUN 栈、本地代理端口、必应国内版；附加规则；YAML 覆写；内核日志与连接查看 |

另外：系统下拉菜单磁贴一键开关；长按桌面图标直接「连接」「断开」。

## 技术说明

- 内核：[mihomo](https://github.com/MetaCubeX/mihomo)，通过 gomobile 编成 `core.aar` 在 App 进程内运行，TUN 由 VpnService 建立后把文件描述符交给内核。
- 界面：Kotlin + Jetpack Compose，所有查询和控制走内核自带的 RESTful 接口。Go 侧只有一个文件（`core/core.go`，启动/停止/重载/保护套接字），升级内核几乎不用改代码。
- 只打包 arm64。
- 本软件不含任何节点，所有线路来自你自己的订阅。

## 自己编译

### Windows 一键脚本

1. 克隆或下载本仓库到一个不含中文和空格的路径（如 `D:\EasyClash`）。
2. 右键 `setup-windows.ps1` → **使用 PowerShell 运行**。首次约 20～40 分钟（下载 JDK、Android SDK/NDK、Go），之后 `.\setup-windows.ps1 -BuildOnly` 只编译。
3. 产物：根目录 `EasyClash.apk`。所有工具装在 `%USERPROFILE%\easyclash-tools`，删掉即卸载。

### GitHub Actions

Fork 后在 Actions 里手动运行 **Build APK**，产物在 Artifacts。

### 要点

- 内核必须带 `-tags with_gvisor,cmfa` 编译：`cmfa` 是 mihomo 为非 root 安卓客户端准备的开关，没有它 TUN 启动必失败（表现为"已连接但所有流量为 0"）。
- 中文 Windows：脚本需 UTF-8 BOM；javac 需 `-encoding UTF-8`；下载用系统 `curl.exe`。
- gomobile 最新版要求 Go ≥ 1.26。

## 许可

GPL-3.0。内核 mihomo 为 GPL-3.0，本项目随之采用同一许可。

## 致谢

[mihomo](https://github.com/MetaCubeX/mihomo) · [sing-tun](https://github.com/SagerNet/sing-tun) · [meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat)
