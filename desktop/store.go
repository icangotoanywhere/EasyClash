package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Subscription 一条机场订阅
type Subscription struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	URL                 string `json:"url"`
	LastUpdate          int64  `json:"lastUpdate"` // 上次成功更新时间（毫秒）
	Upload              int64  `json:"upload"`     // 已用上行，-1 表示机场未提供
	Download            int64  `json:"download"`
	Total               int64  `json:"total"`
	Expire              int64  `json:"expire"`              // 到期时间（秒），-1 未提供
	UpdateIntervalHours int    `json:"updateIntervalHours"` // 自动更新间隔，0 表示不自动更新
	NodeCount           int    `json:"nodeCount"`
}

func newSubscription(id, name, url string) *Subscription {
	return &Subscription{ID: id, Name: name, URL: url, Upload: -1, Download: -1, Total: -1, Expire: -1, UpdateIntervalHours: 24}
}

// Settings 所有可持久化的设置。字段含义与安卓版一致，另加 Windows 专有项。
type Settings struct {
	SelectedSubID string `json:"selectedSubId"`
	Mode          string `json:"mode"` // rule / global / direct
	// 每个程序的走法（键是程序文件名，如 chrome.exe / WeChat）："direct" 仅直连、"proxy" 仅代理；没列出的按规则
	AppRoutes map[string]string `json:"appRoutes"`
	// v1.0.27 及以前的分程序设置，只在升级换算时读取（见 openStore）
	AppProxyMode       string   `json:"appProxyMode,omitempty"`
	AppList            []string `json:"appList,omitempty"`
	AIForceProxy       bool     `json:"aiForceProxy"` // AI 服务的域名一律走代理、绝不直连
	LockedRegion       string   `json:"lockedRegion"` // 锁定的地区代码，如 "SG"，空表示不锁定
	IPv6               bool     `json:"ipv6"`
	BlockQuic          bool     `json:"blockQuic"`
	BingDirect         bool     `json:"bingDirect"`
	DirectFallback     bool     `json:"directFallback"` // 出境直连失败自动转代理
	UseSubscriptionDNS bool     `json:"useSubscriptionDns"`
	TunMode            bool     `json:"tunMode"`  // false = 系统代理（默认）；true = TUN 虚拟网卡（需要管理员）
	TunStack           string   `json:"tunStack"` // gvisor / mixed / system
	LogLevel           string   `json:"logLevel"`
	ExtraRules         string   `json:"extraRules"`
	OverrideYaml       string   `json:"overrideYaml"`
	MixedPort          int      `json:"mixedPort"`
	TestIntervalMin    int      `json:"testIntervalMin"` // 自动测速 / 自动选节点的周期（分钟）
	AutoStart          bool     `json:"autoStart"`       // 开机自动启动
	AutoConnect        bool     `json:"autoConnect"`     // 启动后自动连接
	GeoAutoUpdate      bool     `json:"geoAutoUpdate"`
	GeoLastCheck       int64    `json:"geoLastCheck"`
	GeoLastAttempt     int64    `json:"geoLastAttempt"`
	// 锁定地区时被切到「地区选择」的策略组及其原来的选择（解除锁定时恢复）
	RegionSaved map[string]string `json:"regionSaved"`
	// 锁定期间用户单独指定过的策略组（重新应用锁定时跳过）
	RegionExempt []string `json:"regionExempt"`
}

func defaultSettings() Settings {
	return Settings{
		Mode: "rule", AppRoutes: map[string]string{}, AIForceProxy: true,
		BlockQuic: true, BingDirect: true, DirectFallback: true, TunStack: "gvisor", LogLevel: "info",
		MixedPort: 17890, GeoAutoUpdate: true, TestIntervalMin: 2,
	}
}

// Store 本地持久化：%APPDATA%\EasyClash 下的 settings.json / subs.json，订阅原始 YAML 存在 subs\ 下。
type Store struct {
	dir      string
	mu       sync.Mutex
	settings Settings
	subs     []*Subscription
}

