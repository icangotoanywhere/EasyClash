package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 订阅的下载、解析、保存，对应安卓版 SubscriptionRepo.kt

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// normalizeURL 支持直接粘贴 clash://install-config?url=... 这类一键导入链接
func normalizeURL(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	if strings.HasPrefix(t, "clash://") || strings.HasPrefix(t, "clashmeta://") {
		if u, err := url.Parse(t); err == nil {
			if v := u.Query().Get("url"); v != "" {
				return v, nil
			}
		}
	}
	if !strings.HasPrefix(t, "http://") && !strings.HasPrefix(t, "https://") {
		return "", errors.New("订阅链接必须以 http:// 或 https:// 开头")
	}
	return t, nil
}

var base64Re = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)

func looksLikeBase64(s string) bool {
	t := strings.TrimSpace(s)
	return len(t) > 20 && strings.Count(t, "\n") < 5 && base64Re.MatchString(strings.ReplaceAll(t, "\n", ""))
}

var filenameRe = regexp.MustCompile(`filename\*?=(?:UTF-8'')?"?([^";]+)`)

// downloadRoute 下载订阅的一条线路
type downloadRoute struct {
	Label string // 显示用：本软件的代理 / 系统代理 127.0.0.1:7897 / 直连
	Proxy string // 代理地址（http://… 或 socks5://…），空表示直连
}

// shortNetErr 把常见的网络错误翻成一句人话
func shortNetErr(err error) string {
	m := err.Error()
	l := strings.ToLower(m)
	switch {
	case strings.Contains(l, "timeout") || strings.Contains(m, "did not properly respond") || strings.Contains(l, "deadline exceeded"):
		return "连接超时"
	case strings.Contains(l, "refused") || strings.Contains(m, "actively refused"):
		return "连接被拒绝"
	case strings.Contains(l, "no such host"):
		return "域名解析失败"
	case strings.Contains(l, "reset") || strings.Contains(l, "forcibly closed") || strings.Contains(l, "eof"):
		return "连接被中断"
	}
	if len(m) > 120 {
		m = m[:120] + "…"
	}
	return m
}

// downloadSub 依次尝试各条线路下载订阅，连不上才换下一条（服务器返回错误时不再换线路）。
// 线路顺序由调用方决定：已连接时先走本软件的代理，再走系统里已有的代理（比如另一个代理软件），最后直连。
func downloadSub(store *Store, sub *Subscription, routes []downloadRoute) error {
	if strings.TrimSpace(sub.URL) == "" {
		return errors.New("这个订阅是从文件导入的，没有更新地址")
	}
	if _, err := url.Parse(sub.URL); err != nil {
		return errors.New("订阅链接格式不对：" + err.Error())
	}
	if len(routes) == 0 {
		routes = []downloadRoute{{Label: "直连"}}
	}
	var tried []string
	for _, rt := range routes {
		tr := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 15 * time.Second}
		if rt.Proxy != "" {
			if pu, err := url.Parse(rt.Proxy); err == nil {
				tr.Proxy = http.ProxyURL(pu)
			}
		}
		client := &http.Client{Timeout: 25 * time.Second, Transport: tr}
		req, _ := http.NewRequest("GET", sub.URL, nil)
		// 机场看到 clash 字样的 UA 才会返回 Clash 格式配置
		req.Header.Set("User-Agent", "clash.meta/1.19.0 mihomo EasyClash/1.0")
		req.Header.Set("Accept", "*/*")
		resp, err := client.Do(req)
		if err != nil {
			appendLog("[APP] 下载订阅失败（" + rt.Label + "）：" + err.Error())
			tried = append(tried, rt.Label+"："+shortNetErr(err))
			continue
		}
		if len(routes) > 1 {
			appendLog("[APP] 订阅下载线路：" + rt.Label)
		}
		defer resp.Body.Close()
		return readSubResponse(store, sub, resp)
	}
	return errors.New("无法连接订阅服务器（已尝试 " + strings.Join(tried, "；") + "）。\n可能是机场服务器暂时连不上，稍后再试；或者先连接一个能用的订阅后再更新。")
}

// readSubResponse 校验并保存下载到的订阅内容，读取机场给的流量、到期、名称、更新间隔
func readSubResponse(store *Store, sub *Subscription, resp *http.Response) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("订阅服务器返回错误 %d，请检查链接是否过期", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return errors.New("下载订阅内容失败：" + err.Error())
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return errors.New("订阅内容为空")
	}
	count, perr := countProxies(body)
	if count < 0 {
		if looksLikeBase64(string(body)) {
			return errors.New("这个链接返回的是 V2Ray/SS 分享链接格式，不是 Clash 格式。请回到机场网站选择「Clash」或「Mihomo」订阅链接。")
		}
		return errors.New("订阅内容不是有效的 Clash 配置（" + perr.Error() + "）")
	}
	if count == 0 {
		return errors.New("订阅里没有任何节点，可能已到期或流量用尽")
	}
	if err := os.WriteFile(store.SubFile(sub.ID), body, 0o644); err != nil {
		return err
	}
	sub.LastUpdate = time.Now().UnixMilli()
	sub.NodeCount = count

	// 机场常用的流量信息头：upload=..; download=..; total=..; expire=..
	if info := resp.Header.Get("subscription-userinfo"); info != "" {
		for _, kv := range strings.Split(info, ";") {
			p := strings.SplitN(strings.TrimSpace(kv), "=", 2)
			if len(p) != 2 {
				continue
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(p[1]), 64)
			if err != nil {
				continue
			}
			n := int64(f)
			switch strings.TrimSpace(p[0]) {
			case "upload":
				sub.Upload = n
			case "download":
				sub.Download = n
			case "total":
				sub.Total = n
			case "expire":
				sub.Expire = n
			}
		}
	}
	// 机场提供的名称
	if cd := resp.Header.Get("content-disposition"); cd != "" {
		if m := filenameRe.FindStringSubmatch(cd); len(m) == 2 {
			name := m[1]
			if d, err := url.QueryUnescape(name); err == nil {
				name = d
			}
			name = strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
			if sub.Name == "订阅" && strings.TrimSpace(name) != "" {
				sub.Name = name
			}
		}
	}
	if h, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("profile-update-interval"))); err == nil && h > 0 {
		sub.UpdateIntervalHours = h
	}
	return nil
}

// importSubText 从本地文件导入（.yaml）
func importSubText(store *Store, text []byte, name string) (*Subscription, error) {
	count, err := countProxies(text)
	if count < 0 {
		return nil, errors.New("文件不是有效的 Clash 配置（" + err.Error() + "）")
	}
	sub := newSubscription(newID(), name, "")
	sub.LastUpdate = time.Now().UnixMilli()
	sub.NodeCount = count
	if err := os.WriteFile(store.SubFile(sub.ID), text, 0o644); err != nil {
		return nil, err
	}
	store.UpsertSub(*sub)
	if store.Settings().SelectedSubID == "" {
		store.Update(func(s *Settings) { s.SelectedSubID = sub.ID })
	}
	return sub, nil
}
