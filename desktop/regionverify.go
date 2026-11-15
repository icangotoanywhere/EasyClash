package main

// 锁定地区时核实节点的实际出口（安卓对应 app/.../core/RegionVerify.kt，两边逻辑相同）。
//
// 节点名写着「日本」，实际出口不一定在日本。锁定地区后，EasyClash 在地区自动组（url-test）里替内核挑节点：
//  1. 按延迟从快到慢排候选；当前钉住的节点还活着、且不比最快的慢 50 ms 以上（和 url-test 的 tolerance 一致）就不动；
//  2. 否则依次核实候选：经专用的本地入口（verifyListener，出站固定为 verifyGroup，把这个组切到候选节点）查 ip-api.com，
//     国家代码和锁定的地区一致才「钉住」（url-test 的固定选择，PUT /proxies/组）；
//  3. 不一致的写一条「[地区不符]」日志、跳过；全都不符合就保持原来的节点不动。
//
// 钉住后内核仍会在节点断开时（5 秒内 5 次连接失败或被拒）立刻自己改用最快的可用节点，网络不断；
// 每 30 秒检查一次，发现当前用的不是核实过的节点就重新核实。解除锁定时取消钉住（DELETE /proxies/组）。
// 核实结果缓存 6 小时；ip-api.com 免费版每分钟 45 次，所以每轮最多查 verifyMaxChecks 个新节点。

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	verifyGroup     = "EasyClash·核实出口" // 核实专用的选择组（包含全部节点，界面上不显示）
	verifyListener  = "easyclash-verify"
	verifyTTL       = 6 * time.Hour
	verifyTolerance = 50
	verifyMaxChecks = 5
)

// verifyListenPort 核实出口用的本机入口端口；连接时选一个空闲端口，重载沿用。0 表示不生成（例如启动时的配置检查）
var verifyListenPort int

type verifyCheck struct {
	code, where, ip string
	at              time.Time
}

var verifier = struct {
	mu     sync.Mutex // 保护 cache / pinned
	run    sync.Mutex // 同一时间只核实一轮
	cache  map[string]verifyCheck
	pinned map[string]string // 地区组 → 钉住的节点
}{cache: map[string]verifyCheck{}, pinned: map[string]string{}}

// verifyReset 断开连接时清空（节点可能变了）
func verifyReset() {
	verifier.mu.Lock()
	verifier.cache = map[string]verifyCheck{}
	verifier.pinned = map[string]string{}
	verifier.mu.Unlock()
}

func verifyCached(node string) (verifyCheck, bool) {
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	c, ok := verifier.cache[node]
	if !ok || time.Since(c.at) > verifyTTL {
		return verifyCheck{}, false
	}
	return c, true
}

// verifyUnpinAllExcept 没锁定的地区组都取消钉住（钉住记录会保存到下次连接，解除锁定时可能没连着）
func verifyUnpinAllExcept(api *CoreAPI, regions []*Region, keep string) {
	for _, r := range regions {
		g := r.GroupName()
		if g == keep {
			continue
		}
		verifier.mu.Lock()
		delete(verifier.pinned, g)
		verifier.mu.Unlock()
		_ = api.Unfix(g)
	}
}

// verifyOnLockChanged 锁定地区变化后（连接、重载、切换锁定）调用：别的地区组取消钉住；锁定了就先测一次该组，再核实挑节点
func verifyOnLockChanged(api *CoreAPI, regions []*Region, region *Region) {
	keep := ""
	if region != nil {
		keep = region.GroupName()
	}
	verifyUnpinAllExcept(api, regions, keep)
	if region == nil {
		return
	}
	api.GroupDelay(region.GroupName())
	verifyEnsure(api, region, true)
}

