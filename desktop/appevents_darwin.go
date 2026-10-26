package main

/*
#cgo LDFLAGS: -framework Cocoa -framework CoreServices
void ecInstallEventHandlers(void);
void ecClaimURLSchemes(void);
*/
import "C"

import (
	"strings"
	"sync"
)

// 处理 macOS 发给程序的两类事件（实现在 appevents_darwin.m）：
//   - 程序已在运行时用户又双击了它（或在启动台点它）：打开控制面板
//   - 网页上点了 clash:// 一键导入链接：把订阅交给订阅页

var (
	evMu  sync.Mutex
	evApp *App
	evUI  *UI
)

func installAppleEventHandlers(app *App, ui *UI) {
	evMu.Lock()
	evApp, evUI = app, ui
	evMu.Unlock()
	C.ecInstallEventHandlers()
}

//export goOnReopen
func goOnReopen() {
	evMu.Lock()
	ui := evUI
	evMu.Unlock()
	if ui != nil {
		appendLog("[APP] 程序被再次打开，打开控制面板")
		go ui.Show()
	}
}

//export goOnOpenURL
func goOnOpenURL(u *C.char) {
	s := C.GoString(u)
	evMu.Lock()
	app, ui := evApp, evUI
	evMu.Unlock()
	if app == nil || !(strings.HasPrefix(s, "clash://") || strings.HasPrefix(s, "clashmeta://")) {
		return
	}
	appendLog("[APP] 收到一键导入链接")
	app.mu.Lock()
	app.pendingImport = s
	app.mu.Unlock()
	go app.changed()
	if ui != nil {
		go ui.Show()
	}
}

// registerURLProtocol macOS：安装包里已声明 clash:// 与 clashmeta://，系统会自动登记。
// force=true（用户在高级页点了按钮）时，再把本软件设为它们的默认打开方式。
func registerURLProtocol(force bool) {
	if force {
		C.ecClaimURLSchemes()
	}
}
