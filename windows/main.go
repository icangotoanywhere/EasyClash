package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	webview2 "github.com/jchv/go-webview2"
)

var appVersion = "1.0.0" // 编译时用 -ldflags "-X main.appVersion=..." 覆盖

func init() {
	// WebView2 窗口必须在主线程创建并跑消息循环
	runtime.LockOSThread()
}

func main() {
	var (
		dataFlag   = flag.String("data", "", "数据目录（默认 %APPDATA%\\EasyClash）")
		autostart  = flag.Bool("autostart", false, "开机自启时使用：只在托盘运行，不弹窗口")
		headless   = flag.Bool("headless", false, "测试用：不开窗口和托盘，只运行后台")
		noSysProxy = flag.Bool("no-sysproxy", false, "测试用：不修改系统代理")
		uiPort     = flag.Int("ui-port", 0, "测试用：界面服务固定端口")
		waitInst   = flag.Bool("wait-instance", false, "重新启动时等待旧进程退出")
		debugUI    = flag.Bool("debug-ui", false, "允许右键菜单和开发者工具")
		checkCfg   = flag.Bool("check-config", false, "测试用：按当前设置生成配置并检查，不启动")
	)
	flag.Parse()

	dataDir := *dataFlag
	if dataDir == "" {
		base, err := os.UserConfigDir() // %APPDATA%
		if err != nil {
			base = filepath.Dir(exePath())
		}
		dataDir = filepath.Join(base, "EasyClash")
	}
	_ = os.MkdirAll(dataDir, 0o755)
	// 崩溃时 Go 运行时把错误写进 crash.txt（界面「诊断」里会显示）；空文件说明上次没崩
	crashFile := filepath.Join(dataDir, "crash.txt")
	if fi, err := os.Stat(crashFile); err == nil && fi.Size() == 0 {
		_ = os.Remove(crashFile)
	}
	if f, err := os.OpenFile(crashFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		_ = debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close()
	}

	openLogFile(logDir(dataDir))

	if *checkCfg {
		// 测试用：按当前设置生成配置并让内核解析一遍，不启动
		os.Exit(checkConfig(dataDir))
	}

	importURL := ""
	for _, arg := range flag.Args() {
		if strings.HasPrefix(arg, "clash://") || strings.HasPrefix(arg, "clashmeta://") {
			importURL = arg
		}
	}

	if !acquireSingleInstance() {
		ok := false
		if *waitInst {
			for i := 0; i < 40 && !ok; i++ {
				time.Sleep(250 * time.Millisecond)
				ok = acquireSingleInstance()
			}
		}
		if !ok {
			// 已经有一个在运行：让它显示窗口（或把一键导入的链接交给它），自己退出
			signalRunningInstance(dataDir, importURL)
			return
		}
	}

	store := openStore(dataDir)
	if !*noSysProxy && recoverSystemProxy(dataDir) {
		appendLog("[APP] 上次没有正常退出，已恢复原来的系统代理设置")
	}
	app := newApp(store, dataDir, *noSysProxy)
	if importURL != "" {
		app.pendingImport = importURL
	}
	ui := newUI(app, dataDir, *debugUI)
	srv, err := startServer(app, ui, *uiPort)
	if err != nil {
		messageBox("EasyClash", "界面服务启动失败："+err.Error())
		return
	}
	ui.url = srv.URL()
	appendLog("[APP] EasyClash " + appVersion + " 已启动，内核 mihomo " + coreVersion())

	if !*headless {
		registerURLProtocol(false)
		if store.Settings().AutoStart {
			_ = setAutoStart(true) // 程序挪了位置时刷新路径
		}
	}
	watchSessionEnd(func() { app.Shutdown() })

	if store.Settings().AutoConnect {
		go app.Connect()
	}

	if *headless {
		fmt.Println(srv.URL())
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		go func() { <-ui.quitCh; ch <- os.Interrupt }()
		<-ch
		app.Shutdown()
		releaseSingleInstance()
		return
	}

	go runTray(app, ui)
	if !*autostart {
		ui.Show()
	}
	ui.mainLoop()
	app.Shutdown()
	stopTray()
	removeInstanceFileIfOurs(dataDir)
	releaseSingleInstance()
}

// removeInstanceFileIfOurs 只删自己写的 instance.json（以管理员身份重启时新进程可能已经写了新的）
func removeInstanceFileIfOurs(dataDir string) {
	b, err := os.ReadFile(instanceFile(dataDir))
	if err != nil {
		return
	}
	var inf instanceInfo
	if json.Unmarshal(b, &inf) == nil && inf.PID == os.Getpid() {
		_ = os.Remove(instanceFile(dataDir))
	}
}

func signalRunningInstance(dataDir, importURL string) {
	b, err := os.ReadFile(instanceFile(dataDir))
	if err != nil {
		return
	}
	var inf instanceInfo
	if json.Unmarshal(b, &inf) != nil {
		return
	}
	allowForegroundAny()
	path, body := "/api/show", []byte("{}")
	if importURL != "" {
		path = "/api/import"
		body, _ = json.Marshal(map[string]string{"url": importURL})
	}
	req, _ := http.NewRequest("POST", "http://127.0.0.1:"+itoa(inf.Port)+path, bytes.NewReader(body))
	req.Header.Set("X-EC-Token", inf.Token)
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 5 * time.Second}
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
	}
}

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


// checkConfig 生成最终配置并用内核解析一遍，结果写到 check-config.txt
func checkConfig(dataDir string) int {
	store := openStore(dataDir)
	st := store.Settings()
	out := filepath.Join(dataDir, "check-config.txt")
	report := func(code int, msg string) int { _ = os.WriteFile(out, []byte(msg), 0o644); return code }
	sub, ok := store.SelectedSub()
	if !ok {
		return report(2, "没有选中的订阅")
	}
	y, err := store.RawYaml(sub.ID)
	if err != nil {
		return report(2, err.Error())
	}
	extractGeoFiles(store.HomeDir())
	res, err := buildConfig(y, st, store.RunDir(), "127.0.0.1:0", "check", st.TunMode)
	if err != nil {
		return report(1, "生成失败："+err.Error())
	}
	if err := coreValidate(store.HomeDir(), res.Path); err != nil {
		return report(1, "内核解析失败："+err.Error())
	}
	return report(0, fmt.Sprintf("OK 主组=%s 地区组=%d tun=%v 文件=%s", res.MainGroup, len(res.Regions), st.TunMode, res.Path))
}
