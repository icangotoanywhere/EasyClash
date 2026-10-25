# =====================================================================
#  EasyClash · Windows 一键安装编译环境并生成 APK
#
#  用法：在本文件所在目录右键 → 「使用 PowerShell 运行」
#        或在 PowerShell 里执行：  .\setup-windows.ps1
#
#  做的事情（全部装在 %USERPROFILE%\easyclash-tools，不污染系统）：
#    1. 下载 JDK 17（微软 CDN）
#    2. 下载 Android 命令行工具 + 平台 + 构建工具 + NDK（谷歌官方源，国内可直连）
#    3. 下载 Go（golang.google.cn 国内镜像）+ gomobile
#    4. 用 gomobile 把 mihomo 内核编译成 core.aar
#    5. 下载 geoip / geosite 数据库放进 assets
#    6. 用 Gradle 编译 APK（依赖走阿里云镜像）
#
#  首次运行约 20～40 分钟（主要是下载 NDK 约 600 MB），之后再运行只重编译。
#  再次编译只需：  .\setup-windows.ps1 -BuildOnly
# =====================================================================
param(
    [switch]$BuildOnly,      # 跳过环境安装，只编译
    [switch]$SkipCore,       # 跳过 Go 内核编译（core.aar 已存在时可用）
    [string]$MihomoVersion = "latest"   # 例如 v1.19.12；latest = 最新正式版
)

# 注意：不用 "Stop"。很多命令行工具（java、go、gradle）把正常信息写到 stderr，
# 在 Stop 模式下会被当成致命错误直接中断。我们改为自己检查退出码。
$ErrorActionPreference = "Continue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$ProgressPreference = "SilentlyContinue"   # 关掉下载进度条，下载快很多
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$Root  = $PSScriptRoot
$Tools = Join-Path $env:USERPROFILE "easyclash-tools"
$Jdk   = Join-Path $Tools "jdk"
$Sdk   = Join-Path $Tools "android-sdk"
$GoDir = Join-Path $Tools "go"
$GoPath = Join-Path $Tools "gopath"
New-Item -ItemType Directory -Force -Path $Tools | Out-Null

function Step($msg) { Write-Host "`n==== $msg ====" -ForegroundColor Cyan }

function Dl($url, $out) {
    if (Test-Path $out) { Write-Host "已存在，跳过下载：$out"; return }
    Write-Host "下载 $url"
    # 用系统自带 curl（遵循系统代理设置，比 Invoke-WebRequest 稳定）
    & curl.exe -L --retry 3 --retry-delay 3 -sS -o $out $url 2>&1 | ForEach-Object { "$_" }
    if ($LASTEXITCODE -ne 0 -or -not (Test-Path $out)) { Remove-Item $out -ErrorAction SilentlyContinue; throw "下载失败：$url" }
}

# 运行外部命令：stderr 合并显示，按退出码判断成败
function Run([string]$exe, [string[]]$arguments, [string]$what) {
    Write-Host ">> $exe $($arguments -join ' ')" -ForegroundColor DarkGray
    & $exe @arguments 2>&1 | ForEach-Object { "$_" }
    if ($LASTEXITCODE -ne 0) { throw "$what 失败（退出码 $LASTEXITCODE），请把上面的报错发给 Claude" }
}

