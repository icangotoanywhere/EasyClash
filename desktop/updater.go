package main

// 检查更新与原地升级（Windows / macOS 共用部分）。
//
// 版本信息来自 GitHub Releases 接口（最近 3 个正式版，含发布说明）。网络线路和下载订阅一样：
// 已连接时先走本软件的代理，其次系统里别的代理，最后直连。
// 升级：下载本平台的安装包 → 平台相关的 applyUpdate 原地替换程序（update_windows.go / update_darwin.go）
// → 退出并重新启动新版本。订阅和设置在数据目录里，不受影响。

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const releasesAPI = "https://api.github.com/repos/icangotoanywhere/EasyClash/releases?per_page=3"

// githubHint 检查和下载都要访问 GitHub，国内直连常常不通：失败时都带上这句提示
const githubHint = "（检查和下载新版本都要访问 GitHub：请先连接 EasyClash，或确认这台电脑能打开 github.com，再试一次）"

type releaseInfo struct {
	Version string `json:"version"` // 不带 v，例如 1.0.31
	Name    string `json:"name"`
	Date    string `json:"date"` // 2026-11-14
	Notes   string `json:"notes"`
	URL     string `json:"url"` // 发布页
	asset   string // 本平台安装包的下载地址
	size    int64
}

type updateState struct {
	mu         sync.Mutex
	releases   []releaseInfo
	checkedAt  time.Time
	checkErr   string
	busy       bool   // 正在下载 / 安装
	stage      string // 显示用：正在下载… / 正在安装…
	installErr string
	total      atomic.Int64
	done       atomic.Int64
}

var upd updateState

