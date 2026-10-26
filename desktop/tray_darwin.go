package main

import "fyne.io/systray"

// macOS：点菜单栏图标弹出菜单（系统惯例），从菜单里打开控制面板
const trayClickOpensUI = false

const trayShowLabel = "打开控制面板…"

// 菜单栏用"模板图标"：系统按浅色 / 深色菜单栏自动着色；未连接时图标半透明
func setTrayIcon(on bool) {
	if on {
		systray.SetTemplateIcon(trayMacOn, trayMacOn)
	} else {
		systray.SetTemplateIcon(trayMacOff, trayMacOff)
	}
}
