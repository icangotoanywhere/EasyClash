package main

import (
	"runtime"

	"fyne.io/systray"
)

// 托盘（Windows 右下角 / macOS 顶部菜单栏）：菜单里可连接 / 断开 / 切换出站模式 / 打开界面 / 退出。
// 图标、点击行为等平台差异见 tray_windows.go / tray_darwin.go

// onExit：macOS 上「退出」时系统直接结束进程，清理工作（断开、恢复系统代理）必须放在这里做
func runTray(app *App, ui *UI, onExit func()) {
	runtime.LockOSThread() // 托盘窗口和它的消息循环必须在同一个系统线程
	systray.Run(func() { trayReady(app, ui) }, onExit)
}

func stopTray() { systray.Quit() }

func trayReady(app *App, ui *UI) {
	setTrayIcon(false)
	systray.SetTooltip("EasyClash：未连接")
	if trayClickOpensUI {
		systray.SetOnTapped(func() { ui.Show() })
	}

	mStatus := systray.AddMenuItem("EasyClash · 未连接", "")
	mStatus.Disable()
	systray.AddSeparator()
	mToggle := systray.AddMenuItem("连接", "连接或断开代理")
	mMode := systray.AddMenuItem("出站模式", "")
	mRule := mMode.AddSubMenuItemCheckbox("规则（国内直连、国外走代理）", "", false)
	mGlobal := mMode.AddSubMenuItemCheckbox("全局（全部走代理）", "", false)
	mDirect := mMode.AddSubMenuItemCheckbox("直连（全部不走代理）", "", false)
	mShow := systray.AddMenuItem(trayShowLabel, "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出 EasyClash", "断开代理、恢复系统代理设置并退出")

	modeName := map[string]string{"rule": "规则", "global": "全局", "direct": "直连"}
	refresh := func() {
		st := app.store.Settings()
		status := app.Status()
		label := map[string]string{stIdle: "未连接", stConnecting: "正在连接…", stConnected: "已连接", stStopping: "正在断开…"}[status]
		app.mu.Lock()
		tun := app.tunOn
		app.mu.Unlock()
		if status == stConnected {
			setTrayIcon(true)
			how := "系统代理"
			if tun {
				how = "TUN"
			}
			app.mu.Lock()
			warn := app.nodeWarn != ""
			app.mu.Unlock()
			if warn {
				systray.SetTooltip("EasyClash：已连接，但当前节点连不上")
			} else {
				systray.SetTooltip("EasyClash：已连接（" + modeName[st.Mode] + " · " + how + "）")
			}
			mToggle.SetTitle("断开")
		} else {
			setTrayIcon(false)
			systray.SetTooltip("EasyClash：" + label)
			mToggle.SetTitle("连接")
		}
		mStatus.SetTitle("EasyClash · " + label)
		if status == stConnecting || status == stStopping {
			mToggle.Disable()
		} else {
			mToggle.Enable()
		}
		for m, item := range map[string]*systray.MenuItem{"rule": mRule, "global": mGlobal, "direct": mDirect} {
			if st.Mode == m {
				item.Check()
			} else {
				item.Uncheck()
			}
		}
	}
	app.mu.Lock()
	app.onChange = refresh
	app.mu.Unlock()
	refresh()

	go func() {
		for {
			select {
			case <-mToggle.ClickedCh:
				if app.Status() == stConnected {
					go app.Disconnect()
				} else {
					go app.Connect()
				}
			case <-mRule.ClickedCh:
				app.SetMode("rule")
			case <-mGlobal.ClickedCh:
				app.SetMode("global")
			case <-mDirect.ClickedCh:
				app.SetMode("direct")
			case <-mShow.ClickedCh:
				ui.Show()
			case <-mQuit.ClickedCh:
				ui.Quit()
				return
			}
		}
	}()
}
