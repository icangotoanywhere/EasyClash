package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// macOS 平台相关的实现（与 winutil_windows.go 一一对应）。

// tunSupported Mac 版暂不提供 TUN 模式：macOS 上创建虚拟网卡需要 root 权限的后台服务，
// 先用系统代理（浏览器和绝大多数软件都遵守）。
const tunSupported = false

func isAdmin() bool { return os.Geteuid() == 0 }

func relaunch(elevated bool, args string) error {
	return errors.New("Mac 版暂不支持以管理员身份重新启动")
}

// ======================= 系统代理 =======================
// 用系统自带的 networksetup 修改「系统设置 → 网络 → 详细信息 → 代理」，对每个已启用的网络服务
// （Wi-Fi、以太网、USB 网卡……）都设置一遍，换网络也不会失效。改之前把原来的设置备份到
// sysproxy-backup.json，断开 / 退出时按备份恢复。
// networksetup 对管理员账户不需要输入密码；当前账户不是管理员时会失败，界面里给出手动设置的办法。

const networksetup = "/usr/sbin/networksetup"

var macBypass = []string{
	"127.0.0.1", "localhost", "*.local", "::1",
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "fe80::/10",
}

type macProxy struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Port    int    `json:"port"`
}

type macServiceProxy struct {
	Web         macProxy `json:"web"`
	Secure      macProxy `json:"secure"`
	Socks       macProxy `json:"socks"`
	Bypass      []string `json:"bypass"`
	AutoURL     string   `json:"autoUrl"`
	AutoEnabled bool     `json:"autoEnabled"`
}

// proxyBackup 开启前每个网络服务原来的代理设置，以及本软件设成的地址
type proxyBackup struct {
	Services map[string]macServiceProxy `json:"services"`
	Ours     string                     `json:"ours"`
}

func backupPath(dataDir string) string { return filepath.Join(dataDir, "sysproxy-backup.json") }

func readBackup(dataDir string) *proxyBackup {
	b, err := os.ReadFile(backupPath(dataDir))
	if err != nil {
		return nil
	}
	var bk proxyBackup
	if json.Unmarshal(b, &bk) != nil || bk.Services == nil {
		return nil
	}
	return &bk
}

