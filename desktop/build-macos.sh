#!/bin/bash
# EasyClash macOS 版编译脚本：生成 dist/EasyClash.app 和 dist/EasyClash-macOS-v版本号.dmg（Intel / M 系列芯片通用）
#
# 用法：./build-macos.sh 1.0.0
# 需要：Go 1.26 以上、Xcode 命令行工具（终端里执行 xcode-select --install 安装）
set -euo pipefail

VER="${1:-1.0.0}"
ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

export GOFLAGS=-mod=mod
export CGO_ENABLED=1                  # 菜单栏图标和系统事件要用到 macOS 的 Cocoa 框架
export MACOSX_DEPLOYMENT_TARGET=12.0  # Go 1.26 最低支持 macOS 12

step() { echo; echo "==== $* ===="; }

step "1/5 准备地理数据库（压缩后打包进程序）"
mkdir -p assets
for f in geoip.metadb geosite.dat GeoLite2-ASN.mmdb; do
  if [ -f "assets/$f.gz" ]; then echo "已存在：assets/$f.gz"; continue; fi
  src="../app/src/main/assets/$f"   # 安卓版已经下载过的直接复用
  if [ ! -f "$src" ]; then
    src="$(mktemp -d)/$f"
    echo "下载 $f …"
    curl -fL --retry 3 -o "$src" "https://github.com/MetaCubeX/meta-rules-dat/releases/download/latest/$f"
  fi
  gzip -9 -c "$src" > "assets/$f.gz"
done
ls -la assets

step "2/5 编译（arm64 + amd64，合并成通用程序）"
go version
go mod tidy
MIHOMO=$(go list -m -f '{{.Version}}' github.com/metacubex/mihomo)
LDF="-s -w -X main.appVersion=$VER -X github.com/metacubex/mihomo/constant.Version=$MIHOMO"
rm -rf dist && mkdir -p dist/bin
for arch in arm64 amd64; do
  echo "编译 $arch …"
  GOOS=darwin GOARCH=$arch go build -trimpath -tags with_gvisor -ldflags "$LDF" -o "dist/bin/EasyClash-$arch" .
done
lipo -create -output dist/bin/EasyClash dist/bin/EasyClash-arm64 dist/bin/EasyClash-amd64
lipo -info dist/bin/EasyClash

step "3/5 打包 EasyClash.app"
APP=dist/EasyClash.app
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp dist/bin/EasyClash "$APP/Contents/MacOS/EasyClash"
sed "s/__VERSION__/$VER/g" macos/Info.plist > "$APP/Contents/Info.plist"
plutil -lint "$APP/Contents/Info.plist"
printf 'APPL????' > "$APP/Contents/PkgInfo"
ICONSET=dist/AppIcon.iconset
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  sips -z $s $s macos/icon_1024.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  d=$((s * 2))
  sips -z $d $d macos/icon_1024.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"

step "4/5 签名（本机临时签名：没有苹果开发者证书，第一次打开需在「隐私与安全性」里允许）"
codesign --force --deep --sign - "$APP"
codesign --verify --deep --verbose=2 "$APP"
codesign -dv "$APP" 2>&1 | head -5

step "5/5 制作 dmg"
STAGE=dist/dmg
mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
cp "macos/第一次打开必看.txt" "$STAGE/"
DMG="dist/EasyClash-macOS-v$VER.dmg"
rm -f "$DMG"
# GitHub 的云端 Mac 上 hdiutil 偶尔报"资源忙"，失败就重试
for i in 1 2 3 4 5; do
  if hdiutil create -volname "EasyClash" -srcfolder "$STAGE" -ov -format UDZO "$DMG"; then break; fi
  echo "hdiutil 失败，第 $i 次重试…"
  sleep 5
done
[ -f "$DMG" ]
ls -la "$DMG"
rm -rf "$STAGE" "$ICONSET" # 只留一份 EasyClash.app，免得系统登记到两个同名程序
echo
echo "完成：$ROOT/$APP"
echo "      $ROOT/$DMG"
