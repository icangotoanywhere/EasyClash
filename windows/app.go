package main

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	stIdle       = "idle"
	stConnecting = "connecting"
	stConnected  = "connected"
	stStopping   = "stopping"

	geoCheckInterval = 7 * 24 * time.Hour // 地理数据库每 7 天检查一次
	geoRetryInterval = 12 * time.Hour     // 失败后 12 小时内不再自动重试
)

// App 连接生命周期与所有用户操作（相当于安卓版的 VpnService + MainViewModel）。
type App struct {
	store      *Store
	dataDir    string
	noSysProxy bool // 测试用：不改系统代理

	opMu sync.Mutex // 连接 / 重载 / 断开 串行执行，避免互相打架

	mu            sync.Mutex
	status        string
	lastErr       string
	api           *CoreAPI
	ctlAddr       string
	secret        string
	mainGroup     string
	regions       []*Region
	tiered        map[string]bool // 分了优先/备用两级的地区
	sysProxyOn    bool
	tunOn         bool
	busy          int
	pendingImport string
	nodeWarn      string // 节点全部连不上时的提示（首页显示）
	nodeChecking  bool
	maintCancel   context.CancelFunc
	onChange      func() // 状态变化时通知托盘刷新
}

func newApp(store *Store, dataDir string, noSysProxy bool) *App {
	return &App{store: store, dataDir: dataDir, noSysProxy: noSysProxy, status: stIdle}
}

func (a *App) changed() {
	a.mu.Lock()
	f := a.onChange
	a.mu.Unlock()
	if f != nil {
		go f()
	}
}

func (a *App) setStatus(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	a.changed()
}

func (a *App) Status() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

func (a *App) setError(msg string) {
	a.mu.Lock()
	a.lastErr = msg
	a.mu.Unlock()
}

func (a *App) ClearError() { a.setError("") }

func (a *App) API() *CoreAPI {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.status != stConnected {
		return nil
	}
	return a.api
}

func (a *App) runtime() (mainGroup string, regions []*Region) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.mainGroup, append([]*Region{}, a.regions...)
}

func (a *App) beginBusy() { a.mu.Lock(); a.busy++; a.mu.Unlock() }
func (a *App) endBusy()   { a.mu.Lock(); a.busy--; a.mu.Unlock() }

// subRoutes 下载订阅时依次尝试的线路：已连接时先走本软件的代理；系统里开着别的代理软件时走它；最后直连。
// 只有直连一条线路时再重试一次，偶发的连接超时不至于直接失败。
func (a *App) subRoutes() []downloadRoute {
	st := a.store.Settings()
	ours := ""
	if st.MixedPort > 0 {
		ours = ourProxyServer(st.MixedPort)
	}
	var rs []downloadRoute
	if a.Status() == stConnected && ours != "" {
		rs = append(rs, downloadRoute{Label: "本软件的代理", Proxy: "http://" + ours})
	}
	if pu, hp := systemProxyURL(); pu != "" && hp != ours {
		rs = append(rs, downloadRoute{Label: "系统代理 " + hp, Proxy: pu})
	}
	rs = append(rs, downloadRoute{Label: "直连"})
	if len(rs) == 1 {
		rs = append(rs, downloadRoute{Label: "直连（重试）"})
	}
	return rs
}

// ---------------- 节点可用性检查 ----------------
// 连接后测一下实际在用的节点：全部连不上时首页醒目提示，免得走代理的程序（比如 Claude）打不开还不知道原因。

const nodeWarnTail = "走代理的网站和程序（包括 Claude）会打不开。可能是这台电脑所在的网络屏蔽了这类节点，或者机场暂时故障。可以换个地区或换个订阅试试，或者先断开（断开后系统代理会恢复原样）。"

