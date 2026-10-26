package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 界面是一个本地网页（WebView2 窗口里打开），通过这里的接口操作。
// 所有 /api/ 与 /core/ 请求都要带启动时随机生成的令牌，防止本机其他程序乱调。

type Server struct {
	app   *App
	ui    *UI
	token string
	port  int
}

type instanceInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func instanceFile(dataDir string) string { return filepath.Join(dataDir, "instance.json") }

func startServer(app *App, ui *UI, fixedPort int) (*Server, error) {
	addr := "127.0.0.1:0"
	if fixedPort > 0 {
		addr = "127.0.0.1:" + strconv.Itoa(fixedPort)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{app: app, ui: ui, token: randomSecret(), port: ln.Addr().(*net.TCPAddr).Port}
	static, _ := fs.Sub(uiFS, "ui")
	files := http.FileServer(http.FS(static))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/core/", s.handleCore)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
	go func() { _ = (&http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}).Serve(ln) }()
	b, _ := json.Marshal(instanceInfo{Port: s.port, Token: s.token, PID: os.Getpid()})
	_ = os.WriteFile(instanceFile(app.dataDir), b, 0o600)
	return s, nil
}

func (s *Server) URL() string { return "http://127.0.0.1:" + strconv.Itoa(s.port) + "/#" + s.token }

