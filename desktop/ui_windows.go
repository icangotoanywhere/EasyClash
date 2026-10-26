//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sync"

	webview2 "github.com/jchv/go-webview2"
)

// ======================= 窗口 =======================

type UI struct {
	app     *App
	dataDir string
	debug   bool
	url     string
	showCh  chan struct{}
	quitCh  chan struct{}
	quitOne sync.Once
	mu      sync.Mutex
	cur     webview2.WebView
}

func newUI(app *App, dataDir string, debug bool) *UI {
	return &UI{app: app, dataDir: dataDir, debug: debug, showCh: make(chan struct{}, 1), quitCh: make(chan struct{})}
}

// Show 显示主窗口：已打开就提到最前，否则新建
func (u *UI) Show() {
	u.mu.Lock()
	w := u.cur
	u.mu.Unlock()
	if w != nil {
		w.Dispatch(func() { bringToFront(uintptr(w.Window())) })
		return
	}
	select {
	case u.showCh <- struct{}{}:
	default:
	}
}

// Quit 退出程序（托盘「退出」）
func (u *UI) Quit() {
	u.quitOne.Do(func() {
		close(u.quitCh)
		u.mu.Lock()
		w := u.cur
		u.mu.Unlock()
		if w != nil {
			w.Dispatch(func() { w.Terminate() })
		}
	})
}

// RestartElevated 以管理员身份重新启动（TUN 模式需要）
func (u *UI) RestartElevated() {
	u.app.Shutdown()
	releaseSingleInstance()
	if err := relaunch(true, "--wait-instance"); err != nil {
		// 用户取消了 UAC：重新占住单实例锁，继续运行
		acquireSingleInstance()
		u.app.setError("没有以管理员身份重新启动：" + err.Error())
		return
	}
	u.Quit()
}

func (u *UI) mainLoop() {
	for {
		select {
		case <-u.quitCh:
			return
		case <-u.showCh:
			u.openWindow()
			select {
			case <-u.quitCh:
				return
			default:
			}
			u.firstCloseHint()
		}
	}
}

func (u *UI) openWindow() {
	scale := float64(systemDPI()) / 96
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     u.debug,
		AutoFocus: true,
		DataPath:  filepath.Join(u.dataDir, "webview"),
		WindowOptions: webview2.WindowOptions{
			Title: "EasyClash", Width: uint(1000 * scale), Height: uint(760 * scale), IconId: 1, Center: true,
		},
	})
	if w == nil {
		// 没有 WebView2 运行库时退而求其次：用默认浏览器打开界面
		openURL(u.url)
		messageBox("EasyClash", "没能创建程序窗口（可能缺少 Microsoft Edge WebView2 运行库），已改用浏览器打开界面。\n\n关闭浏览器标签页不影响代理，软件在右下角托盘继续运行。")
		return
	}
	w.SetSize(int(760*scale), int(560*scale), webview2.HintMin)
	u.mu.Lock()
	u.cur = w
	u.mu.Unlock()
	w.Navigate(u.url)
	w.Run()
	u.mu.Lock()
	u.cur = nil
	u.mu.Unlock()
	w.Destroy()
}

// firstCloseHint 第一次关窗口时提醒一次：软件还在托盘里运行
func (u *UI) firstCloseHint() {
	marker := filepath.Join(u.dataDir, ".tray-hint-shown")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	_ = os.WriteFile(marker, []byte("1"), 0o644)
	status := "未连接"
	if u.app.Status() == stConnected {
		status = "已连接，代理保持生效"
	}
	go messageBox("EasyClash 仍在运行", "窗口已关闭，EasyClash 继续在屏幕右下角的托盘里运行（"+status+"）。\n\n• 单击托盘图标：重新打开窗口\n• 右键托盘图标：连接 / 断开 / 切换模式 / 退出\n\n这条提示只出现一次。")
}

// runMainLoop Windows：托盘在后台线程，主线程负责 WebView2 窗口
func runMainLoop(app *App, ui *UI, show bool, cleanup func()) {
	go runTray(app, ui, func() {})
	if show {
		ui.Show()
	}
	ui.mainLoop()
	cleanup()
	stopTray()
}
