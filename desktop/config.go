package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// 把"机场订阅 + 用户设置"合成内核真正加载的最终配置。与安卓版 ConfigBuilder.kt 的逻辑一致：
//  1. 去掉订阅里与本机环境冲突的字段（端口、tun、控制接口等），换成我们自己的
//  2. 按地区自动生成 "🇸🇬 新加坡·自动" 这类测速组和一个 "🌍 地区选择" 组
//  3. 规则：插入用户附加规则 / 分程序规则 / QUIC 拦截；在线规则集与地理数据库固定走代理下载；订阅没带规则时补基础分流
//  4. 应用高级页的 YAML 覆写
//  5. 系统代理模式不开 TUN；TUN 模式（管理员）开启内核自带的虚拟网卡

const (
	regionSelector    = "🌍 地区选择"
	fallbackMainGroup = "🚀 节点选择"
	geoMirrorHost     = "testingcf.jsdelivr.net"
)

// 订阅没带分流规则时补上的基础分流
var basicRules = []string{
	"GEOIP,LAN,DIRECT,no-resolve",
	"GEOSITE,private,DIRECT",
	"GEOSITE,cn,DIRECT",
	"GEOIP,CN,DIRECT",
}

type buildResult struct {
	Path      string
	MainGroup string
	Regions   []*Region
	Tiered    map[string]bool // 哪些地区分了「优先 / 备用」两级（地区代码）
}

// normalize 把 yaml.v3 解出来的 map[interface{}]interface{} 统一成 map[string]any
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalize(x)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[fmt.Sprint(k)] = normalize(x)
		}
		return m
	case []any:
		for i, x := range t {
			t[i] = normalize(x)
		}
		return t
	}
	return v
}

func loadYamlMap(text []byte) (map[string]any, error) {
	var obj any
	if err := yaml.Unmarshal(text, &obj); err != nil {
		msg := err.Error()
		if i := strings.Index(msg, "\n"); i > 0 {
			msg = msg[:i]
		}
		return nil, errors.New("YAML 格式错误：" + msg)
	}
	m, ok := normalize(obj).(map[string]any)
	if !ok {
		return nil, errors.New("配置顶层不是键值对")
	}
	return m, nil
}

// countProxies 统计节点数。-1 表示不是 Clash 配置；没有 proxies 但有 proxy-providers 时返回 1（数量未知）
func countProxies(text []byte) (int, error) {
	m, err := loadYamlMap(text)
	if err != nil {
		return -1, err
	}
	proxies, okP := m["proxies"].([]any)
	providers, okV := m["proxy-providers"].(map[string]any)
	if !okP && !okV {
		return -1, errors.New("缺少 proxies 或 proxy-providers")
	}
	n := len(proxies)
	if n == 0 && len(providers) > 0 {
		return 1, nil
	}
	return n, nil
}

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func isSelect(g map[string]any) bool { return strings.EqualFold(str(g["type"]), "select") }