// nsRun 执行 networksetup。它出错时常常仍返回 0，只在输出里写 "** Error"，所以两种都要判断。
func nsRun(args ...string) (string, error) {
	out, err := exec.Command(networksetup, args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	low := strings.ToLower(s)
	if strings.Contains(low, "requires admin") || strings.Contains(low, "admin privileges") {
		return s, errors.New("修改系统代理需要管理员账户：当前登录的 Mac 账户不是管理员。可以在「系统设置 → 网络 → 详细信息 → 代理」里手动把网页代理、安全网页代理都设成 127.0.0.1 和本软件的端口")
	}
	if err != nil || strings.HasPrefix(s, "** Error") {
		if s == "" && err != nil {
			s = err.Error()
		}
		return s, errors.New("networksetup " + strings.Join(args, " ") + "：" + s)
	}
	return s, nil
}

// listServices 已启用的网络服务名（第一行是说明，带 * 的是已停用的服务）
func listServices() ([]string, error) {
	out, err := nsRun("-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var svcs []string
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if i == 0 && strings.Contains(strings.ToLower(line), "asterisk") {
			continue
		}
		if line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		svcs = append(svcs, line)
	}
	if len(svcs) == 0 {
		return nil, errors.New("没有找到已启用的网络服务")
	}
	return svcs, nil
}

func parseKV(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

func getProxy(flag, svc string) macProxy {
	out, err := nsRun(flag, svc)
	if err != nil {
		return macProxy{}
	}
	m := parseKV(out)
	p, _ := strconv.Atoi(m["Port"])
	return macProxy{Enabled: strings.EqualFold(m["Enabled"], "Yes"), Server: m["Server"], Port: p}
}

func queryService(svc string) macServiceProxy {
	sp := macServiceProxy{
		Web:    getProxy("-getwebproxy", svc),
		Secure: getProxy("-getsecurewebproxy", svc),
		Socks:  getProxy("-getsocksfirewallproxy", svc),
	}
	if out, err := nsRun("-getproxybypassdomains", svc); err == nil && !strings.Contains(out, "aren't any") {
		for _, l := range strings.Split(out, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				sp.Bypass = append(sp.Bypass, l)
			}
		}
	}
	if out, err := nsRun("-getautoproxyurl", svc); err == nil {
		m := parseKV(out)
		if u := m["URL"]; u != "" && u != "(null)" {
			sp.AutoURL = u
		}
		sp.AutoEnabled = strings.EqualFold(m["Enabled"], "Yes")
	}
	return sp
}

// forEachService 对每个网络服务并行执行（networksetup 每次要几十毫秒，服务多时串行会明显变慢）
func forEachService(svcs []string, f func(svc string) error) error {
	var wg sync.WaitGroup
	errs := make([]error, len(svcs))
	for i, s := range svcs {
		wg.Add(1)
		go func(i int, s string) {
			defer wg.Done()
			errs[i] = f(s)
		}(i, s)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// enableSystemProxy 把每个网络服务的网页 / 安全网页 / SOCKS 代理都指向 127.0.0.1:port。
// 第一次开启前先备份原来的设置（比如别的代理软件的端口）。
func enableSystemProxy(dataDir string, port int) error {
	svcs, err := listServices()
	if err != nil {
		return errors.New("读取网络服务失败：" + err.Error())
	}
	bk := readBackup(dataDir)
	if bk == nil {
		bk = &proxyBackup{Services: map[string]macServiceProxy{}}
	}
	var mu sync.Mutex
	_ = forEachService(svcs, func(svc string) error {
		mu.Lock()
		_, have := bk.Services[svc]
		mu.Unlock()
		if !have {
			cur := queryService(svc)
			mu.Lock()
			bk.Services[svc] = cur
			mu.Unlock()
			if cur.Web.Enabled && cur.Web.Server != "" {
				appendLog("[APP] 「" + svc + "」原来的系统代理是 " + cur.Web.Server + ":" + itoa(cur.Web.Port) + "（断开或退出时恢复）")
			}
		}
		return nil
	})
	bk.Ours = ourProxyServer(port)
	b, _ := json.Marshal(bk)
	if err := os.WriteFile(backupPath(dataDir), b, 0o644); err != nil {
		return errors.New("备份系统代理设置失败：" + err.Error())
	}
	p := itoa(port)
	return forEachService(svcs, func(svc string) error {
		for _, f := range []string{"-setwebproxy", "-setsecurewebproxy", "-setsocksfirewallproxy"} {
			if _, err := nsRun(f, svc, "127.0.0.1", p); err != nil {
				return err
			}
		}
		if _, err := nsRun(append([]string{"-setproxybypassdomains", svc}, macBypass...)...); err != nil {
			return err
		}
		// 自动代理配置（PAC）会盖过手动代理，开着的话先关掉（备份里记着，恢复时再打开）
		if bk.Services[svc].AutoEnabled {
			_, _ = nsRun("-setautoproxystate", svc, "off")
		}
		return nil
	})
}

func applyServiceProxy(svc string, sp macServiceProxy, ours string) error {
	set := func(setFlag, stateFlag string, p macProxy) error {
		if p.Server != "" && p.Port > 0 && p.Server+":"+itoa(p.Port) != ours {
			if _, err := nsRun(setFlag, svc, p.Server, itoa(p.Port)); err != nil {
				return err
			}
			if !p.Enabled {
				_, err := nsRun(stateFlag, svc, "off")
				return err
			}
			return nil
		}
		_, err := nsRun(stateFlag, svc, "off")
		return err
	}
	if err := set("-setwebproxy", "-setwebproxystate", sp.Web); err != nil {
		return err
	}
	if err := set("-setsecurewebproxy", "-setsecurewebproxystate", sp.Secure); err != nil {
		return err
	}
	if err := set("-setsocksfirewallproxy", "-setsocksfirewallproxystate", sp.Socks); err != nil {
		return err
	}
	if len(sp.Bypass) == 0 {
		_, _ = nsRun("-setproxybypassdomains", svc, "Empty")
	} else {
		_, _ = nsRun(append([]string{"-setproxybypassdomains", svc}, sp.Bypass...)...)
	}
	if sp.AutoEnabled && sp.AutoURL != "" {
		_, _ = nsRun("-setautoproxyurl", svc, sp.AutoURL)
	}
	return nil
}

// restoreSystemProxy 恢复开启前的系统代理设置（没有备份时把所有服务的手动代理都关掉）
func restoreSystemProxy(dataDir string) error {
	svcs, _ := listServices()
	bk := readBackup(dataDir)
	if bk == nil {
		bk = &proxyBackup{Services: map[string]macServiceProxy{}}
	}
	// 备份之后新出现的网络服务（比如插上了 USB 网卡）也被设过本软件的代理，一并关掉
	all := map[string]bool{}
	for s := range bk.Services {
		all[s] = true
	}
	for _, s := range svcs {
		all[s] = true
	}
	list := make([]string, 0, len(all))
	for s := range all {
		list = append(list, s)
	}
	err := forEachService(list, func(svc string) error {
		return applyServiceProxy(svc, bk.Services[svc], bk.Ours)
	})
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

// scutilProxy 当前实际生效的系统代理（scutil --proxy，读取很快，不需要权限）
func scutilProxy() map[string]string {
	out, err := exec.Command("/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, " : "); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// systemProxyURL 当前系统代理（可能是别的代理软件）转成可用的代理地址，没开代理时返回空
func systemProxyURL() (proxyURL, hostPort string) {
	m := scutilProxy()
	for _, k := range []string{"HTTPS", "HTTP"} {
		if m[k+"Enable"] == "1" && m[k+"Proxy"] != "" {
			hp := m[k+"Proxy"] + ":" + m[k+"Port"]
			return "http://" + hp, hp
		}
	}
	if m["SOCKSEnable"] == "1" && m["SOCKSProxy"] != "" {
		hp := m["SOCKSProxy"] + ":" + m["SOCKSPort"]
		return "socks5://" + hp, hp
	}
	return "", ""
}

// systemProxyNow 当前系统代理指向哪里（没开代理时返回空）
func systemProxyNow() string {
	_, hp := systemProxyURL()
	return hp
}

// ======================= 开机自启 =======================
// 用户级 LaunchAgent：登录时启动（--autostart：只在菜单栏运行，不打开控制面板）

const launchAgentLabel = "com.easyclash.desktop"

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func setAutoStart(on bool) error {
	p := launchAgentPath()
	if !on {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(exePath()) + `</string>
		<string>--autostart</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>LimitLoadToSessionType</key>
	<string>Aqua</string>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(plist), 0o644)
}

// ======================= 进程列表（分程序代理用） =======================

func listProcesses() []ProcInfo {
	out, err := exec.Command("/bin/ps", "-axo", "comm=").Output()
	if err != nil {
		return nil
	}
	self := strings.ToLower(filepath.Base(exePath()))
	seen := map[string]*ProcInfo{}
	for _, line := range strings.Split(string(out), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		name := filepath.Base(path)
		key := strings.ToLower(name)
		if key == self || key == "ps" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		sys := !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "/System/") || strings.HasPrefix(path, "/usr/") ||
			strings.HasPrefix(path, "/sbin/") || strings.HasPrefix(path, "/bin/") || strings.HasPrefix(path, "/Library/Apple/") ||
			strings.HasPrefix(path, "/private/") || strings.HasPrefix(path, "/Library/PrivilegedHelperTools/")
		seen[key] = &ProcInfo{Name: name, Path: path, System: sys}
	}
	list := make([]ProcInfo, 0, len(seen))
	for _, p := range seen {
		list = append(list, *p)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].System != list[j].System {
			return !list[i].System
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list
}

// ======================= 单实例 =======================

var instanceLock *os.File

// acquireSingleInstance 返回 false 表示已有一个 EasyClash 在运行（用文件锁，进程退出时系统自动释放）
func acquireSingleInstance() bool {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "EasyClash")
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "instance.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return false
	}
	instanceLock = f
	return true
}

func releaseSingleInstance() {
	if instanceLock != nil {
		_ = syscall.Flock(int(instanceLock.Fd()), syscall.LOCK_UN)
		instanceLock.Close()
		instanceLock = nil
	}
}

// ======================= 杂项 =======================

func openFolder(dir string) { _ = exec.Command("/usr/bin/open", dir).Start() }

// openURL 用默认浏览器打开。数据目录里有 .test-no-browser 时（云端自动测试用）只记日志，不真的打开浏览器。
func openURL(u string) {
	if base, err := os.UserConfigDir(); err == nil {
		if _, err := os.Stat(filepath.Join(base, "EasyClash", ".test-no-browser")); err == nil {
			appendLog("[TEST] open " + strings.SplitN(u, "#", 2)[0])
			return
		}
	}
	_ = exec.Command("/usr/bin/open", u).Start()
}

// messageBox 弹出系统对话框（阻塞到用户点「好」）。文字通过参数传给 AppleScript，不用转义。
func messageBox(title, text string) {
	_ = exec.Command("/usr/bin/osascript",
		"-e", "on run argv",
		"-e", `display dialog (item 1 of argv) with title (item 2 of argv) buttons {"好"} default button 1 with icon note`,
		"-e", "end run",
		text, title).Run()
}

func allowForegroundAny() {}

// ======================= 退出 / 注销时恢复系统代理 =======================
// 正常退出、注销、关机时系统会让程序结束（菜单栏图标的退出回调里已经恢复）；
// 这里再兜住被 kill / 终端 Ctrl+C 等信号结束的情况。

func watchSessionEnd(onEnd func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		<-ch
		onEnd()
		os.Exit(0)
	}()
}
