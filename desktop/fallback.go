package main

// 出境直连失败自动转代理（与安卓版 core/fallback.go 内容一致，只有包名不同）。
//
// 规则让一条连接直连、而目标在境外（域名不在国内网站列表、IP 不在国内）时，若直连连不上
// （超时、被拒绝、域名解析失败），就改经代理再试一次；同一个网站失败过一次后，本次运行期间
// 直接走代理，不再先试直连。国内目标直连失败按正常失败处理，不重试。方向只有"直连 → 代理"，
// 代理失败绝不改走直连（避免真实 IP 暴露给 Claude、Google 等服务）。
//
// 做法：mihomo 里所有"直连"（规则写的 DIRECT、策略组里选的 DIRECT、兜底直连）用的都是同一个
// DIRECT 对象。配置解析之后、生效之前，把这个对象里的出站换成带重试的包装，一处替换处处生效。
// 改走哪个代理由配置里的 easyclash-direct-fallback 指定（界面侧写入：有地区选择组就用它，即
// 首页选的出口地区，否则用主组）；没有这一项就是关闭。
//
// 同一个包装还负责「国际 AI 服务强制走代理」：配置里 easyclash-proxy-only 列出的域名（含子域名），
// 它们的连接一律不准直连——无论规则写了什么、策略组里选的是不是直连、首页是不是直连模式。
// 要直连的这类连接改走 via 指定的代理组（主组，即首页选的出口地区）；代理组当前选的也是直连 / 拒绝时
// 就拒绝连接。只比域名，不查连接属于哪个 App，没有额外开销。

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/config"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/log"
	RC "github.com/metacubex/mihomo/rules/common"
	"github.com/metacubex/mihomo/tunnel"
	"go.yaml.in/yaml/v3"
)

const (
	fallbackKey  = "easyclash-direct-fallback"
	proxyOnlyKey = "easyclash-proxy-only" // {via: 代理组, domains: [域名...]}
)

type fallbackCtxKey struct{}

var (
	geoOnce   sync.Once
	cnSite    *RC.GEOSITE
	cnIP      *RC.GEOIP
	learnedMu sync.Mutex
	learned   = map[string]bool{} // 本次运行中直连失败过、已改走代理的网站
)

// installDirectFallback 在 ApplyConfig 之前调用：按配置给 DIRECT 装上"失败转代理"和"AI 服务不准直连"。
func installDirectFallback(cfg *config.Config, raw []byte) {
	var opt map[string]any
	_ = yaml.Unmarshal(raw, &opt)
	dp, ok := cfg.Proxies["DIRECT"].(*adapter.Proxy)
	if !ok {
		return
	}
	inner := dp.ProxyAdapter
	if w, ok := inner.(*directFallback); ok { // 防止重复包装
		inner = w.ProxyAdapter
	}

	var via C.Proxy
	if name, _ := opt[fallbackKey].(string); name != "" {
		if p, ok := cfg.Proxies[name]; ok {
			via = p
			log.Infoln("[EasyClash] 出境直连失败自动转代理：已开启，改走 %s", name)
		} else {
			log.Warnln("[EasyClash] 出境直连失败转代理：找不到策略组 %s，未启用", name)
		}
	}
	guard := parseProxyOnly(opt[proxyOnlyKey], cfg.Proxies)
	if len(guard.domains) > 0 {
		log.Infoln("[EasyClash] 国际 AI 服务强制走代理：%d 个域名不允许直连", len(guard.domains))
	}
	if via == nil && len(guard.domains) == 0 {
		dp.ProxyAdapter = inner
		return
	}
	dp.ProxyAdapter = &directFallback{ProxyAdapter: inner, via: via, guard: guard}
	learnedMu.Lock()
	learned = map[string]bool{}
	learnedMu.Unlock()
}

type directFallback struct {
	C.ProxyAdapter
	via   C.Proxy // 直连失败时改走的代理；nil = 不转
	guard proxyOnly
}

// proxyOnly 不允许直连的域名
type proxyOnly struct {
	via     C.Proxy  // 这类连接改走的代理组；nil 或当前选的是直连 / 拒绝时直接拒绝
	domains []string // 小写域名，匹配它本身及所有子域名
}

func parseProxyOnly(v any, proxies map[string]C.Proxy) proxyOnly {
	var g proxyOnly
	m, _ := v.(map[string]any)
	if name, _ := m["via"].(string); name != "" {
		g.via = proxies[name]
	}
	if list, ok := m["domains"].([]any); ok {
		for _, x := range list {
			if s := strings.ToLower(strings.Trim(strings.TrimSpace(fmt.Sprint(x)), ".")); s != "" {
				g.domains = append(g.domains, s)
			}
		}
	}
	return g
}

// blocks 这条（准备直连的）连接的域名是否在名单里：返回命中的域名，空串表示不在
func (g proxyOnly) blocks(m *C.Metadata) string {
	for _, host := range []string{m.Host, m.SniffHost} {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if host == "" {
			continue
		}
		for _, d := range g.domains {
			if host == d || strings.HasSuffix(host, "."+d) {
				return d
			}
		}
	}
	return ""
}