// buildConfig 生成最终配置并写入 runDir/config.yaml
func buildConfig(subYaml []byte, st Settings, runDir string, controller string, secret string, tun bool) (*buildResult, error) {
	cfg, err := loadYamlMap(subYaml)
	if err != nil {
		return nil, err
	}

	// ---------- 4. 高级覆写（先应用，让它能影响到后面的分组逻辑） ----------
	overrideHasDNS := false
	if strings.TrimSpace(st.OverrideYaml) != "" {
		ov, err := loadYamlMap([]byte(st.OverrideYaml))
		if err != nil {
			return nil, errors.New("覆写内容有误：" + err.Error())
		}
		_, overrideHasDNS = ov["dns"]
		applyOverride(cfg, ov)
	}

	// ---------- 2. 地区分组 ----------
	proxies := asList(cfg["proxies"])
	var providerNames []string
	for k := range asMap(cfg["proxy-providers"]) {
		providerNames = append(providerNames, k)
	}
	var groups []map[string]any
	for _, g := range asList(cfg["proxy-groups"]) {
		if m := asMap(g); m != nil {
			groups = append(groups, m)
		}
	}
	var rules []string
	for _, r := range asList(cfg["rules"]) {
		rules = append(rules, str(r))
	}
	isMatch := func(r string) bool { return strings.HasPrefix(strings.TrimSpace(r), "MATCH,") }
	// 订阅自己有没有写分流规则（只有一条 MATCH 不算）
	subHasRules := false
	for _, r := range rules {
		if !isMatch(r) {
			subHasRules = true
			break
		}
	}

	var nodeNames []string
	for _, p := range proxies {
		if n := str(asMap(p)["name"]); n != "" {
			nodeNames = append(nodeNames, n)
		}
	}
	existing := map[string]bool{}
	for _, g := range groups {
		existing[str(g["name"])] = true
	}

	var created []*Region
	tiered := map[string]bool{}
	tierSel := map[string]bool{}
	keyword := strings.TrimSpace(st.BackupKeyword)
	interval := testIntervalSec(st)
	for _, region := range allRegions {
		if existing[region.GroupName()] {
			created = append(created, region)
			continue
		}
		var matched []any
		for _, n := range nodeNames {
			if matchRegion(n) == region {
				matched = append(matched, n)
			}
		}
		if len(matched) == 0 && len(providerNames) == 0 {
			continue
		}
		// 分级：名字含备用关键字的节点单独一组。两边都有节点才分，否则就是普通的一个自动组
		var primary, backup []any
		for _, n := range matched {
			if keyword != "" && strings.Contains(strings.ToLower(str(n)), strings.ToLower(keyword)) {
				backup = append(backup, n)
			} else {
				primary = append(primary, n)
			}
		}
		useTiers := keyword != "" && len(backup) > 0 && (len(primary) > 0 || len(providerNames) > 0)
		if !useTiers {
			groups = append(groups, urlTestGroup(region.GroupName(), matched, providerNames, region.Pattern, infoNodePattern, interval))
		} else {
			kw := regexEscape(keyword)
			rp := strings.TrimPrefix(region.Pattern, "(?i)")
			// 优先组：该地区所有不含关键字的节点；备用组：该地区含关键字的节点
			groups = append(groups, urlTestGroup(region.PrimaryGroup(), primary, providerNames, region.Pattern, "(?i)"+infoNodePattern+"|"+kw, interval))
			groups = append(groups, urlTestGroup(region.BackupGroup(), backup, providerNames, "(?i)(?:"+rp+").*(?:"+kw+")|(?:"+kw+").*(?:"+rp+")", infoNodePattern, interval))
			// 自动组本身变成一个选择组，由 tier.go 按"优先比备用慢多少"决定选哪一级
			groups = append(groups, map[string]any{"name": region.GroupName(), "type": "select", "proxies": []any{region.PrimaryGroup(), region.BackupGroup()}})
			tiered[region.Code] = true
			tierSel[region.GroupName()] = true
		}
		existing[region.GroupName()] = true
		created = append(created, region)
	}

	// 主选择组：MATCH 规则指向的组优先，否则第一个 select 组，都没有就自己造一个
	mainGroup := ""
	for i := len(rules) - 1; i >= 0; i-- {
		if isMatch(rules[i]) {
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rules[i]), "MATCH,"))
			for _, g := range groups {
				if str(g["name"]) == name && isSelect(g) {
					mainGroup = name
				}
			}
			break
		}
	}
	if mainGroup == "" {
		for _, g := range groups {
			if isSelect(g) {
				mainGroup = str(g["name"])
				break
			}
		}
	}
	if mainGroup == "" {
		list := []any{}
		for _, n := range nodeNames {
			list = append(list, n)
		}
		if len(list) == 0 {
			list = append(list, "DIRECT")
		}
		g := map[string]any{"name": fallbackMainGroup, "type": "select", "proxies": list}
		if len(providerNames) > 0 {
			g["use"] = providerNames
		}
		groups = append([]map[string]any{g}, groups...)
		mainGroup = fallbackMainGroup
		hasMatch := false
		for _, r := range rules {
			if isMatch(r) {
				hasMatch = true
			}
		}
		if !hasMatch {
			rules = append(rules, "MATCH,"+fallbackMainGroup)
		}
	}

	if len(created) > 0 {
		if !existing[regionSelector] {
			var names []any
			for _, r := range created {
				names = append(names, r.GroupName())
			}
			groups = append(groups, map[string]any{"name": regionSelector, "type": "select", "proxies": names})
		}
		// 每个能选代理的手动组（含主组）末尾都加上「🌍 地区选择」这一项：锁定地区时把它们都切过去，
		// 所有走代理的流量就都从该地区出去。加在末尾，不改变订阅原来的默认选择。
		nonProxy := map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "COMPATIBLE": true}
		for _, g := range groups {
			name := str(g["name"])
			// 分级地区的「·自动」选择组是地区选择的下级，加进去会形成环
			if name == regionSelector || !isSelect(g) || tierSel[name] {
				continue
			}
			list := asList(g["proxies"])
			canProxy := name == mainGroup || g["use"] != nil || g["include-all"] == true || g["include-all-proxies"] == true
			found := false
			for _, x := range list {
				if str(x) == regionSelector {
					found = true
				}
				if !nonProxy[strings.ToUpper(str(x))] {
					canProxy = true
				}
			}
			if canProxy && !found {
				g["proxies"] = append(list, regionSelector)
			}
		}
	}

	// ---------- 在线规则集 / 地理数据库的下载线路 ----------
	// 订阅引用的在线规则集和地理数据库大多放在国外网站上，国内直连下不了；很多订阅的兜底组默认指向"直连"，
	// 结果规则集全部下载失败、等于没有。这里固定让它们走代理：有地区选择组就用它，否则用主组。订阅自己写了 proxy 的不动。
	downloadVia := mainGroup
	if len(created) > 0 {
		downloadVia = regionSelector
	}
	if rp := asMap(cfg["rule-providers"]); rp != nil {
		for _, v := range rp {
			if m := asMap(v); m != nil && strings.EqualFold(str(m["type"]), "http") && m["proxy"] == nil {
				m["proxy"] = downloadVia
			}
		}
	}

	// ---------- 3. 规则 ----------
	if !subHasRules {
		// 订阅没带分流规则：补一套基础分流，局域网和国内网站、国内 IP 直连，其余走主组
		subMatch := ""
		for _, r := range rules {
			if isMatch(r) {
				subMatch = r
			}
		}
		rules = append([]string{}, basicRules...)
		if subMatch == "" {
			subMatch = "MATCH," + mainGroup
		}
		rules = append(rules, subMatch)
	} else {
		hasMatch := false
		for _, r := range rules {
			if isMatch(r) {
				hasMatch = true
			}
		}
		if !hasMatch {
			// 有规则但没有兜底：没命中的流量内核默认直连，这里补成走主组
			rules = append(rules, "MATCH,"+mainGroup)
		}
	}

	var front []string
	// 分程序代理（按进程名）：排除的程序永远直连；"仅选中"时其余程序一律直连
	var apps []string
	for _, a := range st.AppList {
		a = strings.TrimSpace(a)
		if a != "" && !strings.ContainsAny(a, ",()") {
			apps = append(apps, a)
		}
	}
	if len(apps) > 0 {
		switch st.AppProxyMode {
		case "deny":
			for _, a := range apps {
				front = append(front, "PROCESS-NAME,"+a+",DIRECT")
			}
		case "allow":
			if len(apps) == 1 {
				front = append(front, "NOT,((PROCESS-NAME,"+apps[0]+")),DIRECT")
			} else {
				var sub []string
				for _, a := range apps {
					sub = append(sub, "(PROCESS-NAME,"+a+")")
				}
				front = append(front, "NOT,((OR,("+strings.Join(sub, ",")+"))),DIRECT")
			}
		}
	}
	if st.BlockQuic {
		front = append(front, "AND,((NETWORK,UDP),(DST-PORT,443)),REJECT")
	}
	if st.BingDirect {
		front = append(front, microsoftDirectRule())
	}
	for _, line := range strings.Split(st.ExtraRules, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			front = append(front, line)
		}
	}
	// 地理数据库的更新下载也固定走代理（理由同上）
	if !strings.Contains(downloadVia, ",") {
		front = append(front, "DOMAIN,"+geoMirrorHost+","+downloadVia)
	}
	// 内核自己的控制接口与本地回环永远直连
	front = append(front, "IP-CIDR,127.0.0.0/8,DIRECT,no-resolve")
	rules = append(front, rules...)

	groupList := make([]any, 0, len(groups))
	for _, g := range groups {
		groupList = append(groupList, g)
	}
	ruleList := make([]any, 0, len(rules))
	for _, r := range rules {
		ruleList = append(ruleList, r)
	}
	if proxies == nil {
		proxies = []any{}
	}
	cfg["proxies"] = proxies
	cfg["proxy-groups"] = groupList
	cfg["rules"] = ruleList

	// ---------- 1 + 5. 本机环境专属字段 ----------
	for _, k := range []string{"port", "socks-port", "redir-port", "tproxy-port", "mixed-port", "allow-lan", "bind-address",
		"external-controller", "external-controller-tls", "external-controller-unix", "external-controller-pipe", "external-ui", "external-ui-url",
		"secret", "tun", "interface-name", "routing-mark", "lan-allowed-ips", "lan-disallowed-ips",
		"external-controller-cors", "ebpf", "iptables", "listeners"} {
		delete(cfg, k)
	}
	if st.MixedPort > 0 {
		cfg["mixed-port"] = st.MixedPort
	}
	cfg["allow-lan"] = false
	cfg["bind-address"] = "127.0.0.1"
	cfg["mode"] = st.Mode
	cfg["log-level"] = st.LogLevel
	cfg["ipv6"] = st.IPv6
	// 出境直连失败自动转代理：告诉内核侧（fallback.go）改走哪个组；没有这一项就是关闭
	delete(cfg, "easyclash-direct-fallback")
	if st.DirectFallback {
		cfg["easyclash-direct-fallback"] = downloadVia
	}
	cfg["external-controller"] = controller
	cfg["secret"] = secret
	cfg["unified-delay"] = true
	cfg["tcp-concurrent"] = true
	cfg["find-process-mode"] = "strict"
	cfg["geodata-mode"] = false
	cfg["geo-auto-update"] = false
	cfg["profile"] = map[string]any{"store-selected": true, "store-fake-ip": true}
	// 地理数据库的更新下载地址（jsDelivr 镜像；数据库本身已打包在程序里，这里只用于"更新"）
	mirror := "https://" + geoMirrorHost + "/gh/MetaCubeX/meta-rules-dat@release"
	cfg["geox-url"] = map[string]any{
		"geoip": mirror + "/geoip.dat", "geosite": mirror + "/geosite.dat",
		"mmdb": mirror + "/geoip.metadb", "asn": mirror + "/GeoLite2-ASN.mmdb",
	}

	if tun {
		cfg["tun"] = map[string]any{
			"enable": true, "stack": st.TunStack, "auto-route": true, "auto-detect-interface": true,
			"dns-hijack": []any{"any:53"}, "strict-route": false, "mtu": 9000,
		}
	}

	subDNS := asMap(cfg["dns"])
	keepSubDNS := (st.UseSubscriptionDNS && subDNS != nil && subDNS["enable"] == true) || overrideHasDNS
	if keepSubDNS && subDNS != nil {
		subDNS["enable"] = true
		subDNS["ipv6"] = st.IPv6
		delete(subDNS, "listen") // 避免端口冲突
		cfg["dns"] = subDNS
	} else {
		cfg["dns"] = defaultDNS(st.IPv6, tun)
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(runDir, "config.yaml")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return nil, err
	}
	return &buildResult{Path: path, MainGroup: mainGroup, Regions: created, Tiered: tiered}, nil
}

