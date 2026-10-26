package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/systray"
)

// macOS 版没有内嵌窗口：常驻顶部菜单栏，控制面板在默认浏览器里打开（同一套界面，和 Windows 版完全一致）。
// 这样不依赖任何系统网页组件的版本差异，最稳。

type UI struct {
	app     *App
	dataDir string
	debug   bool
	url     string
	quitCh  chan struct{}
	quitOne sync.Once
}

func newUI(app *App, dataDir string, debug bool) *UI {
	return &UI{app: app, dataDir: dataDir, debug: debug, quitCh: make(chan struct{})}
}

// Show 在默认浏览器里打开控制面板
func (u *UI) Show() {
	if u.url != "" {
		openURL(u.url)
	}
}

// Quit 退出程序（菜单栏「退出」）
func (u *UI) Quit() {
	u.quitOne.Do(func() {
		close(u.quitCh)
		systray.Quit()
	})
}

// RestartElevated Mac 版不提供（TUN 模式暂不支持）
func (u *UI) RestartElevated() {
	u.app.setError("Mac 版暂不支持 TUN 模式，请使用系统代理")
}

// runMainLoop macOS：菜单栏图标必须在主线程跑系统消息循环。
// 用户点「退出」或注销关机时，系统在结束程序前调用 cleanup（断开、恢复系统代理），之后进程直接结束。
func runMainLoop(app *App, ui *UI, show bool, cleanup func()) {
	installAppleEventHandlers(app, ui)
	if show {
		ui.Show()
		go ui.firstRunHint()
	}
	runTray(app, ui, func() {
		app.mu.Lock()
		app.onChange = nil // 退出过程中不再刷新菜单栏
		app.mu.Unlock()
		cleanup()
	})
}

// firstRunHint 第一次启动时提醒一次：软件在菜单栏里运行，控制面板在浏览器里
func (u *UI) firstRunHint() {
	marker := filepath.Join(u.dataDir, ".menubar-hint-shown")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	_ = os.WriteFile(marker, []byte("1"), 0o644)
	if _, err := os.Stat(filepath.Join(u.dataDir, ".test-no-browser")); err == nil {
		return
	}
	messageBox("EasyClash 已在菜单栏运行", strings.Join([]string{
		"EasyClash 常驻在屏幕顶部的菜单栏（右上角的双环图标），控制面板已在浏览器里打开。",
		"",
		"• 点菜单栏图标：连接 / 断开 / 切换模式 / 打开控制面板 / 退出",
		"• 关闭浏览器标签页不影响代理",
		"• 退出 EasyClash 时会自动恢复原来的系统代理设置",
		"",
		"这条提示只出现一次。",
	}, "\n"))
}
