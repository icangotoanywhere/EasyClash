# =====================================================================
#  EasyClash · 本机一键发布
#
#  用法（在仓库根目录的 PowerShell 里）：
#      .\release.ps1 -Version 1.0.25                       # 完整发布
#      .\release.ps1 -Version 1.0.25 -NotesFile notes.md   # 指定发布说明
#      .\release.ps1 -Version 1.0.25 -NoUpload             # 只在本机打包，不提交、不发布（试编译用）
#
#  做的事情：
#    1. 检查：在 main 分支、没有未提交的改动、和 GitHub 同步；gh 已登录；能找到发布密钥
#    2. 改版本号（app/build.gradle.kts），提交并推到 main —— 云端随即开始编译、测试 macOS 版
#    3. 本机编译 mihomo 内核（版本固定）→ 安卓 APK，用本机发布密钥签名，并核对签名确实是这把密钥
#    4. 本机编译 Windows 版 EasyClash.exe，打成 zip
#    5. 等云端 macOS 版编译并测试通过，下载 dmg
#    6. 生成 SHA256 校验文件，在 GitHub 上创建 Release 并上传全部文件
#
#  前提：运行过一次 .\setup-windows.ps1（装好 JDK / Android SDK / Go）；
#        装好 GitHub 命令行工具并登录一次：winget install --id GitHub.cli，然后 gh auth login
#  发布密钥：默认在 D:\EasyClash\signing（keystore.properties + easyclash-release.jks），务必备份
# =====================================================================
param(
    [Parameter(Mandatory = $true)][string]$Version,   # 例如 1.0.25
    [string]$NotesFile,                                # 发布说明（Markdown）；不给就用上个版本以来的提交标题
    [switch]$NoUpload,                                 # 只打包，不提交、不推送、不发布
    [switch]$SkipMac,                                  # 不等 macOS 版（云端 Mac 出问题时临时用）
    [switch]$SkipCore,                                 # 不重编内核（app\libs\core.aar 已是正确版本时省时间）
    [string]$MihomoVersion = "v1.19.33"                # 与 desktop/go.mod 保持一致
)

$ErrorActionPreference = "Continue"
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$ProgressPreference = "SilentlyContinue"

$Root  = $PSScriptRoot
$Out   = Join-Path $Root "release-out"
$Tools = Join-Path $env:USERPROFILE "easyclash-tools"
$Repo  = "icangotoanywhere/EasyClash"
$Gradle = Join-Path $Root "app\build.gradle.kts"

function Step($msg) { Write-Host "`n==== $msg ====" -ForegroundColor Cyan }
function Fail($msg) { throw $msg }
function Run([string]$exe, [string[]]$arguments, [string]$what) {
    Write-Host ">> $exe $($arguments -join ' ')" -ForegroundColor DarkGray
    & $exe @arguments 2>&1 | ForEach-Object { "$_" }
    if ($LASTEXITCODE -ne 0) { Fail "$what 失败（退出码 $LASTEXITCODE）" }
}
function Text([string]$exe, [string[]]$arguments) { ((& $exe @arguments 2>$null) -join "`n").Trim() }
function Sha256Hex($s) { ($s -replace '[^0-9A-Fa-f]', '').ToLower() }

Set-Location $Root
try {

if ($Version -notmatch '^\d+\.\d+\.\d+$') { Fail "版本号格式应为 1.0.25 这样的三段数字" }
$Tag = "v$Version"

# ---------------------------------------------------------------- 1. 检查
Step "1/6 发布前检查"

# 发布密钥
$propsFile = @($env:EASYCLASH_KEYSTORE_PROPS, (Join-Path $Root "keystore.properties"), "D:\EasyClash\signing\keystore.properties") |
    Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $propsFile) { Fail "找不到发布密钥配置 keystore.properties（默认在 D:\EasyClash\signing）。没有它打出来的 APK 无法覆盖升级，不能发布。" }