// CheckNodes 检查当前在用的节点，结果放进首页提示
func (a *App) CheckNodes() {
	api := a.API()
	if api == nil {
		return
	}
	a.mu.Lock()
	if a.nodeChecking {
		a.mu.Unlock()
		return
	}
	a.nodeChecking = true
	a.mu.Unlock()
	a.changed()
	warn := a.nodeCheck(api)
	if warn != "" {
		// 刚连接时第一次测速偶尔会因为 DNS 冷启动等原因超时，隔两秒再确认一次，避免误报
		time.Sleep(2 * time.Second)
		if a.API() == api {
			warn = a.nodeCheck(api)
		}
	}
	a.mu.Lock()
	a.nodeChecking = false
	if a.status == stConnected {
		a.nodeWarn = warn
	}
	a.mu.Unlock()
	if warn != "" {
		appendLog("[APP] 节点检查：" + warn)
	} else {
		appendLog("[APP] 节点检查：正常")
	}
	a.changed()
}

func (a *App) nodeCheck(api *CoreAPI) string {
	_, regions := a.runtime()
	st := a.store.Settings()
	if st.Mode == "direct" {
		return "" // 直连模式不走节点
	}
	if st.LockedRegion != "" && st.Mode != "global" {
		for _, r := range regions {
			if r.Code == st.LockedRegion {
				if a.isTiered(r.Code) {
					// 分级地区：两级都测，顺带按"优先比备用慢多少"选好该用的那一级
					res := a.tierPick(api, r)
					appendLog("[APP] 分级选择 " + res.describe(r, st.BackupPenaltyMs))
					if res.PrimaryMin <= 0 && res.BackupMin <= 0 {
						return "锁定的「" + r.Flag + " " + r.DisplayName() + "」节点全部连不上（测速超时）。" + nodeWarnTail
					}
					return ""
				}
				// 测整个地区组：内核只返回测通了的节点，一个都没有就是全部连不上（顺带切到该地区最快的节点）
				if res := api.GroupDelay(r.GroupName()); len(res) == 0 {
					return "锁定的「" + r.Flag + " " + r.DisplayName() + "」节点全部连不上（测速超时）。" + nodeWarnTail
				}
				return ""
			}
		}
	}
	px, err := api.Proxies()
	if err != nil {
		return ""
	}
	// 没锁定地区：测各个手动组最终落到的真实节点
	leaves := map[string]bool{}
	for name, g := range px {
		// 全局模式只看 GLOBAL 组；规则模式看各个手动组
		if (st.Mode == "global") != (name == "GLOBAL") {
			continue
		}
		if isSelectorItem(g) && leadsToProxy(px, name) {
			leaves[resolveLeaf(px, name)] = true
		}
	}
	if len(leaves) == 0 {
		return ""
	}
	var names []string
	for n := range leaves {
		names = append(names, n)
		if len(names) >= 8 {
			break
		}
	}
	ok := make(chan bool, len(names))
	for _, n := range names {
		go func(n string) { ok <- api.Delay(n) > 0 }(n)
	}
	alive := 0
	for range names {
		if <-ok {
			alive++
		}
	}
	if alive == 0 {
		return fmt.Sprintf("当前在用的节点（%d 个）全部连不上（测速超时）。", len(names)) + nodeWarnTail
	}
	return ""
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 16090
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func randomSecret() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = chars[int(b[i])%len(chars)]
	}
	return string(b)
}

