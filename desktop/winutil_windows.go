//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	wininet                   = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOptionW    = wininet.NewProc("InternetSetOptionW")
	procInternetQueryOptionW  = wininet.NewProc("InternetQueryOptionW")
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procGlobalFree            = kernel32.NewProc("GlobalFree")
	user32                    = windows.NewLazySystemDLL("user32.dll")
	procGetDpiForSystem       = user32.NewProc("GetDpiForSystem")
	procRegisterClassExW      = user32.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	procShowWindow            = user32.NewProc("ShowWindow")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procIsIconic              = user32.NewProc("IsIconic")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
	procAllowSetForegroundWnd = user32.NewProc("AllowSetForegroundWindow")
)

// ======================= 系统代理 =======================
// 用 WinINet 的"按连接设置"接口修改局域网连接的代理（和 Windows 设置 → 网络 → 代理 是同一处），
// 改完通知系统刷新，浏览器等程序立即生效。

const (
	optPerConnection   = 75
	optSettingsChanged = 39
	optRefresh         = 37
	perConnFlags       = 1
	perConnProxyServer = 2
	perConnProxyBypass = 3
	perConnAutoConfig  = 4
	proxyTypeDirect    = 1
	proxyTypeProxy     = 2
	proxyTypeAutoURL   = 4
	proxyTypeDetect    = 8
)

type perConnOption struct {
	Option uint32
	_      uint32
	Value  uintptr
}

type perConnOptionList struct {
	Size        uint32
	_           uint32
	Connection  uintptr
	OptionCount uint32
	OptionError uint32
	Options     uintptr
}

// proxyState 系统代理的一份完整设置（用于备份与恢复）
type proxyState struct {
	Flags  uint32 `json:"flags"`
	Server string `json:"server"`
	Bypass string `json:"bypass"`
	PAC    string `json:"pac"`
}

func queryProxy() (proxyState, error) {
	opts := []perConnOption{{Option: perConnFlags}, {Option: perConnProxyServer}, {Option: perConnProxyBypass}, {Option: perConnAutoConfig}}
	list := perConnOptionList{OptionCount: uint32(len(opts)), Options: uintptr(unsafe.Pointer(&opts[0]))}
	list.Size = uint32(unsafe.Sizeof(list))
	size := list.Size
	r, _, err := procInternetQueryOptionW.Call(0, optPerConnection, uintptr(unsafe.Pointer(&list)), uintptr(unsafe.Pointer(&size)))
	runtime.KeepAlive(opts)
	if r == 0 {
		return proxyState{}, err
	}
	readStr := func(v uintptr) string {
		if v == 0 {
			return ""
		}
		s := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(v)))
		_, _, _ = procGlobalFree.Call(v)
		return s
	}
	return proxyState{Flags: uint32(opts[0].Value), Server: readStr(opts[1].Value), Bypass: readStr(opts[2].Value), PAC: readStr(opts[3].Value)}, nil
}

func applyProxy(st proxyState) error {
	server, _ := windows.UTF16PtrFromString(st.Server)
	bypass, _ := windows.UTF16PtrFromString(st.Bypass)
	pac, _ := windows.UTF16PtrFromString(st.PAC)
	opts := []perConnOption{
		{Option: perConnFlags, Value: uintptr(st.Flags)},
		{Option: perConnProxyServer, Value: uintptr(unsafe.Pointer(server))},
		{Option: perConnProxyBypass, Value: uintptr(unsafe.Pointer(bypass))},
		{Option: perConnAutoConfig, Value: uintptr(unsafe.Pointer(pac))},
	}
	list := perConnOptionList{OptionCount: uint32(len(opts)), Options: uintptr(unsafe.Pointer(&opts[0]))}
	list.Size = uint32(unsafe.Sizeof(list))
	r, _, err := procInternetSetOptionW.Call(0, optPerConnection, uintptr(unsafe.Pointer(&list)), uintptr(list.Size))
	runtime.KeepAlive(opts)
	runtime.KeepAlive(server)
	runtime.KeepAlive(bypass)
	runtime.KeepAlive(pac)
	if r == 0 {
		return errors.New("设置系统代理失败：" + err.Error())
	}
	_, _, _ = procInternetSetOptionW.Call(0, optSettingsChanged, 0, 0)
	_, _, _ = procInternetSetOptionW.Call(0, optRefresh, 0, 0)
	return nil
}

const proxyBypass = "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;172.20.*;172.21.*;172.22.*;172.23.*;172.24.*;172.25.*;172.26.*;172.27.*;172.28.*;172.29.*;172.30.*;172.31.*;192.168.*;<local>"

func backupPath(dataDir string) string { return filepath.Join(dataDir, "sysproxy-backup.json") }

// proxyBackup 开启前用户原来的系统代理设置，以及本软件设成的地址（用来判断恢复时哪些是自己的）
type proxyBackup struct {
	proxyState
	Ours string `json:"ours"`
}