$props = @{}
Get-Content $propsFile | Where-Object { $_ -match '^\s*([^#=]+?)\s*=\s*(.*)$' } | ForEach-Object { $props[$Matches[1]] = $Matches[2] }
$storeFile = $props["storeFile"] -replace '/', '\'
if (-not (Test-Path $storeFile)) { Fail "keystore.properties 指向的密钥文件不存在：$storeFile" }
Write-Host "发布密钥：$storeFile"

# 工具
if (-not (Test-Path "$Tools\jdk\bin\keytool.exe")) { Fail "没找到编译环境（$Tools）：请先运行一次 .\setup-windows.ps1" }
$keytool   = "$Tools\jdk\bin\keytool.exe"
$apksigner = Get-ChildItem "$Tools\android-sdk\build-tools\*\apksigner.bat" -ErrorAction SilentlyContinue | Sort-Object FullName | Select-Object -Last 1
if (-not $apksigner) { Fail "没找到 apksigner（Android build-tools），请先运行一次 .\setup-windows.ps1" }
$env:JAVA_HOME = "$Tools\jdk"
$env:Path = "$Tools\jdk\bin;" + $env:Path

# 这把密钥的证书指纹，用来核对 APK
$kt = (& $keytool -list -v -keystore $storeFile -alias $props["keyAlias"] -storepass $props["storePassword"] 2>$null) -join "`n"
if ($kt -notmatch 'SHA256:\s*([0-9A-F:]+)') { Fail "读不出发布密钥的证书指纹（密码不对或文件损坏）" }
$expectedCert = Sha256Hex $Matches[1]
Write-Host "证书 SHA-256：$expectedCert"

if (-not $NoUpload) {
    if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { Fail "没装 GitHub 命令行工具：执行 winget install --id GitHub.cli，重新打开 PowerShell 后执行 gh auth login" }
    & gh auth status 2>&1 | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "gh 还没登录：执行 gh auth login（选 GitHub.com → HTTPS → 浏览器登录）" }
    & gh auth setup-git 2>&1 | Out-Null   # 让 git push 用 gh 的登录

    $branch = Text "git" @("rev-parse", "--abbrev-ref", "HEAD")
    if ($branch -ne "main") { Fail "请在 main 分支上发布（当前：$branch）" }
    $dirty = Text "git" @("status", "--porcelain", "--untracked-files=no")
    if ($dirty) { Fail "有未提交的改动，先提交或撤销：`n$dirty" }
    Run "git" @("fetch", "--tags", "origin") "从 GitHub 同步"
    $behind = Text "git" @("rev-list", "--count", "HEAD..origin/main")
    if ($behind -ne "0") { Fail "本地 main 落后 GitHub $behind 个提交，先 git pull" }
    & gh release view $Tag --repo $Repo 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { Fail "GitHub 上已经有 $Tag 这个 Release 了，换一个版本号" }
}