// extractGeoFiles 把打包进 exe 的 geo 数据库释放到内核工作目录（只释放一次）
func extractGeoFiles(home string) {
	for _, name := range []string{"geoip.metadb", "geosite.dat", "GeoLite2-ASN.mmdb"} {
		dst := filepath.Join(home, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		f, err := geoAssets.Open("assets/" + name + ".gz")
		if err != nil {
			continue
		}
		zr, err := gzip.NewReader(f)
		if err == nil {
			if out, err := os.Create(dst + ".tmp"); err == nil {
				_, cerr := io.Copy(out, zr)
				out.Close()
				if cerr == nil {
					_ = os.Rename(dst+".tmp", dst)
				} else {
					_ = os.Remove(dst + ".tmp")
				}
			}
			zr.Close()
		}
		f.Close()
	}
}

func friendly(err error) string {
	m := err.Error()
	switch {
	case strings.Contains(strings.ToLower(m), "address already in use") || strings.Contains(m, "Only one usage of each socket address"):
		return "端口被占用，请关闭其他代理软件，或到「高级」页换一个本机代理端口后重试"
	case strings.Contains(strings.ToLower(m), "proxy") && strings.Contains(strings.ToLower(m), "not found"):
		return "订阅里的策略组引用了不存在的节点：" + m
	}
	return m
}

// ---------------- 连接 ----------------

func (a *App) Connect() {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if s := a.Status(); s == stConnected || s == stConnecting {
		return
	}
	a.ClearError()
	a.setStatus(stConnecting)
	if err := a.doConnect(); err != nil {
		appendLog("[APP] 启动失败：" + err.Error())
		a.teardown("启动失败")
		a.setError(friendly(err))
		a.setStatus(stIdle)
	}
}

func (a *App) doConnect() error {
	st := a.store.Settings()
	sub, ok := a.store.SelectedSub()
	if !ok {
		return errors.New("还没有选择订阅，请先在「订阅」页添加一个机场订阅")
	}
	yamlText, err := a.store.RawYaml(sub.ID)
	if err != nil {
		return errors.New("订阅文件不存在，请先更新一次订阅")
	}
	extractGeoFiles(a.store.HomeDir())

	tun := st.TunMode
	if tun && !isAdmin() {
		return errors.New("TUN 模式需要管理员权限：请到「高级」页点「以管理员身份重新启动」，或关掉 TUN 模式改用系统代理")
	}
	if !tun && st.MixedPort <= 0 {
		return errors.New("系统代理模式需要本机代理端口，请到「高级」页填一个端口（默认 17890）")
	}
	ctl := "127.0.0.1:" + strconv.Itoa(freePort())
	secret := randomSecret()
	res, err := buildConfig(yamlText, st, a.store.RunDir(), ctl, secret, tun)
	if err != nil {
		return err
	}
	mode := "系统代理"
	if tun {
		mode = "TUN"
	}
	appendLog(fmt.Sprintf("[APP] 接管方式=%s 端口=%d 主组=%s 地区组=%d", mode, st.MixedPort, res.MainGroup, len(res.Regions)))

	if err := coreStart(a.store.HomeDir(), res.Path); err != nil {
		return err
	}
	api := newCoreAPI(ctl, secret)
	ok = false
	for i := 0; i < 50 && !ok; i++ {
		if ok = api.Ping(); !ok {
			time.Sleep(120 * time.Millisecond)
		}
	}
	if !ok {
		return errors.New("内核已启动但控制接口无响应")
	}
	// 端口被别的软件占用时内核只打日志不报错，这里主动检查一下
	if st.MixedPort > 0 {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(st.MixedPort), 2*time.Second)
		if err != nil {
			return fmt.Errorf("本机代理端口 %d 没能打开（可能被其他软件占用），请到「高级」页换一个端口", st.MixedPort)
		}
		c.Close()
	}
	appendLog("[APP] 内核已就绪，版本 " + coreVersion())

	a.mu.Lock()
	a.api, a.ctlAddr, a.secret = api, ctl, secret
	a.mainGroup, a.regions, a.tiered = res.MainGroup, res.Regions, res.Tiered
	a.tunOn = tun
	a.mu.Unlock()
	a.applyRegionLock(api, res.MainGroup)

	if !tun && !a.noSysProxy {
		if err := enableSystemProxy(a.dataDir, st.MixedPort); err != nil {
			return err
		}
		a.mu.Lock()
		a.sysProxyOn = true
		a.mu.Unlock()
		appendLog("[APP] 已把系统代理指向 127.0.0.1:" + strconv.Itoa(st.MixedPort))
	}
	a.setStatus(stConnected)
	a.startMaintenance(api, true)
	return nil
}

