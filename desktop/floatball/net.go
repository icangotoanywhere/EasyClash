//go:build windows

package floatball

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type NetInfo struct {
	IP          string
	Country     string
	CountryCode string
	Region      string
	City        string
	ISP         string
	AS          string
	Provider    string
}

type CheckResult struct {
	Info    NetInfo
	OK      bool
	Latency time.Duration
	Err     string
	Via     string // 直连 / 代理地址
	Primary bool   // first provider succeeded
}

const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) EasyClash-FloatBall/1.0"

// ---------- system proxy (WinINET settings in HKCU) ----------

const inetKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func regOpen(path string, access uint32) (syscall.Handle, error) {
	var k syscall.Handle
	err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, u16(path), 0, access, &k)
	return k, err
}

func regDWORD(k syscall.Handle, name string) (uint32, bool) {
	var typ, v uint32
	n := uint32(4)
	if syscall.RegQueryValueEx(k, u16(name), nil, &typ, (*byte)(unsafe.Pointer(&v)), &n) != nil {
		return 0, false
	}
	return v, true
}

func regString(k syscall.Handle, name string) (string, bool) {
	var typ, n uint32
	if syscall.RegQueryValueEx(k, u16(name), nil, &typ, nil, &n) != nil || n == 0 {
		return "", false
	}
	buf := make([]uint16, n/2+1)
	if syscall.RegQueryValueEx(k, u16(name), nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &n) != nil {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

// systemProxy returns a proxy URL string, or "" for direct.
func systemProxy() string {
	k, err := regOpen(inetKey, syscall.KEY_READ)
	if err != nil {
		return ""
	}
	defer syscall.RegCloseKey(k)
	if en, ok := regDWORD(k, "ProxyEnable"); !ok || en == 0 {
		return ""
	}
	s, ok := regString(k, "ProxyServer")
	if !ok {
		return ""
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "=") {
		return normalizeProxy(s)
	}
	m := map[string]string{}
	for _, part := range strings.Split(s, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && kv[1] != "" {
			m[strings.ToLower(kv[0])] = kv[1]
		}
	}
	for _, key := range []string{"https", "http"} {
		if v, ok := m[key]; ok {
			return normalizeProxy(v)
		}
	}
	if v, ok := m["socks"]; ok {
		if !strings.Contains(v, "://") {
			v = "socks5://" + v
		}
		return v
	}
	return ""
}

func normalizeProxy(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return s
}

func makeClient(useSys bool, custom string) (*http.Client, string) {
	tr := &http.Transport{
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 6 * time.Second,
		DialContext:         (&net.Dialer{Timeout: 6 * time.Second}).DialContext,
	}
	via := "直连"
	p := normalizeProxy(custom)
	if p == "" && useSys {
		p = systemProxy()
	}
	if p != "" {
		if u, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(u)
			via = "代理 " + u.Host
		}
	}
	return &http.Client{Transport: tr, Timeout: 8 * time.Second}, via
}

func getJSON(c *http.Client, u string, v any) error {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(body, v)
}

// ---------- providers ----------

func provIPAPI(c *http.Client) (NetInfo, error) {
	var r struct {
		Status, Message, Country, CountryCode, RegionName, City, Isp, Org, As, Query string
	}
	err := getJSON(c, "http://ip-api.com/json/?lang=zh-CN&fields=status,message,country,countryCode,regionName,city,isp,org,as,query", &r)
	if err != nil {
		return NetInfo{}, err
	}
	if r.Status != "success" || r.Query == "" {
		return NetInfo{}, errors.New("ip-api: " + r.Message)
	}
	isp := r.Isp
	if isp == "" {
		isp = r.Org
	}
	return NetInfo{IP: r.Query, Country: r.Country, CountryCode: r.CountryCode, Region: r.RegionName,
		City: r.City, ISP: isp, AS: r.As, Provider: "ip-api.com"}, nil
}

func provIPWho(c *http.Client) (NetInfo, error) {
	var r struct {
		Success     bool   `json:"success"`
		Message     string `json:"message"`
		IP          string `json:"ip"`
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
		Region      string `json:"region"`
		City        string `json:"city"`
		Connection  struct {
			Asn int    `json:"asn"`
			Org string `json:"org"`
			Isp string `json:"isp"`
		} `json:"connection"`
	}
	if err := getJSON(c, "https://ipwho.is/?lang=zh-CN", &r); err != nil {
		return NetInfo{}, err
	}
	if !r.Success || r.IP == "" {
		return NetInfo{}, errors.New("ipwho.is: " + r.Message)
	}
	isp := r.Connection.Isp
	if isp == "" {
		isp = r.Connection.Org
	}
	as := ""
	if r.Connection.Asn > 0 {
		as = fmt.Sprintf("AS%d", r.Connection.Asn)
	}
	return NetInfo{IP: r.IP, Country: r.Country, CountryCode: r.CountryCode, Region: r.Region,
		City: r.City, ISP: isp, AS: as, Provider: "ipwho.is"}, nil
}

func provIPInfo(c *http.Client) (NetInfo, error) {
	var r struct{ IP, City, Region, Country, Org string }
	if err := getJSON(c, "https://ipinfo.io/json", &r); err != nil {
		return NetInfo{}, err
	}
	if r.IP == "" {
		return NetInfo{}, errors.New("ipinfo: empty")
	}
	as, isp := "", r.Org
	if strings.HasPrefix(r.Org, "AS") {
		if i := strings.IndexByte(r.Org, ' '); i > 0 {
			as, isp = r.Org[:i], r.Org[i+1:]
		}
	}
	return NetInfo{IP: r.IP, Country: r.Country, CountryCode: r.Country, Region: r.Region,
		City: r.City, ISP: isp, AS: as, Provider: "ipinfo.io"}, nil
}

func runCheck(useSys bool, custom string) CheckResult {
	c, via := makeClient(useSys, custom)
	provs := []func(*http.Client) (NetInfo, error){provIPAPI, provIPWho, provIPInfo}
	var lastErr error
	for i, p := range provs {
		t0 := time.Now()
		info, err := p(c)
		if err == nil {
			return CheckResult{Info: info, OK: true, Latency: time.Since(t0), Via: via, Primary: i == 0}
		}
		lastErr = err
	}
	return CheckResult{OK: false, Err: friendlyErr(lastErr), Via: via}
}

func friendlyErr(err error) string {
	if err == nil {
		return "未知错误"
	}
	s := err.Error()
	var ne net.Error
	switch {
	case strings.Contains(s, "proxyconnect"):
		return "代理连接失败（代理软件未运行？）"
	case errors.As(err, &ne) && ne.Timeout(), strings.Contains(s, "deadline"), strings.Contains(s, "timeout"):
		return "请求超时"
	case strings.Contains(s, "no such host"):
		return "DNS 解析失败"
	case strings.Contains(s, "refused"):
		return "连接被拒绝"
	case strings.Contains(s, "reset"):
		return "连接被重置"
	case strings.Contains(s, "HTTP "):
		return "接口返回 " + s[strings.Index(s, "HTTP "):]
	}
	return "网络不可用"
}
