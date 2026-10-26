package main

import (
	"embed"
	_ "embed"
)

// 界面文件
//
//go:embed ui
var uiFS embed.FS

// 地理数据库（gzip 压缩后打包，首次连接时释放到数据目录）
//
//go:embed assets/*.gz
var geoAssets embed.FS

//go:embed assets/tray_on.ico
var trayIconOn []byte

//go:embed assets/tray_off.ico
var trayIconOff []byte

// macOS 菜单栏模板图标（黑色 + 透明度，系统自动适配浅色 / 深色菜单栏）
//
//go:embed assets/tray_mac_on.png
var trayMacOn []byte

//go:embed assets/tray_mac_off.png
var trayMacOff []byte
