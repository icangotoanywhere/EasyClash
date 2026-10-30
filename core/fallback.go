package core

// 出境直连失败自动转代理（安卓版与电脑版 desktop/fallback.go 内容一致，只有包名不同）。
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

import (
	"context"
	"errors"
	"net/netip"
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

const fallbackKey = "easyclash-direct-fallback"

type fallbackCtxKey struct{}

var (
	geoOnce   sync.Once
	cnSite    *RC.GEOSITE
	cnIP      *RC.GEOIP
	learnedMu sync.Mutex
	learned   = map[string]bool{} // 本次运行中直连失败过、已改走代理的网站
)

// installDirectFallback 在 ApplyConfig 之前调用：按配置里的开关给 DIRECT 装上"失败转代理"。
func installDirectFallback(cfg *config.Config, raw []byte) {
	var opt map[string]any
	_ = yaml.Unmarshal(raw, &opt)
	name, _ := opt[fallbackKey].(string)
	if name == "" {
		return
	}
	dp, ok := cfg.Proxies["DIRECT"].(*adapter.Proxy)
	if !ok {
		return
	}
	via, ok := cfg.Proxies[name]
	if !ok {
		log.Warnln("[EasyClash] 出境直连失败转代理：找不到策略组 %s，未启用", name)
		return
	}
	inner := dp.ProxyAdapter
	if w, ok := inner.(*directFallback); ok { // 防止重复包装
		inner = w.ProxyAdapter
	}
	dp.ProxyAdapter = &directFallback{ProxyAdapter: inner, via: via}
	learnedMu.Lock()
	learned = map[string]bool{}
	learnedMu.Unlock()
	log.Infoln("[EasyClash] 出境直连失败自动转代理：已开启，改走 %s", name)
}

type directFallback struct {
	C.ProxyAdapter
	via C.Proxy
}

func (d *directFallback) DialContext(ctx context.Context, m *C.Metadata) (C.Conn, error) {
	if ctx.Value(fallbackCtxKey{}) != nil || tunnel.Mode() == tunnel.Direct {
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
func (d *directFallback) viaUsable(m *C.Metadata) bool {
	var p C.Proxy = d.via
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