// newerThan a 是否比 b 新（三段数字比较；格式不对的当作旧）
func newerThan(a, b string) bool {
	pa, pb := splitVer(a), splitVer(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func splitVer(v string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3) {
		n, _ := strconv.Atoi(strings.TrimFunc(p, func(r rune) bool { return r < '0' || r > '9' }))
		out[i] = n
	}
	return out
}

// httpGet 按线路依次尝试；返回第一个成功的响应（调用方负责关闭）
func (a *App) httpGet(u string, timeout time.Duration) (*http.Response, error) {
	var tried []string
	for _, rt := range a.subRoutes() {
		tr := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
		if rt.Proxy != "" {
			if pu, err := url.Parse(rt.Proxy); err == nil {
				tr.Proxy = http.ProxyURL(pu)
			}
		}
		c := &http.Client{Timeout: timeout, Transport: tr}
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "EasyClash/"+appVersion)
		resp, err := c.Do(req)
		if err == nil && resp.StatusCode == 200 {
			return resp, nil
		}
		if err == nil {
			resp.Body.Close()
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		tried = append(tried, rt.Label+"："+shortNetErr(err))
	}
	return nil, errors.New(strings.Join(tried, "；"))
}

// checkUpdate 取最近 3 个版本的信息
func (a *App) checkUpdate() error {
	resp, err := a.httpGet(releasesAPI, 30*time.Second)
	if err != nil {
		upd.mu.Lock()
		upd.checkErr, upd.checkedAt = "检查更新失败："+err.Error()+githubHint, time.Now()
		upd.mu.Unlock()
		return err
	}
	defer resp.Body.Close()
	var raw []struct {
		Tag        string `json:"tag_name"`
		Name       string `json:"name"`
		Body       string `json:"body"`
		HTMLURL    string `json:"html_url"`
		Published  string `json:"published_at"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		upd.mu.Lock()
		upd.checkErr, upd.checkedAt = "检查更新失败：返回的数据看不懂", time.Now()
		upd.mu.Unlock()
		return err
	}
	var list []releaseInfo
	for _, r := range raw {
		if r.Draft || r.Prerelease {
			continue
		}
		ri := releaseInfo{Version: strings.TrimPrefix(r.Tag, "v"), Name: r.Name, Notes: strings.TrimSpace(r.Body), URL: r.HTMLURL}
		if len(r.Published) >= 10 {
			ri.Date = r.Published[:10]
		}
		want := updateAssetName(ri.Version)
		for _, as := range r.Assets {
			if as.Name == want {
				ri.asset, ri.size = as.URL, as.Size
			}
		}
		list = append(list, ri)
	}
	upd.mu.Lock()
	upd.releases, upd.checkErr, upd.checkedAt = list, "", time.Now()
	upd.mu.Unlock()
	if len(list) > 0 && newerThan(list[0].Version, appVersion) {
		appendLog("[APP] 发现新版本 v" + list[0].Version + "（当前 v" + appVersion + "）")
	}
	return nil
}

// autoCheckUpdates 启动 1 分钟后检查一次，之后每 12 小时一次
func (a *App) autoCheckUpdates() {
	time.Sleep(time.Minute)
	for {
		_ = a.checkUpdate()
		time.Sleep(12 * time.Hour)
	}
}

// updateJSON 给界面的状态
func updateJSON() map[string]any {
	upd.mu.Lock()
	defer upd.mu.Unlock()
	latest := ""
	if len(upd.releases) > 0 {
		latest = upd.releases[0].Version
	}
	checked := ""
	if !upd.checkedAt.IsZero() {
		checked = upd.checkedAt.Format("2006-01-02 15:04")
	}
	rels := upd.releases
	if rels == nil {
		rels = []releaseInfo{}
	}
	return map[string]any{
		"current":    appVersion,
		"latest":     latest,
		"hasNew":     latest != "" && newerThan(latest, appVersion),
		"canInstall": len(upd.releases) > 0 && upd.releases[0].asset != "",
		"releases":   rels,
		"checkedAt":  checked,
		"checkErr":   upd.checkErr,
		"busy":       upd.busy,
		"stage":      upd.stage,
		"done":       upd.done.Load(),
		"total":      upd.total.Load(),
		"installErr": upd.installErr,
	}
}

// installUpdate 下载最新版并原地替换。成功返回 nil，调用方接着退出并重新启动
func (a *App) installUpdate() error {
	upd.mu.Lock()
	if upd.busy {
		upd.mu.Unlock()
		return errors.New("正在升级中")
	}
	if len(upd.releases) == 0 || upd.releases[0].asset == "" {
		upd.mu.Unlock()
		return errors.New("还没有检查到可下载的新版本")
	}
	rel := upd.releases[0]
	upd.busy, upd.stage, upd.installErr = true, "正在下载…", ""
	upd.mu.Unlock()
	upd.done.Store(0)
	upd.total.Store(rel.size)

	err := func() error {
		dir := filepath.Join(a.dataDir, "update")
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		file := filepath.Join(dir, updateAssetName(rel.Version))
		resp, err := a.httpGet(rel.asset, 30*time.Minute)
		if err != nil {
			return errors.New("下载失败：" + err.Error() + githubHint)
		}
		defer resp.Body.Close()
		if resp.ContentLength > 0 {
			upd.total.Store(resp.ContentLength)
		}
		f, err := os.Create(file)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, &countingReader{r: resp.Body})
		f.Close()
		if err != nil {
			return errors.New("下载中断：" + shortNetErr(err) + githubHint)
		}
		if t := upd.total.Load(); t > 0 && upd.done.Load() != t {
			return errors.New("下载不完整，请重试" + githubHint)
		}
		upd.mu.Lock()
		upd.stage = "正在安装…"
		upd.mu.Unlock()
		appendLog("[APP] 已下载 v" + rel.Version + "，开始原地升级")
		return applyUpdate(file, dir)
	}()

	upd.mu.Lock()
	upd.busy = false
	if err != nil {
		upd.stage, upd.installErr = "", err.Error()
		appendLog("[APP] 升级失败：" + err.Error())
	} else {
		upd.stage = "升级完成，正在重新启动…"
	}
	upd.mu.Unlock()
	return err
}

type countingReader struct{ r io.Reader }

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	upd.done.Add(int64(n))
	return n, err
}
