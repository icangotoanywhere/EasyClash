# EasyClash Windows 版一键编译脚本：在本目录生成 EasyClash.exe（单文件，双击即可运行）
#
# 用法：右键本文件 → 使用 PowerShell 运行；或在 PowerShell 里执行 .\build-windows.ps1 -Version 1.0.0
# 需要 Go 1.26 以上。运行过上一级目录的 setup-windows.ps1（安卓版编译脚本）的电脑上已经有 Go，会自动使用。
param([string]$Version = "1.0.0")
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

function Step($t) { Write-Host ""; Write-Host "==== $t ====" -ForegroundColor Cyan }
function Run($exe, [string[]]$a, $what) {
    Write-Host ">> $exe $($a -join ' ')"
    & $exe @a
    if ($LASTEXITCODE -ne 0) { throw "$what 失败（退出码 $LASTEXITCODE），请把上面的输出发给 Claude" }
}

# ---------- 1. Go ----------
Step "1/4 检查 Go"
$Tools = Join-Path $env:USERPROFILE "easyclash-tools"
if (Test-Path "$Tools\go\bin\go.exe") {
    $env:GOROOT = "$Tools\go"
    $env:GOPATH = "$Tools\gopath"
    $env:Path = "$Tools\go\bin;$Tools\gopath\bin;" + $env:Path
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "没有找到 Go：请先运行上一级目录的 setup-windows.ps1（会自动安装 Go），或自行安装 Go 1.26 以上"
}
if (-not $env:GOPROXY) { $env:GOPROXY = "https://goproxy.cn,direct" }
if (-not $env:GOSUMDB) { $env:GOSUMDB = "sum.golang.google.cn" }
$env:GOFLAGS = "-mod=mod"
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$gopathBin = Join-Path (go env GOPATH) "bin"
$env:Path = "$gopathBin;" + $env:Path
Run "go" @("version") "检查 Go"

# ---------- 2. 地理数据库（压缩后打包进 exe） ----------
Step "2/4 准备地理数据库"
$assets = Join-Path $Root "assets"
New-Item -ItemType Directory -Force -Path $assets | Out-Null
$geoBase = "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release"
foreach ($f in @("geoip.metadb", "geosite.dat", "GeoLite2-ASN.mmdb")) {
    $gz = Join-Path $assets "$f.gz"
    if (Test-Path $gz) { Write-Host "已存在：$gz"; continue }
    $src = Join-Path $Root "..\app\src\main\assets\$f"     # 安卓版已经下载过的直接复用
    if (-not (Test-Path $src)) {
        $src = Join-Path $env:TEMP $f
        Write-Host "下载 $f …"
        & curl.exe -fL --retry 3 -o $src "$geoBase/$f"
        if ($LASTEXITCODE -ne 0) { throw "下载 $f 失败，请检查网络后重试" }
    }
    $in = [IO.File]::OpenRead((Resolve-Path $src))
    $out = [IO.File]::Create($gz)
    $z = New-Object IO.Compression.GZipStream($out, [IO.Compression.CompressionLevel]::Optimal)
    $in.CopyTo($z); $z.Dispose(); $out.Dispose(); $in.Dispose()
    Write-Host ("已打包 {0}（{1:N1} MB）" -f $f, ((Get-Item $gz).Length / 1MB))
}

# ---------- 3. 图标、版本信息、清单（DPI 感知） ----------
Step "3/4 生成程序图标与版本信息"
if (-not (Get-Command go-winres -ErrorAction SilentlyContinue)) {
    Run "go" @("install", "github.com/tc-hib/go-winres@v0.3.3") "安装 go-winres"
}
$fileVer = "$Version.0"
Run "go-winres" @("make", "--in", "winres\winres.json", "--arch", "amd64", "--product-version", $fileVer, "--file-version", $fileVer) "生成资源"

# ---------- 4. 编译 ----------
Step "4/4 编译 EasyClash.exe"
Run "go" @("mod", "tidy") "整理 Go 依赖"
# -H windowsgui：不弹黑色命令行窗口；with_gvisor：TUN 模式可选 gvisor 网络栈
$mihomoVer = (go list -m -f "{{.Version}}" github.com/metacubex/mihomo).Trim()
$ld = "-H windowsgui -s -w -X main.appVersion=$Version -X github.com/metacubex/mihomo/constant.Version=$mihomoVer"
Run "go" @("build", "-trimpath", "-tags", "with_gvisor", "-ldflags", $ld, "-o", "EasyClash.exe", ".") "编译"
$size = (Get-Item "EasyClash.exe").Length / 1MB
Write-Host ""
Write-Host ("完成！{0}\EasyClash.exe（{1:N1} MB，版本 {2}）" -f $Root, $size, $Version) -ForegroundColor Green
Write-Host "双击即可运行，不需要安装。设置和订阅保存在 %APPDATA%\EasyClash。"