func openStore(dir string) *Store {
	s := &Store{dir: dir, settings: defaultSettings()}
	for _, d := range []string{dir, s.SubsDir(), s.HomeDir(), s.RunDir()} {
		_ = os.MkdirAll(d, 0o755)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &s.settings)
	}
	if s.settings.AppRoutes == nil {
		s.settings.AppRoutes = map[string]string{}
	}
	// 旧版换算（旧设置文件里才有 appProxyMode）：「排除选中的程序」→ 这些程序仅直连；
	// 「仅选中的程序」无法逐个换算（不知道电脑上有哪些程序），改为全部按规则，发布说明里提醒用户重新设置
	if s.settings.AppProxyMode == "deny" {
		for _, a := range s.settings.AppList {
			if a = strings.TrimSpace(a); a != "" {
				s.settings.AppRoutes[a] = "direct"
			}
		}
	}
	s.settings.AppProxyMode, s.settings.AppList = "", nil
	if b, err := os.ReadFile(filepath.Join(dir, "subs.json")); err == nil {
		_ = json.Unmarshal(b, &s.subs)
	}
	return s
}

func (s *Store) SubsDir() string { return filepath.Join(s.dir, "subs") }
func (s *Store) HomeDir() string { return filepath.Join(s.dir, "home") } // 内核工作目录（geo 数据库、缓存、规则集）
func (s *Store) RunDir() string  { return filepath.Join(s.dir, "run") }  // 生成的最终配置
func (s *Store) SubFile(id string) string {
	return filepath.Join(s.SubsDir(), id+".yaml")
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copySettings(src Settings) Settings {
	c := src
	c.AppRoutes = map[string]string{}
	for k, v := range src.AppRoutes {
		c.AppRoutes[k] = v
	}
	c.AppList = nil
	c.RegionExempt = append([]string{}, src.RegionExempt...)
	c.RegionSaved = map[string]string{}
	for k, v := range src.RegionSaved {
		c.RegionSaved[k] = v
	}
	return c
}

// Settings 返回设置的副本
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copySettings(s.settings)
}

// Update 修改设置并保存。用法：store.Update(func(st *Settings){ st.IPv6 = true })
func (s *Store) Update(f func(*Settings)) Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.settings)
	if s.settings.AppRoutes == nil {
		s.settings.AppRoutes = map[string]string{}
	}
	for k, v := range s.settings.AppRoutes { // 只记"仅直连 / 仅代理"，其他值一律当作按规则删掉
		if v != "direct" && v != "proxy" {
			delete(s.settings.AppRoutes, k)
		}
	}
	if s.settings.RegionSaved == nil {
		s.settings.RegionSaved = map[string]string{}
	}
	if s.settings.RegionExempt == nil {
		s.settings.RegionExempt = []string{}
	}
	_ = writeJSONAtomic(filepath.Join(s.dir, "settings.json"), s.settings)
	return copySettings(s.settings)
}

func (s *Store) Subs() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Subscription, 0, len(s.subs))
	for _, x := range s.subs {
		out = append(out, *x)
	}
	return out
}

func (s *Store) saveSubsLocked() { _ = writeJSONAtomic(filepath.Join(s.dir, "subs.json"), s.subs) }

func (s *Store) UpsertSub(sub Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.subs {
		if x.ID == sub.ID {
			c := sub
			s.subs[i] = &c
			s.saveSubsLocked()
			return
		}
	}
	c := sub
	s.subs = append(s.subs, &c)
	s.saveSubsLocked()
}

func (s *Store) GetSub(id string) (Subscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.subs {
		if x.ID == id {
			return *x, true
		}
	}
	return Subscription{}, false
}

func (s *Store) RemoveSub(id string) {
	s.mu.Lock()
	var keep []*Subscription
	for _, x := range s.subs {
		if x.ID != id {
			keep = append(keep, x)
		}
	}
	s.subs = keep
	s.saveSubsLocked()
	sel := s.settings.SelectedSubID
	first := ""
	if len(keep) > 0 {
		first = keep[0].ID
	}
	s.mu.Unlock()
	_ = os.Remove(s.SubFile(id))
	if sel == id {
		s.Update(func(st *Settings) { st.SelectedSubID = first })
	}
}

func (s *Store) SelectedSub() (Subscription, bool) {
	return s.GetSub(s.Settings().SelectedSubID)
}

func (s *Store) RawYaml(id string) ([]byte, error) { return os.ReadFile(s.SubFile(id)) }
