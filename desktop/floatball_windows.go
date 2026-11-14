package main

// 网络状态悬浮球（由 NetGateShow 并入，代码在 floatball 子包）：高级页开关 FloatBall 控制显示。
// 这里负责告诉悬浮球「线路」那一行显示什么：
//   - EasyClash 已连接，且流量确实经过它（TUN 模式，或系统代理指向本软件端口）→ 当前节点 + 延迟
//   - 系统代理指向别的地址 → 其他代理软件
//   - 都不是 → 未使用代理

import (
	"strings"

	"easyclash/desktop/floatball"
)

const floatBallSupported = true

func startFloatBall(a *App) {
	floatball.Start(floatball.Options{
		DataDir: a.dataDir,
		Node:    a.floatNode,
		OnHide:  func() { a.store.Update(func(s *Settings) { s.FloatBall = false }) },
	})
}

func stopFloatBall() { floatball.Stop() }

// applyFloatBall 按设置显示 / 隐藏
func applyFloatBall(a *App) {
	if a.store.Settings().FloatBall {
		startFloatBall(a)
	} else {
		stopFloatBall()
	}
}

func (a *App) floatNode() floatball.Node {
	st := a.store.Settings()
	ours := ""
	if st.MixedPort > 0 {
		ours = ourProxyServer(st.MixedPort)
	}
	a.mu.Lock()
	status, tun := a.status, a.tunOn
	a.mu.Unlock()
	_, sysHP := systemProxyURL()

	inUse := status == stConnected && (tun || (ours != "" && sysHP == ours))
	if !inUse {
		switch {
		case sysHP != "" && sysHP != ours:
			return floatball.Node{Text: "其他代理软件", Detail: "未使用 EasyClash，系统代理指向 " + sysHP, Kind: floatball.NodeOther}
		case status == stConnected:
			return floatball.Node{Text: "未使用代理", Detail: "EasyClash 已连接，但系统代理没有指向它", Kind: floatball.NodeNone}
		default:
			return floatball.Node{Text: "未使用代理", Detail: "直连上网", Kind: floatball.NodeNone}
		}
	}
	if st.Mode == "direct" {
		return floatball.Node{Text: "EasyClash · 直连模式", Detail: "不走代理", Kind: floatball.NodeNone}
	}

	api := a.API()
	mainGroup, regions := a.runtime()
	if api == nil {
		return floatball.Node{Text: "EasyClash", Kind: floatball.NodeEasyClash}
	}
	px, err := api.Proxies()
	if err != nil {
		return floatball.Node{Text: "EasyClash", Detail: "读取节点失败", Kind: floatball.NodeEasyClash}
	}
	start := mainGroup
	if st.Mode == "global" {
		start = "GLOBAL"
	}
	// 沿策略组一路找到真实节点
	name, delay := start, 0
	for i := 0; i < 10; i++ {
		p, ok := px[name]
		if !ok {
			break
		}
		delay = p.Delay
		if p.Now == "" {
			break
		}
		name = p.Now
	}
	detail := plainName(name)
	if name == "DIRECT" {
		detail = "直连"
	}
	if delay > 0 {
		detail += " · " + itoa(delay) + "ms"
	}
	line := "EasyClash"
	for _, r := range regions {
		if r.Code == st.LockedRegion {
			line += " · " + r.DisplayName() + "（已锁定）"
		}
	}
	return floatball.Node{Text: line, Detail: detail, Kind: floatball.NodeEasyClash}
}

// plainName 去掉节点名里的国旗表情（悬浮球的字体画不出国旗，会显示成方块）
func plainName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