// ---------------- 锁定地区 ----------------
// 让所有走代理的流量都从选中的地区出去：
//   - 先把「🌍 地区选择」指到该地区的自动测速组；
//   - ★ 主组（MATCH 规则指向的组）一律切到「🌍 地区选择」；
//   - 其他手动选择组，当前选的是具体节点或自动测速 / 故障转移组时，也切过去；
//   - 当前选的是另一个手动选择组（如「🚀 节点选择」）的不动——它跟着那个组走；
//   - 当前选的是直连 / 拒绝的不动（国内网站、广告拦截照旧）。
// 切之前记下原来的选择，解除锁定时恢复；锁定期间用户在某个组里手动选了别的，就尊重这个选择（记为例外）。

var nonProxyTypes = map[string]bool{"direct": true, "reject": true, "rejectdrop": true, "pass": true, "compatible": true}

func isGroupItem(p ProxyItem) bool { return len(p.All) > 0 }
func isSelectorItem(p ProxyItem) bool { return strings.EqualFold(p.Type, "Selector") }

// leadsToProxy 沿着策略组走到底，最终是不是一个真正的代理节点
func leadsToProxy(px map[string]ProxyItem, start string) bool {
	cur := start
	for i := 0; i < 8; i++ {
		p, ok := px[cur]
		if !ok {
			return false
		}
		if !isGroupItem(p) {
			return !nonProxyTypes[strings.ToLower(p.Type)]
		}
		if p.Now == "" {
			return false
		}
		cur = p.Now
	}
	return false
}