# ---------------------------------------------------------------- 2. 版本号
Step "2/6 版本号 → $Version"
$g = [IO.File]::ReadAllText($Gradle)
if ($g -notmatch 'versionCode = (\d+)') { Fail "build.gradle.kts 里找不到 versionCode" }
$oldCode = [int]$Matches[1]
$g -match 'versionName = "([^"]+)"' | Out-Null
$oldName = $Matches[1]
if ($oldName -eq $Version) {
    Write-Host "版本号已经是 $Version（上次发布中途失败后重跑），不再改动"
} else {
    $g = $g -replace 'versionCode = \d+', "versionCode = $($oldCode + 1)"
    $g = $g -replace 'versionName = "[^"]+"', "versionName = `"$Version`""
    [IO.File]::WriteAllText($Gradle, $g, (New-Object Text.UTF8Encoding $false))
    Write-Host "versionCode $oldCode → $($oldCode + 1)，versionName $oldName → $Version"
    if (-not $NoUpload) {
        Run "git" @("commit", "-q", "-m", "EasyClash $Version", "--", "app/build.gradle.kts") "提交版本号"
    }
}
$Sha = Text "git" @("rev-parse", "HEAD")
if (-not $NoUpload) {
    Run "git" @("push", "origin", "main") "推送到 GitHub"
    Write-Host "已推送 $($Sha.Substring(0,7))，云端开始编译 macOS 版（约 10～20 分钟），本机同时编译安卓版和 Windows 版"
}

Remove-Item $Out -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $Out | Out-Null

# ---------------------------------------------------------------- 3. 安卓
Step "3/6 安卓 APK（内核 mihomo $MihomoVersion）"
$setupArgs = @("-BuildOnly", "-MihomoVersion", $MihomoVersion)
if ($SkipCore) { $setupArgs += "-SkipCore" }
& (Join-Path $Root "setup-windows.ps1") @setupArgs
if ($LASTEXITCODE -ne 0) { Fail "安卓版编译失败（见上面的输出）" }
# setup-windows.ps1 编内核时会改 core/go.mod，这不属于本次发布的源码，恢复原样
& git checkout -- core/go.mod 2>$null

$apk = Join-Path $Out "EasyClash-$Tag.apk"
Copy-Item (Join-Path $Root "app\build\outputs\apk\release\app-release.apk") $apk -Force
$verify = (& $apksigner.FullName verify --print-certs $apk 2>&1) -join "`n"
if ($LASTEXITCODE -ne 0) { Fail "APK 签名校验失败：`n$verify" }
if ($verify -notmatch 'certificate SHA-256 digest:\s*([0-9a-fA-F]+)') { Fail "读不出 APK 的签名证书：`n$verify" }
$apkCert = Sha256Hex $Matches[1]
if ($apkCert -ne $expectedCert) { Fail "APK 的签名证书不是发布密钥（$apkCert），可能退回了 debug 签名。不能发布。" }
Write-Host "APK 签名核对通过：$apkCert" -ForegroundColor Green

# ---------------------------------------------------------------- 4. Windows
Step "4/6 Windows 版"
$desk = Join-Path $Root "desktop"
& (Join-Path $desk "build-windows.ps1") -Version $Version
if (-not $? -or -not (Test-Path (Join-Path $desk "EasyClash.exe"))) { Fail "Windows 版编译失败（见上面的输出）" }
Set-Location $Root
& git checkout -- desktop/go.mod 2>$null
$winDir = Join-Path $Out "EasyClash-Windows"
New-Item -ItemType Directory -Force -Path $winDir | Out-Null
Copy-Item (Join-Path $desk "EasyClash.exe") $winDir
Copy-Item (Join-Path $desk "README.md") $winDir
$zip = Join-Path $Out "EasyClash-Windows-$Tag.zip"
Compress-Archive -Path $winDir -DestinationPath $zip -CompressionLevel Optimal -Force
Remove-Item $winDir -Recurse -Force

# ---------------------------------------------------------------- 5. macOS（云端）
Step "5/6 macOS 版（云端编译 + 测试）"
$dmg = Join-Path $Out "EasyClash-macOS-$Tag.dmg"
if ($NoUpload -or $SkipMac) {
    Write-Host "跳过（-NoUpload / -SkipMac）" -ForegroundColor Yellow
} else {
    $runId = $null
    for ($i = 0; $i -lt 30 -and -not $runId; $i++) {
        $runId = Text "gh" @("run", "list", "--repo", $Repo, "--workflow", "build.yml", "--commit", $Sha, "--limit", "1", "--json", "databaseId", "--jq", ".[0].databaseId")
        if (-not $runId) { Start-Sleep -Seconds 10 }
    }
    if (-not $runId) { Fail "等了 5 分钟也没看到云端的编译任务。可以到 GitHub 的 Actions 页面看看，或加 -SkipMac 先发布安卓和 Windows 版" }
    Write-Host "云端任务 https://github.com/$Repo/actions/runs/$runId ，等它完成…"
    & gh run watch $runId --repo $Repo --interval 30 2>&1 | Select-Object -Last 15 | ForEach-Object { "$_" }
    $tmp = Join-Path $Out "mac-tmp"
    & gh run download $runId --repo $Repo --name easyclash-macos --dir $tmp 2>&1 | ForEach-Object { "$_" }
    $got = Get-ChildItem "$tmp\*.dmg" -ErrorAction SilentlyContinue | Select-Object -First 1
    if (-not $got) { Fail "云端 macOS 版没有通过编译或测试，dmg 不存在。原因见上面的链接（或 ci-logs-macos 分支）；要先发布另外两个平台，重跑时加 -SkipMac" }
    Move-Item $got.FullName $dmg -Force
    Remove-Item $tmp -Recurse -Force
}

# ---------------------------------------------------------------- 6. 发布
Step "6/6 校验值与发布"
$files = @($apk, $zip) + @(Get-Item $dmg -ErrorAction SilentlyContinue | ForEach-Object FullName)
$sums = $files | ForEach-Object { "{0}  {1}" -f (Get-FileHash $_ -Algorithm SHA256).Hash.ToLower(), (Split-Path $_ -Leaf) }
[IO.File]::WriteAllLines((Join-Path $Out "SHA256SUMS.txt"), $sums)
$sums | ForEach-Object { Write-Host $_ }

if ($NoUpload) {
    Write-Host "`n打包完成（未发布），文件在：$Out" -ForegroundColor Green
    Write-Host "注意：build.gradle.kts 里的版本号已改为 $Version 但没有提交；不发布的话用 git checkout -- app/build.gradle.kts 撤销"
    exit 0
}

# README 里的"最新版"直链指向固定文件名，所以每个文件再放一份固定名字的副本
Copy-Item $apk (Join-Path $Out "EasyClash.apk") -Force
Copy-Item $zip (Join-Path $Out "EasyClash-Windows.zip") -Force
if (Test-Path $dmg) { Copy-Item $dmg (Join-Path $Out "EasyClash-macOS.dmg") -Force }

$notes = Join-Path $Out "notes.md"
if ($NotesFile) {
    Copy-Item $NotesFile $notes -Force
} else {
    $prev = Text "git" @("describe", "--tags", "--abbrev=0", "$Sha^")
    $range = if ($prev) { "$prev..$Sha" } else { $Sha }
    $lines = (& git log $range --no-merges --format="- %s" 2>$null) | Where-Object { $_ -notmatch "^- EasyClash $([regex]::Escape($Version))$" }
    [IO.File]::WriteAllLines($notes, @($lines))
}
Add-Content $notes "`n安卓版签名证书 SHA-256：``$expectedCert``"
Add-Content $notes "文件校验值见 SHA256SUMS.txt。"

$assets = Get-ChildItem $Out -File | Where-Object { $_.Name -ne "notes.md" } | ForEach-Object FullName
$ghArgs = @("release", "create", $Tag, "--repo", $Repo, "--target", $Sha, "--title", "EasyClash $Tag", "--notes-file", $notes, "--latest") + $assets
Run "gh" $ghArgs "创建 GitHub Release"
Write-Host "`n发布完成：https://github.com/$Repo/releases/tag/$Tag" -ForegroundColor Green
if (-not (Test-Path $dmg)) { Write-Host "这次没有 macOS 版；云端修好后可用 gh release upload $Tag <dmg> 补上" -ForegroundColor Yellow }

} catch {
    Write-Host "`n!!!! 出错了：$($_.Exception.Message)" -ForegroundColor Red
    Write-Host "把这段输出发给 Claude 即可。已经推送的版本号提交不用撤销，修好问题后用同一个版本号重跑。"
    exit 1
}