// verifyEnsure 核实并钉住。force：锁定地区刚变化、或刚测过速时为 true。正在核实时直接返回
func verifyEnsure(api *CoreAPI, region *Region, force bool) {
	if verifyListenPort == 0 || !verifier.run.TryLock() {
		return
	}
	defer verifier.run.Unlock()
	px, err := api.Proxies()
	if err != nil {
		return
	}
	group, ok := px[region.GroupName()]
	if !ok {
		return
	}
	var nodes []ProxyItem
	for _, n := range group.All {
		p, ok := px[n]
		if !ok || len(p.All) > 0 || p.Delay <= 0 || infoNodeRe.MatchString(p.Name) {
			continue
		}
		nodes = append(nodes, p)
	}
	if len(nodes) == 0 {
		return
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Delay < nodes[j].Delay })
	best := nodes[0]
	verifier.mu.Lock()
	cur := verifier.pinned[region.GroupName()]
	verifier.mu.Unlock()
	curDelay := 0
	for _, n := range nodes {
		if n.Name == cur {
			curDelay = n.Delay
		}
	}
	cc, cached := verifyCached(cur)
	curOK := cur != "" && curDelay > 0 && cached && cc.code == region.Code

	if curOK && (!force || cur == group.Now) && curDelay <= best.Delay+verifyTolerance {
		if group.Now != cur {
			_ = api.Select(region.GroupName(), cur) // 被内核临时换走后又恢复了，钉回来
		}
		return
	}

	checks := 0
	for _, n := range nodes {
		// 钉住的节点仍合格且是剩下候选里最快的，就不用换
		if n.Name == cur && curOK {
			if group.Now != cur {
				_ = api.Select(region.GroupName(), cur)
			}
			return
		}
		c, ok := verifyCached(n.Name)
		if !ok {
			if checks >= verifyMaxChecks {
				break
			}
			checks++
			var err error
			c, err = verifyQuery(api, n.Name)
			if err != nil {
				appendLog("[地区核实] 节点「" + n.Name + "」查不到出口（" + shortNetErr(err) + "），跳过")
				continue
			}
			verifier.mu.Lock()
			verifier.cache[n.Name] = c
			verifier.mu.Unlock()
			if c.code != region.Code {
				appendLog(fmt.Sprintf("[地区不符] 节点「%s」标的是%s，实际出口在 %s（%s，IP %s），已跳过", n.Name, region.DisplayName(), c.where, c.code, c.ip))
			}
		}
		if c.code == region.Code {
			if api.Select(region.GroupName(), n.Name) == nil {
				verifier.mu.Lock()
				verifier.pinned[region.GroupName()] = n.Name
				verifier.mu.Unlock()
				if n.Name != cur {
					appendLog(fmt.Sprintf("[地区核实] %s：切到「%s」（%d ms，出口 %s，IP %s）", region.DisplayName(), n.Name, n.Delay, c.where, c.ip))
				}
			}
			return
		}
	}
	keepMsg := "保持当前选择不变"
	if cur != "" {
		keepMsg = "保持「" + cur + "」不变"
	}
	appendLog("[地区核实] " + region.DisplayName() + "：没有找到出口确实在" + region.DisplayName() + "、又能连通的节点，" + keepMsg)
}

// verifyQuery 把核实组切到这个节点，再经核实专用入口查出口 IP
func verifyQuery(api *CoreAPI, node string) (verifyCheck, error) {
	if err := api.Select(verifyGroup, node); err != nil {
		return verifyCheck{}, err
	}
	pu, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(verifyListenPort))
	c := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	req, _ := http.NewRequest("GET", "http://ip-api.com/json/?lang=zh-CN&fields=status,message,country,countryCode,regionName,city,query", nil)
	req.Header.Set("User-Agent", "EasyClash/"+appVersion)
	resp, err := c.Do(req)
	if err != nil {
		return verifyCheck{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return verifyCheck{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var r struct {
		Status, Message, Country, CountryCode, RegionName, City, Query string
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return verifyCheck{}, err
	}
	if r.Status != "success" {
		if r.Message == "" {
			r.Message = "查询失败"
		}
		return verifyCheck{}, errors.New(r.Message)
	}
	var parts []string
	for _, p := range []string{r.Country, r.RegionName, r.City} {
		if p != "" && (len(parts) == 0 || parts[len(parts)-1] != p) {
			parts = append(parts, p)
		}
	}
	return verifyCheck{code: strings.ToUpper(r.CountryCode), where: strings.Join(parts, " · "), ip: r.Query, at: time.Now()}, nil
}