// lockTargets 锁定时需要切到「地区选择」的组
func lockTargets(px map[string]ProxyItem, mainGroup string, exempt map[string]bool) []string {
	var out []string
	for name, g := range px {
		if !isSelectorItem(g) || name == regionSelector || name == "GLOBAL" || g.Now == regionSelector || exempt[name] {
			continue
		}
		has := false
		for _, x := range g.All {
			if x == regionSelector {
				has = true
			}
		}
		if !has {
			continue
		}
		if name == mainGroup {
			out = append(out, name)
			continue
		}
		t, ok := px[g.Now]
		if !ok {
			continue
		}
		switch {
		case !isGroupItem(t): // 具体节点
			if !nonProxyTypes[strings.ToLower(t.Type)] {
				out = append(out, name)
			}
		case isSelectorItem(t): // 另一个手动组：跟着它走
		default: // 自动测速 / 故障转移 / 负载均衡组
			if leadsToProxy(px, t.Name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// lockApply 锁定（或重新应用锁定）到某个地区
func (a *App) lockApply(api *CoreAPI, mainGroup string, region *Region) error {
	if err := api.Select(regionSelector, region.GroupName()); err != nil {
		return errors.New("切换失败：" + err.Error())
	}
	px, err := api.Proxies()
	if err != nil {
		return err
	}
	st := a.store.Settings()
	exempt := map[string]bool{}
	for _, g := range st.RegionExempt {
		exempt[g] = true
	}
	saved := map[string]string{}
	for k, v := range st.RegionSaved {
		saved[k] = v
	}
	for _, g := range lockTargets(px, mainGroup, exempt) {
		if _, ok := saved[g]; !ok {
			saved[g] = px[g].Now
		}
		_ = api.Select(g, regionSelector)
	}
	a.store.Update(func(s *Settings) { s.RegionSaved = saved })
	return nil
}

// lockRelease 解除锁定：把锁定时切过去的组恢复原来的选择（skip 是用户刚手动改过、不用恢复的组）
func (a *App) lockRelease(api *CoreAPI, mainGroup, skip string) {
	px, _ := api.Proxies()
	st := a.store.Settings()
	restore := func(g, prev string) {
		grp, ok := px[g]
		if !ok || grp.Now != regionSelector {
			return
		}
		target := ""
		for _, x := range grp.All {
			if x == prev && x != regionSelector {
				target = x
			}
		}
		if target == "" {
			for _, x := range grp.All {
				if x != regionSelector {
					target = x
					break
				}
			}
		}
		if target != "" {
			_ = api.Select(g, target)
		}
	}
	for g, prev := range st.RegionSaved {
		if g != skip {
			restore(g, prev)
		}
	}
	// 没有记录的主组（旧版本锁定的）退回第一个非地区选择的条目
	if _, ok := st.RegionSaved[mainGroup]; !ok && mainGroup != skip {
		restore(mainGroup, "")
	}
	a.store.Update(func(s *Settings) { s.RegionSaved = map[string]string{}; s.RegionExempt = []string{} })
}

// applyRegionLock 连接 / 重载后调用：已锁定就重新应用；已解除但还有没恢复的组（断开时解除的）就恢复
func (a *App) applyRegionLock(api *CoreAPI, mainGroup string) {
	st := a.store.Settings()
	if st.LockedRegion == "" {
		if len(st.RegionSaved) > 0 {
			a.lockRelease(api, mainGroup, "")
		}
		return
	}
	_, regions := a.runtime()
	for _, r := range regions {
		if r.Code == st.LockedRegion {
			_ = a.lockApply(api, mainGroup, r)
			return
		}
	}
}

// teardown 停内核、恢复系统代理、清空运行状态
func (a *App) teardown(reason string) {
	appendLog("[APP] 断开：" + reason)
	a.mu.Lock()
	if a.maintCancel != nil {
		a.maintCancel()
		a.maintCancel = nil
	}
	sys := a.sysProxyOn
	a.mu.Unlock()
	if sys {
		if err := restoreSystemProxy(a.dataDir); err != nil {
			appendLog("[APP] 恢复系统代理失败：" + err.Error())
		} else {
			appendLog("[APP] 已恢复原来的系统代理设置")
		}
	}
	coreStop()
	a.mu.Lock()
	a.api, a.ctlAddr, a.secret, a.mainGroup, a.regions, a.tiered = nil, "", "", "", nil, nil
	a.sysProxyOn, a.tunOn = false, false
	a.nodeWarn, a.nodeChecking = "", false
	a.mu.Unlock()
}

func (a *App) Disconnect() {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if a.Status() == stIdle {
		return
	}
	a.setStatus(stStopping)
	a.teardown("用户断开")
	a.setStatus(stIdle)
}

// Shutdown 退出程序前调用（托盘退出、关机注销）
func (a *App) Shutdown() {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if a.Status() != stIdle {
		a.teardown("退出程序")
		a.setStatus(stIdle)
	}
	// 万一状态不同步，再兜底恢复一次
	if !a.noSysProxy {
		recoverSystemProxy(a.dataDir)
	}
}

// Reload 设置或订阅变了：已连接时不断开直接换配置
func (a *App) Reload() {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if a.Status() != stConnected {
		return
	}
	if err := a.doReload(); err != nil {
		a.setError("重载配置失败：" + friendly(err))
		appendLog("[APP] 重载失败：" + err.Error())
	}
	a.changed()
}

func (a *App) doReload() error {
	st := a.store.Settings()
	sub, ok := a.store.SelectedSub()
	if !ok {
		return errors.New("没有选中的订阅")
	}
	yamlText, err := a.store.RawYaml(sub.ID)
	if err != nil {
		return errors.New("订阅文件不存在，请先更新一次订阅")
	}
	tun := st.TunMode
	if tun && !isAdmin() {
		tun = false
		a.setError("TUN 模式需要管理员权限，暂时仍用系统代理：请到「高级」页点「以管理员身份重新启动」")
	}
	a.mu.Lock()
	ctl, secret, api := a.ctlAddr, a.secret, a.api
	wasSys := a.sysProxyOn
	a.mu.Unlock()
	res, err := buildConfig(yamlText, st, a.store.RunDir(), ctl, secret, tun)
	if err != nil {
		return err
	}
	if err := coreReload(res.Path); err != nil {
		return err
	}
	a.mu.Lock()
	a.mainGroup, a.regions, a.tiered, a.tunOn = res.MainGroup, res.Regions, res.Tiered, tun
	a.mu.Unlock()
	a.applyRegionLock(api, res.MainGroup)

	// 接管方式 / 端口变化后同步系统代理
	wantSys := !tun && !a.noSysProxy && st.MixedPort > 0
	if wantSys {
		if err := enableSystemProxy(a.dataDir, st.MixedPort); err != nil {
			return err
		}
	} else if wasSys {
		_ = restoreSystemProxy(a.dataDir)
	}
	a.mu.Lock()
	a.sysProxyOn = wantSys
	a.mu.Unlock()
	a.startMaintenance(api, false)
	return nil
}

// ReapplySystemProxy 系统代理被别的软件改掉后，重新指向本软件
func (a *App) ReapplySystemProxy() error {
	a.mu.Lock()
	on := a.sysProxyOn
	a.mu.Unlock()
	if !on {
		return errors.New("当前没有使用系统代理")
	}
	return enableSystemProxy(a.dataDir, a.store.Settings().MixedPort)
}

// ---------------- 后台维护 ----------------

func (a *App) startMaintenance(api *CoreAPI, checkGeo bool) {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.maintCancel != nil {
		a.maintCancel()
	}
	a.maintCancel = cancel
	a.mu.Unlock()
	go func() {
		sleep := func(d time.Duration) bool {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(d):
				return true
			}
		}
		go a.tierLoop(api, sleep)
		// 0. 节点可用性检查（连接 / 重载后 3 秒）
		if !sleep(3 * time.Second) {
			return
		}
		a.CheckNodes()
		// 1. 规则集补下载：启动瞬间节点可能还没就绪，首次下载失败的规则集要等订阅设定的间隔（通常一天）才会重试
		if !sleep(5 * time.Second) {
			return
		}
		if list, err := api.RuleProviders(); err == nil {
			var failed []RuleProviderInfo
			for _, p := range list {
				if strings.EqualFold(p.Vehicle, "HTTP") && p.NeverLoaded {
					failed = append(failed, p)
				}
			}
			if len(failed) > 0 {
				appendLog(fmt.Sprintf("[APP] %d 个规则集首次下载失败，重试中…", len(failed)))
				ok := 0
				for _, p := range failed {
					if api.UpdateRuleProvider(p.Name) == nil {
						ok++
					}
				}
				appendLog(fmt.Sprintf("[APP] 规则集重试完成：成功 %d / %d", ok, len(failed)))
			}
		}
		// 2. 地理数据库自动更新
		if checkGeo {
			st := a.store.Settings()
			now := time.Now()
			if st.GeoAutoUpdate && now.Sub(time.UnixMilli(st.GeoLastCheck)) > geoCheckInterval && now.Sub(time.UnixMilli(st.GeoLastAttempt)) > geoRetryInterval {
				a.store.Update(func(s *Settings) { s.GeoLastAttempt = now.UnixMilli() })
				appendLog("[APP] 地理数据库超过 7 天未检查，后台更新中…")
				if err := coreUpdateGeo(); err == nil {
					a.store.Update(func(s *Settings) { s.GeoLastCheck = time.Now().UnixMilli() })
					appendLog("[APP] 地理数据库检查完成，已是最新")
				} else {
					appendLog("[APP] 地理数据库更新失败：" + err.Error() + "（12 小时后再试）")
				}
			}
		}
		// 3. 订阅自动更新（按机场给的间隔，默认 24 小时；只更新正在用的订阅）
		for sleep(30 * time.Minute) {
			sub, ok := a.store.SelectedSub()
			if !ok || sub.URL == "" || sub.UpdateIntervalHours <= 0 {
				continue
			}
			if time.Since(time.UnixMilli(sub.LastUpdate)) < time.Duration(sub.UpdateIntervalHours)*time.Hour {
				continue
			}
			if err := downloadSub(a.store, &sub, a.subRoutes()); err == nil {
				a.store.UpsertSub(sub)
				appendLog("[APP] 订阅「" + sub.Name + "」已自动更新")
				go a.Reload()
			} else {
				appendLog("[APP] 订阅自动更新失败：" + err.Error())
			}
		}
	}()
}

// ---------------- 用户操作：模式 / 节点 / 地区 ----------------

func (a *App) SetMode(mode string) {
	a.store.Update(func(s *Settings) { s.Mode = mode })
	if api := a.API(); api != nil {
		_ = api.SetMode(mode)
		go a.CheckNodes()
	}
	a.changed()
}

func (a *App) SelectProxy(group, name string) error {
	api := a.API()
	if api == nil {
		return errors.New("未连接")
	}
	if err := api.Select(group, name); err != nil {
		return errors.New("切换失败：" + err.Error())
	}
	main, _ := a.runtime()
	defer func() { go a.CheckNodes() }() // 切换（以及可能的解除锁定）完成后再检查
	if a.store.Settings().LockedRegion == "" || group == regionSelector {
		return nil
	}
	switch {
	case group == main && name != regionSelector:
		// 用户手动改了主组，说明不想再锁地区：其他组恢复锁定前的选择
		a.lockRelease(api, main, group)
		a.store.Update(func(s *Settings) { s.LockedRegion = "" })
	case name != regionSelector:
		// 锁定期间单独给某个组指定了别的：尊重这个选择，重新应用锁定时跳过它
		a.store.Update(func(s *Settings) {
			delete(s.RegionSaved, group)
			for _, g := range s.RegionExempt {
				if g == group {
					return
				}
			}
			s.RegionExempt = append(s.RegionExempt, group)
		})
	default:
		a.store.Update(func(s *Settings) {
			var keep []string
			for _, g := range s.RegionExempt {
				if g != group {
					keep = append(keep, g)
				}
			}
			s.RegionExempt = keep
		})
	}
	return nil
}

// LockRegion 锁定地区：所有走代理的流量都从该地区出去。code 为空表示解除锁定
func (a *App) LockRegion(code string) error {
	api := a.API()
	main, regions := a.runtime()
	if code == "" {
		a.store.Update(func(s *Settings) { s.LockedRegion = "" })
		if api != nil {
			a.lockRelease(api, main, "")
		}
		// 未连接时保留记录，下次连接时再恢复
		return nil
	}
	a.store.Update(func(s *Settings) { s.LockedRegion = code })
	if api == nil {
		return nil
	}
	for _, r := range regions {
		if r.Code == code {
			err := a.lockApply(api, main, r)
			go a.CheckNodes()
			return err
		}
	}
	if reg := regionOf(code); reg != nil {
		return errors.New("当前订阅里没有" + reg.DisplayName() + "的节点")
	}
	return nil
}

// TestRegion 只测某个地区的自动组：内核会重测该组所有节点并按结果重新挑最快的
func (a *App) TestRegion(code string) (string, error) {
	api := a.API()
	if api == nil {
		return "", errors.New("未连接")
	}
	_, regions := a.runtime()
	var region *Region
	for _, r := range regions {
		if r.Code == code {
			region = r
		}
	}
	if region == nil {
		return "", errors.New("当前订阅里没有这个地区")
	}
	a.beginBusy()
	defer a.endBusy()
	before := ""
	if px, err := api.Proxies(); err == nil {
		before = resolveLeaf(px, region.GroupName())
	}
	if a.isTiered(code) {
		// 分级地区：两级都测，并按"优先比备用慢多少"决定用哪一级
		res := a.tierPick(api, region)
		appendLog("[APP] 手动测速 " + res.describe(region, a.store.Settings().BackupPenaltyMs))
	} else {
		api.GroupDelay(region.GroupName())
	}
	// 内核对自动组的"最快"结果有几秒缓存，稍等再取最终选择
	time.Sleep(1500 * time.Millisecond)
	after := ""
	if px, err := api.Proxies(); err == nil {
		after = resolveLeaf(px, region.GroupName())
	}
	label := region.Flag + " " + region.DisplayName()
	switch {
	case after == "":
		return label + " 测速完成", nil
	case before != "" && before != after:
		return label + "：已从「" + before + "」切换到「" + after + "」", nil
	default:
		return label + "：仍是最快节点「" + after + "」", nil
	}
}

func (a *App) TestGroup(name string) {
	if api := a.API(); api != nil {
		a.beginBusy()
		api.GroupDelay(name)
		a.endBusy()
	}
}

// ---------------- 用户操作：订阅 ----------------

func (a *App) AddSub(rawURL, name string) (*Subscription, error) {
	u, err := normalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = "订阅"
	}
	a.beginBusy()
	defer a.endBusy()
	sub := newSubscription(newID(), strings.TrimSpace(name), u)
	if err := downloadSub(a.store, sub, a.subRoutes()); err != nil {
		return nil, err
	}
	a.store.UpsertSub(*sub)
	if a.store.Settings().SelectedSubID == "" {
		a.store.Update(func(s *Settings) { s.SelectedSubID = sub.ID })
	}
	return sub, nil
}

func (a *App) RefreshSub(id string) (*Subscription, error) {
	sub, ok := a.store.GetSub(id)
	if !ok {
		return nil, errors.New("订阅不存在")
	}
	a.beginBusy()
	defer a.endBusy()
	if err := downloadSub(a.store, &sub, a.subRoutes()); err != nil {
		return nil, err
	}
	a.store.UpsertSub(sub)
	if id == a.store.Settings().SelectedSubID {
		go a.Reload()
	}
	return &sub, nil
}

func (a *App) RemoveSub(id string) {
	wasSelected := id == a.store.Settings().SelectedSubID
	a.store.RemoveSub(id)
	if wasSelected {
		go a.Reload()
	}
}

func (a *App) SelectSub(id string) {
	a.store.Update(func(s *Settings) { s.SelectedSubID = id })
	go a.Reload()
}

func (a *App) RenameSub(id, name string) {
	if sub, ok := a.store.GetSub(id); ok && strings.TrimSpace(name) != "" {
		sub.Name = strings.TrimSpace(name)
		a.store.UpsertSub(sub)
	}
}

// ---------------- 用户操作：规则与地理数据库 ----------------

func (a *App) UpdateRuleProviders() (string, error) {
	api := a.API()
	if api == nil {
		return "", errors.New("未连接")
	}
	list, _ := api.RuleProviders()
	var httpList []RuleProviderInfo
	for _, p := range list {
		if strings.EqualFold(p.Vehicle, "HTTP") {
			httpList = append(httpList, p)
		}
	}
	if len(httpList) == 0 {
		return "订阅没有引用在线规则集", nil
	}
	a.beginBusy()
	defer a.endBusy()
	ok := 0
	for _, p := range httpList {
		if api.UpdateRuleProvider(p.Name) == nil {
			ok++
		}
	}
	if ok == len(httpList) {
		return fmt.Sprintf("规则集已全部更新（%d 个）", ok), nil
	}
	return fmt.Sprintf("规则集更新：成功 %d 个，失败 %d 个，原因见「诊断 → 查看日志」", ok, len(httpList)-ok), nil
}

func (a *App) UpdateGeoNow() (string, error) {
	if a.Status() != stConnected {
		return "", errors.New("请先连接")
	}
	a.store.Update(func(s *Settings) { s.GeoLastAttempt = time.Now().UnixMilli() })
	a.beginBusy()
	defer a.endBusy()
	if err := coreUpdateGeo(); err != nil {
		return "", errors.New("数据库更新失败：" + err.Error())
	}
	a.store.Update(func(s *Settings) { s.GeoLastCheck = time.Now().UnixMilli() })
	return "地理数据库已是最新", nil
}

type geoFileInfo struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Date  string `json:"date"`
}

func (a *App) GeoFiles() []geoFileInfo {
	items := [][3]string{
		{"geoip", "GeoIP（IP 归属国家）", "geoip.metadb"},
		{"geosite", "GeoSite（网站分类）", "geosite.dat"},
		{"asn", "ASN（IP 所属运营商）", "GeoLite2-ASN.mmdb"},
	}
	var out []geoFileInfo
	for _, it := range items {
		d := ""
		if fi, err := os.Stat(filepath.Join(a.store.HomeDir(), it[2])); err == nil {
			d = fi.ModTime().Format("2006-01-02")
		}
		out = append(out, geoFileInfo{Key: it[0], Label: it[1], Date: d})
	}
	return out
}
