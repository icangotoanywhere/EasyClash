# EasyClash 桌面版（Windows / macOS）

> **声明：本应用仅支持科研用途，任何人不得借助本应用提供的便利浏览非法内容、发表非法言论，否则后果自负！**

安卓版 EasyClash 的电脑版：同样的五个页面、同样的说明文字和功能。Windows 和 macOS 共用这一份源码。

| 平台 | 下载 | 怎么用 |
|---|---|---|
| Windows 10 / 11 | [EasyClash-Windows.zip](https://github.com/icangotoanywhere/EasyClash/releases/latest/download/EasyClash-Windows.zip) | 解压后双击 `EasyClash.exe`，免安装 |
| macOS 12 以上（Intel 和 M 系列芯片通用） | [EasyClash-macOS.dmg](https://github.com/icangotoanywhere/EasyClash/releases/latest/download/EasyClash-macOS.dmg) | 打开 dmg，把 EasyClash 拖进「应用程序」；第一次打开见下文 |

## 和安卓版一样的地方

- 首页：一键连接/断开；规则 / 全局 / 直连；按国家锁定出口（自动用该国最快节点，可分「优先 / 备用」两级）；单地区或全部地区测速；实时速度
- 订阅：添加链接（支持 `clash://` 一键导入）、从剪贴板、导入 .yaml；更新、重命名、删除；流量与到期
- 节点：树状显示策略组，按地区的组单独成块显示；测速、手动切换
- 高级：分流规则与地理数据库状态、拦截 QUIC、IPv6、DNS、本机代理端口、必应国内版、自动测速间隔、附加规则、YAML 覆写、日志与连接
- 规则集固定经代理下载、订阅没带规则时自动补基础分流、地理数据库每 7 天自动更新、港澳台显示为中国香港/中国澳门/中国台湾

## 电脑版专有

| 安卓版 | Windows 版 | macOS 版 |
|---|---|---|
| VPN（系统接管全部流量） | **系统代理**（默认，不需要管理员）或 **TUN 虚拟网卡**（接管所有程序，需要管理员，界面里一键「以管理员身份重新启动」） | **系统代理**（每个网络服务：Wi-Fi、以太网……都会设置；TUN 模式暂不支持） |
| 分应用代理 | **分程序代理**：按程序名（如 chrome.exe）排除或只让选中的程序走代理 | 同左（程序名如 Google Chrome） |
| 下拉菜单磁贴、桌面长按快捷方式 | **托盘图标**：单击打开窗口，右键连接 / 断开 / 切换模式 / 退出；连接时图标变蓝 | **菜单栏图标**（屏幕右上角的双环）：点开菜单连接 / 断开 / 切换模式 / 打开控制面板 / 退出；未连接时图标变淡 |
| — | 主界面是独立窗口（系统自带的 WebView2） | 控制面板在默认浏览器里打开（只监听本机，带随机令牌）；关掉标签页不影响代理 |

两边都有：

- 断开、退出时把系统代理恢复成原来的设置（包括原来别的代理软件的地址）；上次异常退出（崩溃、强制结束）后再次打开，会先恢复原样
- 连接期间系统代理被其他软件改掉时首页会提醒，并可一键改回
- 开机（登录）自动启动、启动后自动连接
- 下载订阅时，没连接也会先借用系统里已开着的代理，连不上再直连，并自动重试
- 连接后自动测一下实际在用的节点，全部连不上时首页红字提醒

设置和订阅保存在 Windows 的 `%APPDATA%\EasyClash`、macOS 的 `~/Library/Application Support/EasyClash`；日志在其中的 `logs/easyclash.log`（重启后也能查，总大小不超过 1 MB）。

## macOS 第一次打开

本软件没有付费购买苹果的开发者签名，所以第一次打开时系统会拦一下（只需要处理一次）：

1. 双击 EasyClash，提示「无法验证开发者」/「Apple 无法检查其是否包含恶意软件」时点 **完成**（不要点「移到废纸篓」）。
2. 打开 **系统设置 → 隐私与安全性**，拉到最下面，在「已阻止打开 EasyClash」旁边点 **仍要打开**，输入开机密码确认。
3. 以后就能直接双击打开了。

也可以在「终端」里执行一行命令代替第 2 步：

```bash
xattr -dr com.apple.quarantine /Applications/EasyClash.app
```

打开后软件常驻在菜单栏（没有 Dock 图标），控制面板会自动在浏览器里打开；之后随时点菜单栏图标 →「打开控制面板…」，或者再双击一次 EasyClash。

修改系统代理需要当前 Mac 账户是管理员（个人电脑默认就是）；不是管理员时界面会提示手动设置的方法。

## 技术说明

- Go 程序：mihomo 内核以库的形式在进程内运行（和安卓版的 `core/core.go` 同一思路），界面是同一套内嵌网页。
- 平台相关的代码在 `*_windows.go` / `*_darwin.go`：Windows 用 WebView2 窗口、注册表改系统代理；macOS 用 `networksetup` 改系统代理、LaunchAgent 开机自启、菜单栏图标，`appevents_darwin.m` 处理「再次打开」和 `clash://` 链接。
- 内核版本固定为 v1.19.33（`core.go` 用到了它的地理数据库更新接口）。Windows 版 TUN 需要的 wintun 驱动已内嵌在内核里，不用另带 dll。
- 地理数据库（GeoIP / GeoSite / ASN）压缩后打包进程序，首次连接时释放。
- Windows 自带字体没有国旗 emoji，界面内嵌了 [Twemoji Country Flags](https://github.com/talkjs/country-flag-emoji-polyfill) 字体（字形来自 Twemoji，CC-BY 4.0）。
- 界面服务只监听 127.0.0.1，并要求启动时随机生成的令牌，本机其他程序无法调用。
- 两个平台的安装包都由 GitHub Actions 在云端编译；macOS 版每次还会在云端的 Mac 上自动测试（启动、导入订阅、连接、检查系统代理确实指向本软件并能上网、断开后恢复、强制结束后再启动能恢复、菜单栏模式、再次打开、`clash://` 链接）。

## 编译

**Windows**：右键 `build-windows.ps1` → 使用 PowerShell 运行，产物为本目录的 `EasyClash.exe`。需要 Go 1.26 以上（运行过上一级目录的 `setup-windows.ps1` 的电脑上已经有，会自动使用）。

```powershell
.\build-windows.ps1 -Version 1.0.0
```

**macOS**：需要 Go 1.26 以上和 Xcode 命令行工具（`xcode-select --install`）。产物为 `dist/EasyClash.app` 和 `dist/EasyClash-macOS-v1.0.0.dmg`（Intel / M 系列通用）。

```bash
./build-macos.sh 1.0.0
```

测试用参数：`--headless`（不开窗口和托盘）、`--no-sysproxy`（不改系统代理）、`--data <目录>`（换数据目录）、`--ui-port <端口>`、`--check-config`（按当前设置生成配置并让内核检查，结果写到数据目录的 check-config.txt）。

## 卸载

先在「高级 → 启动与托盘 / 启动与菜单栏」里关掉开机自动启动，从托盘（菜单栏）退出，再删除程序和上面说的数据文件夹即可。

## 许可

GPL-3.0（随 mihomo）。