func readBackup(dataDir string) *proxyBackup {
	b, err := os.ReadFile(backupPath(dataDir))
	if err != nil {
		return nil
	}
	var bk proxyBackup
	if json.Unmarshal(b, &bk) != nil {
		return nil
	}
	return &bk
}

// enableSystemProxy 把系统代理指向 127.0.0.1:port。第一次开启前先备份用户原来的设置（比如别的代理软件的端口）。
func enableSystemProxy(dataDir string, port int) error {
	bk := readBackup(dataDir)
	if bk == nil {
		cur, err := queryProxy()
		if err != nil {
			return errors.New("读取当前系统代理设置失败：" + err.Error())
		}
		bk = &proxyBackup{proxyState: cur}
		if cur.Flags&proxyTypeProxy != 0 && cur.Server != "" {
			appendLog("[APP] 原来的系统代理是 " + cur.Server + "（断开或退出时恢复）")
		}
	}
	bk.Ours = ourProxyServer(port)
	b, _ := json.Marshal(bk)
	if err := os.WriteFile(backupPath(dataDir), b, 0o644); err != nil {
		return errors.New("备份系统代理设置失败：" + err.Error())
	}
	return applyProxy(proxyState{Flags: proxyTypeDirect | proxyTypeProxy, Server: bk.Ours, Bypass: proxyBypass})
}

// restoreSystemProxy 恢复开启前的系统代理设置（没有备份时直接关闭代理）
func restoreSystemProxy(dataDir string) error {
	st := proxyState{Flags: proxyTypeDirect}
	if bk := readBackup(dataDir); bk != nil {
		st = bk.proxyState
		// 备份里记的恰好就是本软件自己的地址（不应发生，兜底），恢复成直连
		if st.Server == bk.Ours && st.Flags&proxyTypeProxy != 0 {
			st = proxyState{Flags: proxyTypeDirect}
		}
	}
	err := applyProxy(st)
	if err == nil {
		_ = os.Remove(backupPath(dataDir))
	}
	return err
}

// recoverSystemProxy 启动时调用：上次没正常退出（备份文件还在）就先恢复，免得系统代理指向一个已经不存在的端口
func recoverSystemProxy(dataDir string) bool {
	if _, err := os.Stat(backupPath(dataDir)); err == nil {
		_ = restoreSystemProxy(dataDir)
		return true
	}
	return false
}

// systemProxyURL 当前系统代理（可能是别的代理软件）转成可用的代理地址，没开代理时返回空。
// 系统代理的写法有两种："127.0.0.1:7897"，或按协议分开的 "http=…;https=…;socks=…"。
func systemProxyURL() (proxyURL, hostPort string) {
	s := strings.TrimSpace(systemProxyNow())
	if s == "" {
		return "", ""
	}
	if !strings.Contains(s, "=") {
		h := strings.TrimPrefix(strings.TrimPrefix(s, "http://"), "https://")
		return "http://" + h, h
	}
	parts := map[string]string{}
	for _, p := range strings.Split(s, ";") {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) == 2 {
			parts[strings.ToLower(kv[0])] = strings.TrimPrefix(strings.TrimPrefix(kv[1], "http://"), "https://")
		}
	}
	for _, k := range []string{"https", "http"} {
		if h := parts[k]; h != "" {
			return "http://" + h, h
		}
	}
	if h := parts["socks"]; h != "" {
		return "socks5://" + h, h
	}
	return "", ""
}

// systemProxyNow 当前系统代理指向哪里（没开代理时返回空）
func systemProxyNow() string {
	cur, err := queryProxy()
	if err != nil || cur.Flags&proxyTypeProxy == 0 {
		return ""
	}
	return cur.Server
}

// ======================= 管理员 / 重新启动 =======================

// tunSupported Windows 版支持 TUN 虚拟网卡模式（需要以管理员身份运行）
const tunSupported = true

// logEOL 日志文件的换行符（记事本要 \r\n）
const logEOL = "\r\n"

func isAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }

// relaunch 用 ShellExecute 重新启动自己；elevated=true 时弹 UAC 请求管理员权限
func relaunch(elevated bool, args string) error {
	verb := "open"
	if elevated {
		verb = "runas"
	}
	v, _ := windows.UTF16PtrFromString(verb)
	e, _ := windows.UTF16PtrFromString(exePath())
	a, _ := windows.UTF16PtrFromString(args)
	d, _ := windows.UTF16PtrFromString(filepath.Dir(exePath()))
	err := windows.ShellExecute(0, v, e, a, d, windows.SW_SHOWNORMAL)
	if err != nil && errors.Is(err, windows.ERROR_CANCELLED) {
		return errors.New("已取消（没有获得管理员权限）")
	}
	return err
}