func (s *Server) authorized(r *http.Request) bool {
	t := r.Header.Get("X-EC-Token")
	if t == "" {
		t = r.URL.Query().Get("ectoken")
	}
	return t == s.token
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(400)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func msg(w http.ResponseWriter, m string) { writeJSON(w, map[string]any{"ok": true, "msg": m}) }

type regionJSON struct {
	Code             string `json:"code"`
	Flag             string `json:"flag"`
	DisplayName      string `json:"displayName"`
	GroupName        string `json:"groupName"`
	DisplayGroupName string `json:"displayGroupName"`
	Tiered           bool   `json:"tiered"`
}

func regionsJSON(rs []*Region, tiered map[string]bool) []regionJSON {
	out := []regionJSON{}
	for _, r := range rs {
		out = append(out, regionJSON{r.Code, r.Flag, r.DisplayName(), r.GroupName(), r.DisplayGroupName(), tiered[r.Code]})
	}
	return out
}

func (s *Server) state() map[string]any {
	a := s.app
	a.mu.Lock()
	st := map[string]any{
		"status": a.status, "error": a.lastErr, "mainGroup": a.mainGroup, "regions": regionsJSON(a.regions, a.tiered),
		"sysProxyOn": a.sysProxyOn, "tunOn": a.tunOn, "busy": a.busy > 0, "pendingImport": a.pendingImport,
		"nodeWarn": a.nodeWarn, "nodeChecking": a.nodeChecking,
	}
	sysOn := a.sysProxyOn
	a.mu.Unlock()
	// 连接期间系统代理被别的软件（比如另一个 Clash）改掉了：提醒用户
	if sysOn {
		ours := ourProxyServer(a.store.Settings().MixedPort)
		if now := systemProxyNow(); now != ours {
			if now == "" {
				now = "（已被关闭）"
			}
			st["sysProxyOther"] = now
		}
	}
	crash := ""
	if b, err := os.ReadFile(filepath.Join(a.dataDir, "crash.txt")); err == nil {
		crash = string(b)
		if len(crash) > 4000 {
			crash = crash[:4000]
		}
	}
	st["version"] = appVersion
	st["coreVersion"] = coreVersion()
	st["settings"] = a.store.Settings()
	subs := a.store.Subs()
	if subs == nil {
		subs = []Subscription{}
	}
	st["subs"] = subs
	st["isAdmin"] = isAdmin()
	st["dataDir"] = a.dataDir
	st["crash"] = crash
	st["allRegions"] = regionsJSON(allRegions, nil)
	return st
}

// 改了这些设置不需要重载内核配置
var noReloadKeys = map[string]bool{
	"autoStart": true, "autoConnect": true, "geoAutoUpdate": true, "geoLastCheck": true, "geoLastAttempt": true,
	"lockedRegion": true, "mode": true, "selectedSubId": true, "backupPenaltyMs": true,
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	a := s.app
	body := map[string]any{}
	if r.Method == "POST" {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 60<<20))
		if len(b) > 0 {
			if err := json.Unmarshal(b, &body); err != nil {
				writeErr(w, errors.New("请求格式错误"))
				return
			}
		}
	}
	sv := func(k string) string { return str(body[k]) }

	switch strings.TrimPrefix(r.URL.Path, "/api/") {
	case "state":
		writeJSON(w, s.state())
	case "connect":
		go a.Connect()
		msg(w, "")
	case "disconnect":
		go a.Disconnect()
		msg(w, "")
	case "clear-error":
		a.ClearError()
		msg(w, "")
	case "mode":
		a.SetMode(sv("mode"))
		msg(w, "")
	case "settings":
		// 只改请求里带的字段
		raw, _ := json.Marshal(body)
		var autoStartChanged bool
		before := a.store.Settings()
		after := a.store.Update(func(st *Settings) { _ = json.Unmarshal(raw, st) })
		if before.AutoStart != after.AutoStart {
			autoStartChanged = true
		}
		if autoStartChanged {
			if err := setAutoStart(after.AutoStart); err != nil {
				writeErr(w, errors.New("设置开机自启失败："+err.Error()))
				return
			}
		}
		reload := false
		for k := range body {
			if !noReloadKeys[k] {
				reload = true
			}
		}
		if reload {
			go a.Reload()
		}
		a.changed()
		msg(w, "")
	case "subs/add":
		sub, err := a.AddSub(sv("url"), sv("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		a.mu.Lock()
		a.pendingImport = ""
		a.mu.Unlock()
		msg(w, "已添加「"+sub.Name+"」，共 "+strconv.Itoa(sub.NodeCount)+" 个节点")
	case "subs/import":
		name := strings.TrimSuffix(strings.TrimSuffix(sv("name"), ".yaml"), ".yml")
		if name == "" {
			name = "本地配置"
		}
		sub, err := importSubText(a.store, []byte(sv("text")), name)
		if err != nil {
			writeErr(w, err)
			return
		}
		msg(w, "已导入「"+sub.Name+"」")
	case "subs/refresh":
		sub, err := a.RefreshSub(sv("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		msg(w, "「"+sub.Name+"」已更新，共 "+strconv.Itoa(sub.NodeCount)+" 个节点")
	case "subs/delete":
		a.RemoveSub(sv("id"))
		msg(w, "")
	case "subs/rename":
		a.RenameSub(sv("id"), sv("name"))
		msg(w, "")
	case "subs/select":
		a.SelectSub(sv("id"))
		msg(w, "")
	case "pending-import/clear":
		a.mu.Lock()
		a.pendingImport = ""
		a.mu.Unlock()
		msg(w, "")
	case "select":
		if err := a.SelectProxy(sv("group"), sv("name")); err != nil {
			writeErr(w, err)
			return
		}
		msg(w, "")
	case "region/lock":
		if err := a.LockRegion(sv("code")); err != nil {
			writeErr(w, err)
			return
		}
		msg(w, "")
	case "region/test":
		m, err := a.TestRegion(sv("code"))
		if err != nil {
			writeErr(w, err)
			return
		}
		msg(w, m)
	case "group/test":
		a.TestGroup(sv("name"))
		msg(w, "")
	case "groups/test":
		if list, ok := body["names"].([]any); ok {
			done := make(chan struct{}, len(list))
			for _, n := range list {
				go func(n string) { a.TestGroup(n); done <- struct{}{} }(str(n))
			}
			for range list {
				<-done
			}
		}
		msg(w, "")
	case "logs":
		since, _ := strconv.Atoi(r.URL.Query().Get("since"))
		lines, seq := logsSince(since)
		writeJSON(w, map[string]any{"lines": lines, "seq": seq})
	case "logs/clear":
		clearLogs()
		msg(w, "")
	case "rules-info":
		api := a.API()
		if api == nil {
			writeJSON(w, map[string]any{"connected": false, "geo": a.GeoFiles()})
			return
		}
		provs, _ := api.RuleProviders()
		if provs == nil {
			provs = []RuleProviderInfo{}
		}
		inUse := coreGeoInUse()
		if inUse == nil {
			inUse = []string{}
		}
		writeJSON(w, map[string]any{"connected": true, "ruleCount": api.RuleCount(), "providers": provs, "geoInUse": inUse, "geo": a.GeoFiles()})
	case "rules/update":
		m, err := a.UpdateRuleProviders()
		if err != nil {
			writeErr(w, err)
			return
		}
		msg(w, m)
	case "geo/update":
		m, err := a.UpdateGeoNow()
		if err != nil {
			writeErr(w, err)
			return
		}
		msg(w, m)
	case "processes":
		writeJSON(w, listProcesses())
	case "admin-restart":
		if isAdmin() {
			msg(w, "已经是管理员身份")
			return
		}
		go s.ui.RestartElevated()
		msg(w, "")
	case "sysproxy/reapply":
		if err := a.ReapplySystemProxy(); err != nil {
			writeErr(w, err)
			return
		}
		msg(w, "已重新把系统代理指向本软件")
	case "claim-protocol":
		registerURLProtocol(true)
		msg(w, "已设为 clash:// 一键导入链接的打开方式")
	case "nodes/check":
		a.CheckNodes()
		a.mu.Lock()
		w2 := a.nodeWarn
		a.mu.Unlock()
		if w2 == "" {
			msg(w, "节点检测正常")
		} else {
			msg(w, "节点仍然连不上")
		}
	case "open-log-dir":
		openFolder(logDir(a.dataDir))
		msg(w, "")
	case "open-data-dir":
		openFolder(a.dataDir)
		msg(w, "")
	case "clear-crash":
		_ = os.Remove(filepath.Join(a.dataDir, "crash.txt"))
		msg(w, "")
	case "show":
		s.ui.Show()
		msg(w, "")
	case "import":
		a.mu.Lock()
		a.pendingImport = sv("url")
		a.mu.Unlock()
		s.ui.Show()
		msg(w, "")
	case "quit":
		go s.ui.Quit()
		msg(w, "")
	default:
		http.NotFound(w, r)
	}
}

// handleCore 把 /core/xxx 转给内核的控制接口（带上密钥），界面直接用内核自带的接口查节点、测速、看连接
func (s *Server) handleCore(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "forbidden", 403)
		return
	}
	s.app.mu.Lock()
	addr, secret, status := s.app.ctlAddr, s.app.secret, s.app.status
	s.app.mu.Unlock()
	if status != stConnected || addr == "" {
		http.Error(w, `{"message":"未连接"}`, 503)
		return
	}
	target, _ := url.Parse("http://" + addr)
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = strings.TrimPrefix(req.URL.Path, "/core")
			req.URL.RawPath = ""
			q := req.URL.Query()
			q.Del("ectoken")
			req.URL.RawQuery = q.Encode()
			req.Host = target.Host
			req.Header.Set("Authorization", "Bearer "+secret)
			req.Header.Del("X-EC-Token")
			req.Header.Del("Origin")
		},
	}
	rp.ServeHTTP(w, r)
}