func defaultDNS(ipv6, tun bool) map[string]any {
	d := map[string]any{
		"enable": true,
		// 不开 listen：TUN 模式下由 dns-hijack 在内核内部接管 53 端口；系统代理模式下只给内核自己用
		"ipv6":                    ipv6,
		"prefer-h3":               false,
		"default-nameserver":      []any{"223.5.5.5", "119.29.29.29"},
		"nameserver":              []any{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"},
		"proxy-server-nameserver": []any{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"},
		"respect-rules":           false,
	}
	if tun {
		d["enhanced-mode"] = "fake-ip"
		d["fake-ip-range"] = "198.18.0.1/16"
		d["fake-ip-filter"] = []any{
			"*.lan", "*.local", "*.localdomain", "localhost.ptlogin2.qq.com", "localhost.sec.qq.com",
			"+.msftconnecttest.com", "+.msftncsi.com", "dns.msftncsi.com",
			"+.srv.nintendo.net", "+.stun.playstation.net", "+.xboxlive.com", "stun.*.*", "stun.*.*.*",
			"time.*.com", "ntp.*.com", "+.pool.ntp.org", "time.windows.com",
		}
	}
	return d
}

// applyOverride 覆写合并规则（与 Clash Verge / Clash Party 一致）：
//
//	+rules:  插到原列表前面     rules+:  追加到原列表后面
//	其他键：两边都是键值对则合并，否则整体替换
func applyOverride(base, ov map[string]any) {
	for k, v := range ov {
		switch {
		case strings.HasPrefix(k, "+"):
			key := k[1:]
			base[key] = append(append([]any{}, asList(v)...), asList(base[key])...)
		case strings.HasSuffix(k, "+"):
			key := k[:len(k)-1]
			base[key] = append(append([]any{}, asList(base[key])...), asList(v)...)
		default:
			bm, ok1 := base[k].(map[string]any)
			vm, ok2 := v.(map[string]any)
			if ok1 && ok2 {
				for kk, vv := range vm {
					bm[kk] = vv
				}
			} else {
				base[k] = v
			}
		}
	}
}

// testIntervalSec 自动测速组的健康检查间隔（秒）：高级页"自动测速间隔"，默认 2 分钟
func testIntervalSec(st Settings) int {
	m := st.TestIntervalMin
	if m < 1 {
		m = 2
	}
	if m > 1440 {
		m = 1440
	}
	return m * 60
}

// urlTestGroup 一个按延迟自动选择的组。lazy：只有被用到时才定期测速，省流量
func urlTestGroup(name string, nodes []any, providers []string, filter, exclude string, interval int) map[string]any {
	g := map[string]any{
		"name": name, "type": "url-test",
		"url": "https://www.gstatic.com/generate_204", "interval": interval, "tolerance": 50, "lazy": true,
	}
	if len(nodes) > 0 {
		g["proxies"] = nodes
	}
	if len(providers) > 0 {
		g["use"] = providers
		g["filter"] = filter
		g["exclude-filter"] = exclude
	}
	return g
}

// 微软服务走国内（含必应国内版），与安卓版 ConfigBuilder.kt 一致：微软的服务在国内都能直连，直连更快更稳，
// 必应也会自动落到国内版（cn.bing.com）。例外：Copilot（国内不提供服务）不直连；GitHub 不在名单里（国内直连不稳）。
// 例外的域名不会被这条规则命中，交给订阅自己的规则处理（一般就是走代理）。
var microsoftDirectDomains = []string{
	"bing.com",
	"bing.net",
	"bingapis.com",
	"bing.com.cn",
	"microsoft.com",
	"microsoftonline.com",
	"microsoftonline-p.com",
	"msauth.net",
	"msftauth.net",
	"live.com",
	"live.net",
	"office.com",
	"office.net",
	"office365.com",
	"microsoft365.com",
	"outlook.com",
	"hotmail.com",
	"onedrive.com",
	"sharepoint.com",
	"onenote.com",
	"msn.com",
	"skype.com",
	"xbox.com",
	"xboxlive.com",
	"windows.com",
	"windowsupdate.com",
	"msftconnecttest.com",
	"msftncsi.com",
	"microsoftstore.com",
	"visualstudio.com",
	"s-microsoft.com",
	"aka.ms",
}

var microsoftProxyExceptions = []string{
	"copilot.microsoft.com",
	"designer.microsoft.com",
	"sydney.bing.com",
	"edgeservices.bing.com",
}

// microsoftDirectRule 一条逻辑规则：属于微软域名、又不是例外（Copilot）的 → 直连
func microsoftDirectRule() string {
	or := func(list []string) string {
		parts := make([]string, len(list))
		for i, d := range list {
			parts[i] = "(DOMAIN-SUFFIX," + d + ")"
		}
		return "OR,(" + strings.Join(parts, ",") + ")"
	}
	return "AND,((" + or(microsoftDirectDomains) + "),(NOT,((" + or(microsoftProxyExceptions) + ")))),DIRECT"
}
