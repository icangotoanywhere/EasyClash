package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// CoreAPI 通过内核自带的 RESTful 接口（external-controller）查询与控制，和安卓版 CoreApi.kt 对应。
type CoreAPI struct {
	base   string
	secret string
	client *http.Client
}

func newCoreAPI(addr, secret string) *CoreAPI {
	return &CoreAPI{base: "http://" + addr, secret: secret, client: &http.Client{Timeout: 30 * time.Second}}
}

func (c *CoreAPI) call(method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, errors.New("连不上内核：" + err.Error())
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Message != "" {
			return nil, errors.New(e.Message)
		}
		return nil, fmt.Errorf("内核返回 %d", resp.StatusCode)
	}
	return b, nil
}

func esc(s string) string { return strings.ReplaceAll(url.PathEscape(s), "+", "%2B") }

func (c *CoreAPI) Ping() bool {
	_, err := c.call("GET", "/version", nil)
	return err == nil
}

// ProxyItem 代理组里的一个条目（来自内核 /proxies 接口）
type ProxyItem struct {
	Name  string   `json:"name"`
	Type  string   `json:"type"`
	Now   string   `json:"now"`
	All   []string `json:"all"`
	Delay int      `json:"delay"`
}

func (c *CoreAPI) Proxies() (map[string]ProxyItem, error) {
	b, err := c.call("GET", "/proxies", nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Proxies map[string]struct {
			Name    string   `json:"name"`
			Type    string   `json:"type"`
			Now     string   `json:"now"`
			All     []string `json:"all"`
			History []struct {
				Delay int `json:"delay"`
			} `json:"history"`
		} `json:"proxies"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := map[string]ProxyItem{}
	for k, v := range raw.Proxies {
		d := 0
		if n := len(v.History); n > 0 {
			d = v.History[n-1].Delay
		}
		out[k] = ProxyItem{Name: k, Type: v.Type, Now: v.Now, All: v.All, Delay: d}
	}
	return out, nil
}

func (c *CoreAPI) Select(group, name string) error {
	_, err := c.call("PUT", "/proxies/"+esc(group), map[string]string{"name": name})
	return err
}

// GroupDelay 测整个策略组（内核并发测，比逐个测快得多）
func (c *CoreAPI) GroupDelay(group string) map[string]int {
	b, err := c.call("GET", "/group/"+esc(group)+"/delay?timeout=5000&url="+url.QueryEscape("https://www.gstatic.com/generate_204"), nil)
	out := map[string]int{}
	if err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// Delay 测单个节点延迟，返回毫秒；超时或失败返回 0
func (c *CoreAPI) Delay(name string) int {
	b, err := c.call("GET", "/proxies/"+esc(name)+"/delay?timeout=5000&url="+url.QueryEscape("https://www.gstatic.com/generate_204"), nil)
	if err != nil {
		return 0
	}
	var r struct {
		Delay int `json:"delay"`
	}
	_ = json.Unmarshal(b, &r)
	return r.Delay
}

// RuleProviderInfo 订阅引用的一个规则集
type RuleProviderInfo struct {
	Name        string `json:"name"`
	RuleCount   int    `json:"ruleCount"`
	UpdatedAt   string `json:"updatedAt"`
	Vehicle     string `json:"vehicle"`
	Behavior    string `json:"behavior"`
	NeverLoaded bool   `json:"neverLoaded"`
}

func (c *CoreAPI) RuleProviders() ([]RuleProviderInfo, error) {
	b, err := c.call("GET", "/providers/rules", nil)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Providers map[string]struct {
			Name        string `json:"name"`
			RuleCount   int    `json:"ruleCount"`
			UpdatedAt   string `json:"updatedAt"`
			VehicleType string `json:"vehicleType"`
			Behavior    string `json:"behavior"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var out []RuleProviderInfo
	for k, v := range raw.Providers {
		name := v.Name
		if name == "" {
			name = k
		}
		out = append(out, RuleProviderInfo{
			Name: name, RuleCount: v.RuleCount, UpdatedAt: v.UpdatedAt, Vehicle: v.VehicleType, Behavior: v.Behavior,
			NeverLoaded: v.UpdatedAt == "" || strings.HasPrefix(v.UpdatedAt, "0001"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *CoreAPI) UpdateRuleProvider(name string) error {
	_, err := c.call("PUT", "/providers/rules/"+esc(name), nil)
	return err
}

func (c *CoreAPI) RuleCount() int {
	b, err := c.call("GET", "/rules", nil)
	if err != nil {
		return 0
	}
	var raw struct {
		Rules []json.RawMessage `json:"rules"`
	}
	_ = json.Unmarshal(b, &raw)
	return len(raw.Rules)
}

func (c *CoreAPI) SetMode(mode string) error {
	_, err := c.call("PATCH", "/configs", map[string]string{"mode": mode})
	return err
}

func (c *CoreAPI) CloseAllConnections() { _, _ = c.call("DELETE", "/connections", nil) }

// resolveLeaf 沿着策略组一路找到最终的真实节点名
func resolveLeaf(px map[string]ProxyItem, start string) string {
	cur := start
	for i := 0; i < 8; i++ {
		p, ok := px[cur]
		if !ok || p.Now == "" {
			return cur
		}
		cur = p.Now
	}
	return cur
}
