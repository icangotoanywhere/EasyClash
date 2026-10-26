package main

import "fyne.io/systray"

// Windows：单击托盘图标打开窗口，右键弹菜单
const trayClickOpensUI = true

const trayShowLabel = "显示主界面"

func setTrayIcon(on bool) {
	if on {
		systray.SetIcon(trayIconOn)
	} else {
		systray.SetIcon(trayIconOff)
	}
}