// guardVia 这类连接改走的代理组：组存在、且当前实际选的不是直连 / 拒绝时才返回
func (d *directFallback) guardVia(m *C.Metadata) C.Proxy {
	if d.guard.via != nil && usable(d.guard.via, m) {
		return d.guard.via
	}
	return nil
}

func (d *directFallback) refuse(m *C.Metadata, domain string) error {
	log.Infoln("[EasyClash] 国际 AI 服务强制走代理：%s（%s）不允许直连，代理组当前不可用，已拒绝", m.RemoteAddress(), domain)
	return fmt.Errorf("EasyClash：%s 不允许直连", domain)
}

func (d *directFallback) ListenPacketContext(ctx context.Context, m *C.Metadata) (C.PacketConn, error) {
	if dom := d.guard.blocks(m); dom != "" {
		via := d.guardVia(m)
		if via == nil || ctx.Value(fallbackCtxKey{}) != nil {
			return nil, d.refuse(m, dom)
		}
		return via.ListenPacketContext(context.WithValue(ctx, fallbackCtxKey{}, true), m)
	}
	return d.ProxyAdapter.ListenPacketContext(ctx, m)
}

func (d *directFallback) DialContext(ctx context.Context, m *C.Metadata) (C.Conn, error) {
	if dom := d.guard.blocks(m); dom != "" {
		via := d.guardVia(m)
		if via == nil || ctx.Value(fallbackCtxKey{}) != nil { // 已经是改道后又绕回直连：拒绝，防止死循环
			return nil, d.refuse(m, dom)
		}
		log.Debugln("[EasyClash] %s（%s）不允许直连，改走 %s", m.RemoteAddress(), dom, via.Name())
		return via.DialContext(context.WithValue(ctx, fallbackCtxKey{}, true), m)
	}
	if d.via == nil || ctx.Value(fallbackCtxKey{}) != nil || tunnel.Mode() == tunnel.Direct {
		return d.ProxyAdapter.DialContext(ctx, m)
	}
	key := m.Host
	if key == "" && m.DstIP.IsValid() {
		key = m.DstIP.String()
	}
	learnedMu.Lock()
	known := learned[key]
	learnedMu.Unlock()
	if known && d.viaUsable(m) {
		return d.viaDial(ctx, m)
	}

	c, err := d.ProxyAdapter.DialContext(ctx, m)
	if err == nil || ctx.Err() != nil || key == "" || !d.viaUsable(m) || !abroad(ctx, m) {
		return c, err
	}
	log.Infoln("[EasyClash] %s 直连失败（%v），改走代理 %s 重试；本次运行内该网站直接走代理", m.RemoteAddress(), err, d.via.Name())
	learnedMu.Lock()
	learned[key] = true
	learnedMu.Unlock()
	c2, err2 := d.viaDial(ctx, m)
	if err2 != nil {
		return nil, errors.Join(err, err2)
	}
	return c2, nil
}

func (d *directFallback) viaDial(ctx context.Context, m *C.Metadata) (C.Conn, error) {
	return d.via.DialContext(context.WithValue(ctx, fallbackCtxKey{}, true), m)
}

// viaUsable 代理那一侧当前实际选中的不是直连 / 拒绝时才有意义
func (d *directFallback) viaUsable(m *C.Metadata) bool { return usable(d.via, m) }

// usable 沿策略组一路看下去，最终实际选中的是真正的代理节点（不是直连 / 拒绝）
func usable(start C.Proxy, m *C.Metadata) bool {
	var p C.Proxy = start
	for i := 0; p != nil && i < 16; i++ {
		switch p.Type() {
		case C.Direct, C.Reject, C.RejectDrop, C.Pass, C.Compatible:
			return false
		}
		next := p.Unwrap(m, false)
		if next == nil {
			return true
		}
		p = next
	}
	return p != nil
}

// abroad 目标是否在境外：域名不在国内网站列表，且（能解析时）IP 不在国内、不是局域网。
// 地理数据库不可用时保守处理：判断不了就不重试。
func abroad(ctx context.Context, m *C.Metadata) bool {
	geoOnce.Do(func() {
		if s, err := RC.NewGEOSITE("cn", ""); err == nil {
			cnSite = s
		}
		if g, err := RC.NewGEOIP("cn", "", false, true); err == nil {
			cnIP = g
		}
	})
	if cnSite == nil || cnIP == nil {
		return false
	}
	if m.Host != "" && cnSite.MatchDomain(m.Host) {
		return false
	}
	ip := m.DstIP.Unmap()
	if ip.IsValid() && fakeIPRange.Contains(ip) { // fake-ip 模式下的假地址不代表真实位置
		ip = netip.Addr{}
	}
	if !ip.IsValid() && m.Host != "" {
		if r, err := resolver.ResolveIP(ctx, m.Host); err == nil {
			ip = r
		}
	}
	if ip.IsValid() {
		ip = ip.Unmap()
		if isLocal(ip) || cnIP.MatchIp(ip) {
			return false
		}
	}
	return m.Host != "" || ip.IsValid()
}

var (
	fakeIPRange = netip.MustParsePrefix("198.18.0.0/15")
	cgnatRange  = netip.MustParsePrefix("100.64.0.0/10")
)

func isLocal(ip netip.Addr) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		cgnatRange.Contains(ip) || fakeIPRange.Contains(ip)
}