try {

# ---------- 1. JDK 17 ----------
if (-not $BuildOnly) {
    Step "1/6 JDK 17"
    if (-not (Test-Path "$Jdk\bin\java.exe")) {
        $zip = Join-Path $Tools "jdk17.zip"
        Dl "https://aka.ms/download-jdk/microsoft-jdk-17-windows-x64.zip" $zip
        Expand-Archive -Path $zip -DestinationPath "$Tools\jdk-tmp" -Force -ErrorAction Stop
        $inner = Get-ChildItem "$Tools\jdk-tmp" | Select-Object -First 1
        Move-Item $inner.FullName $Jdk -Force
        Remove-Item "$Tools\jdk-tmp" -Recurse -Force
    }
}
$env:JAVA_HOME = $Jdk
$env:Path = "$Jdk\bin;" + $env:Path
# 中文 Windows 默认 GBK，让 javac 按 UTF-8 读源码（gomobile 生成的 Java 里带注释）
$env:JDK_JAVAC_OPTIONS = "-encoding UTF-8"
Run "java" @("-version") "检查 JDK"

# ---------- 2. Android SDK ----------
if (-not $BuildOnly) {
    Step "2/6 Android SDK + NDK（NDK 约 600 MB，耐心等待）"
    $cmdline = "$Sdk\cmdline-tools\latest\bin\sdkmanager.bat"
    if (-not (Test-Path $cmdline)) {
        $zip = Join-Path $Tools "cmdline-tools.zip"
        Dl "https://dl.google.com/android/repository/commandlinetools-win-11076708_latest.zip" $zip
        Expand-Archive -Path $zip -DestinationPath "$Tools\ct-tmp" -Force -ErrorAction Stop
        New-Item -ItemType Directory -Force -Path "$Sdk\cmdline-tools" | Out-Null
        Move-Item "$Tools\ct-tmp\cmdline-tools" "$Sdk\cmdline-tools\latest" -Force
        Remove-Item "$Tools\ct-tmp" -Recurse -Force
    }
    # 预先写入许可确认文件（等同于手动回答 y），比管道输入可靠
    $lic = "$Sdk\licenses"
    New-Item -ItemType Directory -Force -Path $lic | Out-Null
    Set-Content -Path "$lic\android-sdk-license" -Value "`n8933bad161af4178b1185d1a37fbf41ea5269c55`n24333f8a63b6825ea9c5514f83c2829b004d1fee`nd56f5187479451eabf01fb78af6dfcb131a6481e" -Encoding ASCII
    Set-Content -Path "$lic\android-sdk-preview-license" -Value "`n84831b9409646a918e30573bab4c9c91346d8abd" -Encoding ASCII
    Run $cmdline @("--sdk_root=$Sdk", "platform-tools", "platforms;android-35", "build-tools;35.0.0", "ndk;27.2.12479018") "安装 Android SDK 组件"
    if (-not (Test-Path "$Sdk\ndk\27.2.12479018\ndk-build.cmd")) { throw "NDK 没有安装成功（通常是许可未接受或网络中断），请把上面的输出发给 Claude" }
}
$env:ANDROID_HOME = $Sdk
$env:ANDROID_SDK_ROOT = $Sdk
$env:ANDROID_NDK_HOME = "$Sdk\ndk\27.2.12479018"
Set-Content -Path (Join-Path $Root "local.properties") -Value ("sdk.dir=" + $Sdk.Replace('\', '/')) -Encoding ASCII

# ---------- 3. Go + gomobile ----------
if (-not $BuildOnly) {
    Step "3/6 Go + gomobile"
    if (-not (Test-Path "$GoDir\bin\go.exe")) {
        # gomobile 最新版要求较新的 Go，这里自动取当前最新正式版
        $goVer = "go1.27.3"
        try { $goVer = (curl.exe -sS "https://golang.google.cn/dl/?mode=json" | ConvertFrom-Json)[0].version } catch { }
        Write-Host "使用 $goVer"
        $zip = Join-Path $Tools "$goVer.zip"
        Dl "https://golang.google.cn/dl/$goVer.windows-amd64.zip" $zip
        Expand-Archive -Path $zip -DestinationPath $Tools -Force -ErrorAction Stop   # 解出来就是 $Tools\go
    }
}
$env:GOROOT = $GoDir
$env:GOPATH = $GoPath
$env:GOPROXY = "https://goproxy.cn,direct"
$env:GOSUMDB = "sum.golang.google.cn"
$env:GOFLAGS = "-mod=mod"
$env:Path = "$GoDir\bin;$GoPath\bin;" + $env:Path
Run "go" @("version") "检查 Go"
if (-not $BuildOnly) {
    Run "go" @("install", "golang.org/x/mobile/cmd/gomobile@latest") "安装 gomobile"
    Run "gomobile" @("init") "初始化 gomobile"
}

# ---------- 4. 编译内核 ----------
if (-not $SkipCore) {
    Step "4/6 编译 mihomo 内核 → app\libs\core.aar"
    Push-Location (Join-Path $Root "core")
    try {
        Run "go" @("get", "github.com/metacubex/mihomo@$MihomoVersion") "拉取 mihomo 源码"
        Run "go" @("get", "golang.org/x/mobile/bind@latest") "拉取 gomobile bind"
        Run "go" @("mod", "tidy") "整理 Go 依赖"
        New-Item -ItemType Directory -Force -Path (Join-Path $Root "app\libs") | Out-Null
        # -tags cmfa：mihomo 为非 root 安卓客户端准备的编译开关（不读系统包列表、由 App 传入 TUN fd）；with_gvisor：内置 gVisor 网络栈
        Run "gomobile" @("bind", "-target", "android/arm64", "-androidapi", "26", "-tags", "with_gvisor,cmfa", "-ldflags", "-s -w", "-trimpath", "-o", (Join-Path $Root "app\libs\core.aar"), ".") "编译内核"
    } finally { Pop-Location }
}

# ---------- 5. geo 数据库 ----------
Step "5/6 下载 geoip / geosite 数据库"
$assets = Join-Path $Root "app\src\main\assets"
New-Item -ItemType Directory -Force -Path $assets | Out-Null
$geoBase = "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release"
foreach ($f in @("geoip.metadb", "geosite.dat", "GeoLite2-ASN.mmdb")) {
    try { Dl "$geoBase/$f" (Join-Path $assets $f) } catch { Write-Warning "下载 $f 失败（不影响编译，首次连接时内核会自己下载）" }
}

# ---------- 6. 编译 APK ----------
Step "6/6 编译 APK"
Push-Location $Root
try {
    Run ".\gradlew.bat" @("assembleRelease", "--no-daemon") "Gradle 编译"
} finally { Pop-Location }

$apk = Join-Path $Root "app\build\outputs\apk\release\app-release.apk"
$out = Join-Path $Root "EasyClash.apk"
Copy-Item $apk $out -Force
Write-Host "`n完成！APK 在：$out" -ForegroundColor Green
Write-Host "把它发到手机上安装即可（安装时若提示未知来源，允许一次）。"

} catch {
    Write-Host "`n!!!! 出错了：$($_.Exception.Message)" -ForegroundColor Red
    Write-Host "把这段日志发给 Claude 即可。"
    exit 1
}