// ======================= 开机自启 =======================

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func setAutoStart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue("EasyClash", `"`+exePath()+`" --autostart`)
	}
	err = k.DeleteValue("EasyClash")
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

// ======================= clash:// 一键导入 =======================

// registerURLProtocol 注册 clash:// 与 clashmeta:// 由本软件打开。已被别的软件占用时不抢（force=false）。
func registerURLProtocol(force bool) {
	me := `"` + exePath() + `" "%1"`
	for _, scheme := range []string{"clash", "clashmeta"} {
		if !force {
			if k, err := registry.OpenKey(registry.CLASSES_ROOT, scheme+`\shell\open\command`, registry.QUERY_VALUE); err == nil {
				cur, _, _ := k.GetStringValue("")
				k.Close()
				if cur != "" && !strings.Contains(strings.ToLower(cur), "easyclash") {
					continue
				}
			}
		}
		base := `Software\Classes\` + scheme
		if k, _, err := registry.CreateKey(registry.CURRENT_USER, base, registry.SET_VALUE); err == nil {
			_ = k.SetStringValue("", "URL:Clash Protocol")
			_ = k.SetStringValue("URL Protocol", "")
			k.Close()
		}
		if k, _, err := registry.CreateKey(registry.CURRENT_USER, base+`\shell\open\command`, registry.SET_VALUE); err == nil {
			_ = k.SetStringValue("", me)
			k.Close()
		}
	}
}

// ======================= 进程列表（分程序代理用） =======================

func listProcesses() []ProcInfo {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	seen := map[string]*ProcInfo{}
	winDir := strings.ToLower(os.Getenv("SystemRoot"))
	if winDir == "" {
		winDir = `c:\windows`
	}
	self := strings.ToLower(filepath.Base(exePath()))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		name := windows.UTF16ToString(pe.ExeFile[:])
		key := strings.ToLower(name)
		if name == "" || key == "[system process]" || key == "system" || key == self {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		path := ""
		if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pe.ProcessID); err == nil {
			buf := make([]uint16, windows.MAX_PATH*2)
			n := uint32(len(buf))
			if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
				path = windows.UTF16ToString(buf[:n])
			}
			windows.CloseHandle(h)
		}
		sys := path == "" || strings.HasPrefix(strings.ToLower(path), winDir)
		seen[key] = &ProcInfo{Name: name, Path: path, System: sys}
	}
	out := make([]ProcInfo, 0, len(seen))
	for _, p := range seen {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return !out[i].System
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// ======================= 单实例 =======================

var instanceMutex windows.Handle

// acquireSingleInstance 返回 false 表示已有一个 EasyClash 在运行
func acquireSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\EasyClashWindowsSingleInstance`)
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil && errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return false
	}
	instanceMutex = h
	return true
}

func releaseSingleInstance() {
	if instanceMutex != 0 {
		windows.CloseHandle(instanceMutex)
		instanceMutex = 0
	}
}

// ======================= 杂项 =======================

func systemDPI() int {
	if procGetDpiForSystem.Find() == nil {
		if r, _, _ := procGetDpiForSystem.Call(); r > 0 {
			return int(r)
		}
	}
	return 96
}

func openFolder(dir string) { _ = exec.Command("explorer.exe", dir).Start() }

func openURL(u string) {
	_ = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", u).Start()
}

func messageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40) // MB_ICONINFORMATION
}

func bringToFront(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		_, _, _ = procShowWindow.Call(hwnd, 9) // SW_RESTORE
	} else {
		_, _, _ = procShowWindow.Call(hwnd, 5) // SW_SHOW
	}
	_, _, _ = procSetForegroundWindow.Call(hwnd)
}

func allowForegroundAny() { _, _, _ = procAllowSetForegroundWnd.Call(uintptr(0xFFFFFFFF)) } // ASFW_ANY

// ======================= 关机 / 注销时恢复系统代理 =======================
// GUI 程序收不到控制台的关机信号，只能靠一个顶层隐藏窗口接收 WM_ENDSESSION。

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	_       uint32
}

func watchSessionEnd(onEnd func()) {
	go func() {
		runtime.LockOSThread()
		var hinst windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &hinst)
		cls, _ := windows.UTF16PtrFromString("EasyClashSessionWatcher")
		proc := syscall.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
			switch msg {
			case 0x0011: // WM_QUERYENDSESSION
				return 1
			case 0x0016: // WM_ENDSESSION
				if wp != 0 {
					onEnd()
				}
				return 0
			}
			r, _, _ := procDefWindowProcW.Call(hwnd, msg, wp, lp)
			return r
		})
		wc := wndClassEx{WndProc: proc, Instance: hinst, ClassName: cls}
		wc.Size = uint32(unsafe.Sizeof(wc))
		_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, 0, uintptr(hinst), 0)
		if hwnd == 0 {
			return
		}
		var m winMsg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}()
}
