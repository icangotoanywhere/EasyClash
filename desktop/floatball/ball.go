//go:build windows

// Package floatball 网络状态悬浮球（由 NetGateShow 并入 EasyClash Windows 版）。
//
// 屏幕上一个可拖动的小球，定时检测出口 IP 和所在地，按延迟显示绿 / 黄 / 红；双击展开详情面板，
// 面板里额外显示 EasyClash 当前的节点线路（由 Options.Node 提供）。右键菜单可调刷新间隔、不透明度。
// 窗口在自己的线程里跑消息循环，Start / Stop 可以从任意 goroutine 调用。
package floatball

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

const appName = "EasyClashFloatBall"

// ---------- config ----------

type Config struct {
	IntervalSec    int    `json:"interval_sec"`
	X              int    `json:"x"`
	Y              int    `json:"y"`
	Expanded       bool   `json:"expanded"`
	OpacityPct     int    `json:"opacity_pct"`
	UseSystemProxy bool   `json:"use_system_proxy"`
	Proxy          string `json:"proxy"`           // 可选：手动指定代理，如 http://127.0.0.1:7890
	GoodLatencyMs  int    `json:"good_latency_ms"` // 低于此值为绿色
}

var (
	cfg     = Config{IntervalSec: 30, X: -99999, Y: -99999, OpacityPct: 100, UseSystemProxy: true, GoodLatencyMs: 800}
	cfgPath string
)

func loadConfig(dataDir string) {
	cfgPath = filepath.Join(dataDir, "floatball.json")
	if b, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	if cfg.IntervalSec < 5 {
		cfg.IntervalSec = 5
	}
	if cfg.OpacityPct < 20 || cfg.OpacityPct > 100 {
		cfg.OpacityPct = 100
	}
	if cfg.GoodLatencyMs <= 0 {
		cfg.GoodLatencyMs = 800
	}
}

func saveConfig() {
	b, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(cfgPath, b, 0644)
}

// ---------- state ----------

const (
	stUnknown = iota
	stGood
	stWarn
	stBad
)

// Node 悬浮球展开面板里「线路」一行的内容
type Node struct {
	Text string // 例如 "日本 · 东京 03 · 85ms"、"未使用代理（直连）"、"其他代理软件 127.0.0.1:7897"
	Kind int    // NodeEasyClash / NodeOther / NodeNone，决定颜色
}

const (
	NodeEasyClash = iota // 正在走 EasyClash 的代理
	NodeOther            // 走的是别的代理软件
	NodeNone             // 没走代理
)

// Options 由 EasyClash 传入
type Options struct {
	DataDir string      // 悬浮球自己的设置（位置、间隔、透明度）存在这里的 floatball.json
	Node    func() Node // 取当前线路；在后台 goroutine 里调用，可以访问网络
	OnHide  func()      // 用户在右键菜单里点了「隐藏悬浮球」
}

var (
	opts     Options
	node     = Node{Text: "正在读取…", Kind: NodeNone}
	nodeBusy bool
)

var (
	mu        sync.Mutex
	last      CheckResult
	hasInfo   bool
	lastInfo  NetInfo
	status    = stUnknown
	updatedAt time.Time
	checking  bool
)

var (
	hInst     uintptr
	mainHwnd  uintptr
	scale     float32 = 1
	winX      int
	winY      int
	curW      int
	curH      int
	dragging  bool
	dragMoved bool
	dragStart POINT
	dragWin   POINT
	inDialog  bool
	inMenu    bool
	fontFam   uintptr
)

const (
	timerRefresh = 1
	timerTopmost = 2
	timerNode    = 3 // 每 5 秒刷新一次「线路」（只问本机的 EasyClash，不联网检测）
	msgUpdated   = WM_APP + 1
)

// refreshNode 在后台取一次线路，取完重画
func refreshNode() {
	mu.Lock()
	if nodeBusy || opts.Node == nil {
		mu.Unlock()
		return
	}
	nodeBusy = true
	mu.Unlock()
	go func() {
		n := opts.Node()
		mu.Lock()
		node, nodeBusy = n, false
		mu.Unlock()
		call(pPostMessageW, mainHwnd, msgUpdated, 0, 0)
	}()
}

func statusColor(s int) uint32 {
	switch s {
	case stGood:
		return 0xFF2ECC71
	case stWarn:
		return 0xFFF5C518
	case stBad:
		return 0xFFE74C3C
	}
	return 0xFF95A5A6
}

func statusText(s int) string {
	switch s {
	case stGood:
		return "连接良好"
	case stWarn:
		return "延迟较高"
	case stBad:
		return "连接失败"
	}
	return "检测中"
}

func startCheck() {
	mu.Lock()
	if checking {
		mu.Unlock()
		return
	}
	checking = true
	useSys, custom, good := cfg.UseSystemProxy, cfg.Proxy, cfg.GoodLatencyMs
	mu.Unlock()
	call(pPostMessageW, mainHwnd, msgUpdated, 0, 0)
	refreshNode()

	go func() {
		r := runCheck(useSys, custom)
		mu.Lock()
		last = r
		checking = false
		updatedAt = time.Now()
		if r.OK {
			hasInfo = true
			lastInfo = r.Info
			if r.Primary && r.Latency <= time.Duration(good)*time.Millisecond {
				status = stGood
			} else {
				status = stWarn
			}
		} else {
			status = stBad
		}
		mu.Unlock()
		call(pPostMessageW, mainHwnd, msgUpdated, 0, 0)
	}()
}

// ---------- drawing helpers (GDI+) ----------

func px(v float32) float32 { return v * scale }

func fillEllipse(g uintptr, argb uint32, x, y, w, h float32) {
	var br uintptr
	call(pGdipCreateSolidFill, uintptr(argb), uintptr(unsafe.Pointer(&br)))
	call(pGdipFillEllipse, g, br, f(x), f(y), f(w), f(h))
	call(pGdipDeleteBrush, br)
}

func drawEllipse(g uintptr, argb uint32, width, x, y, w, h float32) {
	var pen uintptr
	call(pGdipCreatePen1, uintptr(argb), f(width), 0, uintptr(unsafe.Pointer(&pen)))
	call(pGdipDrawEllipse, g, pen, f(x), f(y), f(w), f(h))
	call(pGdipDeletePen, pen)
}

func roundRectPath(x, y, w, h, r float32) uintptr {
	var path uintptr
	call(pGdipCreatePath, 0, uintptr(unsafe.Pointer(&path)))
	d := r * 2
	call(pGdipAddPathArc, path, f(x), f(y), f(d), f(d), f(180), f(90))
	call(pGdipAddPathArc, path, f(x+w-d), f(y), f(d), f(d), f(270), f(90))
	call(pGdipAddPathArc, path, f(x+w-d), f(y+h-d), f(d), f(d), f(0), f(90))
	call(pGdipAddPathArc, path, f(x), f(y+h-d), f(d), f(d), f(90), f(90))
	call(pGdipClosePathFigure, path)
	return path
}

func fillRoundRect(g uintptr, argb uint32, x, y, w, h, r float32) {
	path := roundRectPath(x, y, w, h, r)
	var br uintptr
	call(pGdipCreateSolidFill, uintptr(argb), uintptr(unsafe.Pointer(&br)))
	call(pGdipFillPath, g, br, path)
	call(pGdipDeleteBrush, br)
	call(pGdipDeletePath, path)
}

func strokeRoundRect(g uintptr, argb uint32, width, x, y, w, h, r float32) {
	path := roundRectPath(x, y, w, h, r)
	var pen uintptr
	call(pGdipCreatePen1, uintptr(argb), f(width), 0, uintptr(unsafe.Pointer(&pen)))
	call(pGdipDrawPath, g, pen, path)
	call(pGdipDeletePen, pen)
	call(pGdipDeletePath, path)
}

// align: 0 near, 1 center, 2 far
func drawText(g uintptr, s string, size float32, bold bool, argb uint32, rc RectF, align int) {
	if s == "" {
		return
	}
	style := uintptr(0)
	if bold {
		style = 1
	}
	var font, sf, br uintptr
	call(pGdipCreateFont, fontFam, f(size), style, 2 /*UnitPixel*/, uintptr(unsafe.Pointer(&font)))
	call(pGdipCreateStringFormat, 0x1000 /*NoWrap*/, 0, uintptr(unsafe.Pointer(&sf)))
	call(pGdipSetStringFormatAlign, sf, uintptr(align))
	call(pGdipSetStringFormatLineAlign, sf, 1)
	call(pGdipSetStringFormatTrimming, sf, 3 /*EllipsisCharacter*/)
	call(pGdipCreateSolidFill, uintptr(argb), uintptr(unsafe.Pointer(&br)))
	call(pGdipDrawString, g, u16p(s), ^uintptr(0) /* -1 */, font, uintptr(unsafe.Pointer(&rc)), sf, br)
	call(pGdipDeleteBrush, br)
	call(pGdipDeleteStringFormat, sf)
	call(pGdipDeleteFont, font)
}

func shortCountry(info NetInfo) string {
	if info.Country != "" && utf8.RuneCountInString(info.Country) <= 4 {
		return info.Country
	}
	if info.CountryCode != "" {
		return strings.ToUpper(info.CountryCode)
	}
	return "--"
}

func locationText(info NetInfo) string {
	parts := []string{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, p := range parts {
			if p == s {
				return
			}
		}
		parts = append(parts, s)
	}
	add(info.Country)
	add(info.Region)
	add(info.City)
	if len(parts) == 0 {
		return "未知位置"
	}
	return strings.Join(parts, " · ")
}

func intervalText(sec int) string {
	if sec%60 == 0 {
		return fmt.Sprintf("每%d分钟", sec/60)
	}
	return fmt.Sprintf("每%d秒", sec)
}

// ---------- render ----------

func windowSize() (int, int) {
	if cfg.Expanded {
		return int(px(350)), int(px(128))
	}
	return int(px(76)), int(px(76))
}

func render() {
	w, h := windowSize()

	mu.Lock()
	st, chk, info, have, r, upd := status, checking, lastInfo, hasInfo, last, updatedAt
	nd := node
	mu.Unlock()

	bi := BITMAPINFO{Header: BITMAPINFOHEADER{BiSize: 40, BiWidth: int32(w), BiHeight: -int32(h), BiPlanes: 1, BiBitCount: 32}}
	var bits uintptr
	screen := call(pGetDC, 0)
	hbm := call(pCreateDIBSection, screen, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 {
		call(pReleaseDC, 0, screen)
		return
	}

	var bmp, g uintptr
	call(pGdipCreateBitmapFromScan0, uintptr(w), uintptr(h), uintptr(w*4), 0xE200B /*32bppPARGB*/, bits, uintptr(unsafe.Pointer(&bmp)))
	call(pGdipGetImageGraphicsContext, bmp, uintptr(unsafe.Pointer(&g)))
	call(pGdipSetSmoothingMode, g, 4)
	call(pGdipSetTextRenderingHint, g, 4)
	call(pGdipGraphicsClear, g, 0)

	col := statusColor(st)
	glow := (col & 0x00FFFFFF) | 0x55000000
	m := px(6)

	if !cfg.Expanded {
		D := float32(w) - 2*m
		// soft shadow
		for i := float32(3); i >= 1; i-- {
			fillEllipse(g, 0x12000000, m-px(i), m-px(i)+px(1.5), D+2*px(i), D+2*px(i))
		}
		fillEllipse(g, 0xF51C232D, m, m, D, D)
		drawEllipse(g, glow, px(6), m+px(3), m+px(3), D-px(6), D-px(6))
		drawEllipse(g, col, px(3), m+px(3), m+px(3), D-px(6), D-px(6))

		label, sub := "…", "检测中"
		if have {
			label = "--"
			if info.CountryCode != "" {
				label = strings.ToUpper(info.CountryCode)
			}
		}
		if !chk || have {
			switch {
			case st == stBad:
				sub = "断开"
			case r.OK:
				sub = fmt.Sprintf("%dms", r.Latency.Milliseconds())
			}
		}
		lsize := float32(17)
		drawText(g, label, px(lsize), true, 0xFFFFFFFF, RectF{m + px(4), m + D*0.20, D - px(8), D * 0.36}, 1)
		drawText(g, sub, px(10.5), false, 0xFFB7C2CE, RectF{m, m + D*0.54, D, D * 0.24}, 1)
		if chk {
			fillEllipse(g, 0xE6FFFFFF, m+D*0.76, m+D*0.10, px(7), px(7))
		}
	} else {
		W, H := float32(w)-2*m, float32(h)-2*m
		for i := float32(3); i >= 1; i-- {
			fillRoundRect(g, 0x12000000, m-px(i), m-px(i)+px(1.5), W+2*px(i), H+2*px(i), px(16)+px(i))
		}
		fillRoundRect(g, 0xF51C232D, m, m, W, H, px(16))
		strokeRoundRect(g, (col&0x00FFFFFF)|0x66000000, px(1.2), m+px(0.6), m+px(0.6), W-px(1.2), H-px(1.2), px(15.5))

		// status ball
		bd := px(56)
		bx, by := m+px(14), m+(H-bd)/2
		fillEllipse(g, 0xFF262F3B, bx, by, bd, bd)
		drawEllipse(g, glow, px(6), bx+px(3), by+px(3), bd-px(6), bd-px(6))
		drawEllipse(g, col, px(3), bx+px(3), by+px(3), bd-px(6), bd-px(6))
		code := "--"
		if have && info.CountryCode != "" {
			code = strings.ToUpper(info.CountryCode)
		}
		drawText(g, code, px(15), true, 0xFFFFFFFF, RectF{bx, by, bd, bd}, 1)
		if chk {
			fillEllipse(g, 0xE6FFFFFF, bx+bd*0.76, by+bd*0.06, px(7), px(7))
		}

		x0 := bx + bd + px(14)
		tw := m + W - px(12) - x0
		y := m + px(9)

		line1, line2, line3 := "正在检测出口…", "", ""
		line3col := uint32(0xFFAEB9C5)
		if have {
			line1 = locationText(info)
			line2 = "IP  " + info.IP
			line3 = info.ISP
			if info.AS != "" && !strings.Contains(info.ISP, info.AS) {
				as := info.AS
				if i := strings.IndexByte(as, ' '); i > 0 {
					as = as[:i]
				}
				line3 = strings.TrimSpace(line3 + "  " + as)
			}
		}
		if st == stBad {
			line3 = r.Err
			line3col = 0xFFFF8A7A
			if !have {
				line1 = "无法获取出口信息"
			}
		}
		drawText(g, line1, px(15), true, 0xFFFFFFFF, RectF{x0, y, tw, px(22)}, 0)
		drawText(g, line2, px(12), false, 0xFFDDE3EA, RectF{x0, y + px(23), tw, px(18)}, 0)
		drawText(g, line3, px(12), false, line3col, RectF{x0, y + px(41), tw, px(18)}, 0)

		// EasyClash 线路
		ncol := uint32(0xFF9AA7B4)
		switch nd.Kind {
		case NodeEasyClash:
			ncol = 0xFF7EE2A8
		case NodeOther:
			ncol = 0xFFF5C518
		}
		drawText(g, "线路  "+nd.Text, px(12), false, ncol, RectF{x0, y + px(60), tw, px(18)}, 0)

		// footer: status dot + text
		fy := y + px(81)
		fillEllipse(g, col, x0, fy+px(5), px(8), px(8))
		foot := statusText(st)
		if chk && !have {
			foot = "检测中"
		}
		if r.OK && st != stBad {
			foot += fmt.Sprintf(" %dms", r.Latency.Milliseconds())
		}
		if !upd.IsZero() {
			foot += " · " + upd.Format("15:04:05")
		}
		foot += " · " + intervalText(cfg.IntervalSec)
		drawText(g, foot, px(11), false, 0xFF9AA7B4, RectF{x0 + px(13), fy, tw - px(13), px(18)}, 0)
	}

	call(pGdipDeleteGraphics, g)
	call(pGdipDisposeImage, bmp)

	// keep the window on its monitor
	clampPos(w, h)

	mem := call(pCreateCompatibleDC, screen)
	old := call(pSelectObject, mem, hbm)
	pt := POINT{int32(winX), int32(winY)}
	sz := SIZE{int32(w), int32(h)}
	src := POINT{}
	blend := BLENDFUNCTION{0, 0, byte(cfg.OpacityPct * 255 / 100), AC_SRC_ALPHA}
	call(pUpdateLayeredWindow, mainHwnd, screen, uintptr(unsafe.Pointer(&pt)), uintptr(unsafe.Pointer(&sz)),
		mem, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ULW_ALPHA)
	call(pSelectObject, mem, old)
	call(pDeleteDC, mem)
	call(pDeleteObject, hbm)
	call(pReleaseDC, 0, screen)
	curW, curH = w, h
}

func workAreaAt(x, y int) RECT {
	pt := uintptr(uint32(int32(x))) | uintptr(uint32(int32(y)))<<32
	hm := call(pMonitorFromPoint, pt, 2 /*NEAREST*/)
	mi := MONITORINFO{CbSize: uint32(unsafe.Sizeof(MONITORINFO{}))}
	call(pGetMonitorInfoW, hm, uintptr(unsafe.Pointer(&mi)))
	return mi.Work
}

func clampPos(w, h int) {
	wa := workAreaAt(winX+w/2, winY+h/2)
	if winX+w > int(wa.Right) {
		winX = int(wa.Right) - w
	}
	if winY+h > int(wa.Bottom) {
		winY = int(wa.Bottom) - h
	}
	if winX < int(wa.Left) {
		winX = int(wa.Left)
	}
	if winY < int(wa.Top) {
		winY = int(wa.Top)
	}
}

func toggleExpand() {
	oldW := curW
	wa := workAreaAt(winX+curW/2, winY+curH/2)
	rightSide := winX+oldW/2 > int(wa.Left+wa.Right)/2
	cfg.Expanded = !cfg.Expanded
	nw, _ := windowSize()
	if rightSide { // keep the right edge anchored when docked on the right
		winX = winX + oldW - nw
	}
	render()
	cfg.X, cfg.Y = winX, winY
	saveConfig()
}

// ---------- clipboard ----------

func copyText(s string) {
	v, _ := syscall.UTF16FromString(s)
	if call(pOpenClipboard, mainHwnd) == 0 {
		return
	}
	defer call(pCloseClipboard)
	call(pEmptyClipboard)
	h := call(pGlobalAlloc, 2 /*GMEM_MOVEABLE*/, uintptr(len(v)*2))
	if h == 0 {
		return
	}
	p := call(pGlobalLock, h)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(v)), v)
	call(pGlobalUnlock, h)
	call(pSetClipboardData, 13 /*CF_UNICODETEXT*/, h)
}

// ---------- context menu ----------

const (
	idRefresh = 100 + iota
	idExpand
	idCustom
	idSysProxy
	idCopy
	idExit
	idIntervalBase = 200
	idOpacityBase  = 300
)

var intervalPresets = []int{10, 15, 30, 60, 120, 300}
var opacityPresets = []int{100, 90, 75, 60, 45}

func checked(b bool) uintptr {
	if b {
		return MF_CHECKED
	}
	return 0
}

func showMenu() {
	mu.Lock()
	info, have, r := lastInfo, hasInfo, last
	mu.Unlock()

	m := call(pCreatePopupMenu)
	head := "出口：检测中…"
	if have {
		head = "出口：" + info.IP + "  " + shortCountry(info)
	}
	call(pAppendMenuW, m, MF_STRING|MF_GRAYED, 0, u16p(head))
	call(pAppendMenuW, m, MF_SEPARATOR, 0, 0)
	call(pAppendMenuW, m, MF_STRING, idRefresh, u16p("立即刷新"))
	call(pAppendMenuW, m, MF_STRING|checked(cfg.Expanded), idExpand, u16p("显示详情面板（双击切换）"))

	sub := call(pCreatePopupMenu)
	isPreset := false
	for i, s := range intervalPresets {
		if s == cfg.IntervalSec {
			isPreset = true
		}
		call(pAppendMenuW, sub, MF_STRING|checked(s == cfg.IntervalSec), uintptr(idIntervalBase+i), u16p(fmt.Sprintf("%d 秒", s)))
	}
	call(pAppendMenuW, sub, MF_SEPARATOR, 0, 0)
	call(pAppendMenuW, sub, MF_STRING|checked(!isPreset), idCustom, u16p(fmt.Sprintf("自定义…（当前 %d 秒）", cfg.IntervalSec)))
	call(pAppendMenuW, m, MF_POPUP, sub, u16p(fmt.Sprintf("刷新间隔：%d 秒", cfg.IntervalSec)))

	op := call(pCreatePopupMenu)
	for i, o := range opacityPresets {
		call(pAppendMenuW, op, MF_STRING|checked(o == cfg.OpacityPct), uintptr(idOpacityBase+i), u16p(fmt.Sprintf("%d%%", o)))
	}
	call(pAppendMenuW, m, MF_POPUP, op, u16p("不透明度"))

	viaLbl := "通过系统代理检测"
	if r.Via != "" {
		viaLbl += "（当前：" + r.Via + "）"
	}
	call(pAppendMenuW, m, MF_STRING|checked(cfg.UseSystemProxy), idSysProxy, u16p(viaLbl))
	call(pAppendMenuW, m, MF_STRING, idCopy, u16p("复制出口信息"))
	call(pAppendMenuW, m, MF_SEPARATOR, 0, 0)
	call(pAppendMenuW, m, MF_STRING, idExit, u16p("隐藏悬浮球（可在 EasyClash「高级」页重新打开）"))

	var pt POINT
	call(pGetCursorPos, uintptr(unsafe.Pointer(&pt)))
	call(pSetForegroundWindow, mainHwnd)
	inMenu = true
	cmd := int(call(pTrackPopupMenu, m, TPM_RETURNCMD|TPM_RIGHTBUTTON, uintptr(pt.X), uintptr(pt.Y), 0, mainHwnd, 0))
	inMenu = false
	call(pPostMessageW, mainHwnd, 0, 0, 0)
	call(pDestroyMenu, m)

	switch {
	case cmd == idRefresh:
		startCheck()
		refreshNode()
	case cmd == idExpand:
		toggleExpand()
	case cmd >= idIntervalBase && cmd < idIntervalBase+len(intervalPresets):
		setInterval(intervalPresets[cmd-idIntervalBase])
	case cmd == idCustom:
		if n, ok := askInterval(cfg.IntervalSec); ok {
			setInterval(n)
		}
	case cmd >= idOpacityBase && cmd < idOpacityBase+len(opacityPresets):
		cfg.OpacityPct = opacityPresets[cmd-idOpacityBase]
		saveConfig()
		render()
	case cmd == idSysProxy:
		cfg.UseSystemProxy = !cfg.UseSystemProxy
		saveConfig()
		startCheck()
	case cmd == idCopy:
		if have {
			s := fmt.Sprintf("出口IP: %s\r\n位置: %s\r\n运营商: %s %s\r\n状态: %s", info.IP, locationText(info), info.ISP, info.AS, statusText(status))
			if r.OK {
				s += fmt.Sprintf(" (%dms)", r.Latency.Milliseconds())
			}
			mu.Lock()
			s += "\r\n线路: " + node.Text
			mu.Unlock()
			copyText(s)
		}
	case cmd == idExit:
		call(pDestroyWindow, mainHwnd)
		if opts.OnHide != nil {
			go opts.OnHide()
		}
	}
}

func setInterval(n int) {
	cfg.IntervalSec = n
	saveConfig()
	call(pSetTimer, mainHwnd, timerRefresh, uintptr(n*1000), 0)
	render()
}

// ---------- custom interval dialog ----------

var (
	dlgHwnd   uintptr
	dlgEdit   uintptr
	dlgDone   bool
	dlgResult int
	dlgOK     bool
)

func dlgProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case WM_COMMAND:
		switch loword(wp) {
		case 1: // OK
			buf := make([]uint16, 32)
			call(pGetWindowTextW, dlgEdit, uintptr(unsafe.Pointer(&buf[0])), 32)
			n, err := strconv.Atoi(strings.TrimSpace(syscall.UTF16ToString(buf)))
			if err != nil || n < 5 || n > 86400 {
				call(pMessageBoxW, hwnd, u16p("请输入 5 ~ 86400 之间的整数秒数。"), u16p(appName), 0x30)
				call(pSetFocus, dlgEdit)
				return 0
			}
			dlgResult, dlgOK, dlgDone = n, true, true
			call(pDestroyWindow, hwnd)
			return 0
		case 2: // Cancel
			dlgDone = true
			call(pDestroyWindow, hwnd)
			return 0
		}
	case WM_CLOSE:
		dlgDone = true
		call(pDestroyWindow, hwnd)
		return 0
	}
	return call(pDefWindowProcW, hwnd, msg, wp, lp)
}

func askInterval(cur int) (int, bool) {
	if inDialog {
		return 0, false
	}
	inDialog = true
	defer func() { inDialog = false }()

	s := func(v int) uintptr { return uintptr(int(float32(v) * scale)) }
	dw, dh := int(float32(330)*scale), int(float32(158)*scale)
	sw, sh := int(call(pGetSystemMetrics, 0)), int(call(pGetSystemMetrics, 1))
	dlgDone, dlgOK = false, false
	dlgHwnd = call(pCreateWindowExW, WS_EX_TOPMOST|WS_EX_DLGMODALFRAME, u16p(appName+"Dlg"), u16p("设置刷新间隔"),
		WS_POPUP|WS_CAPTION|WS_SYSMENU|WS_VISIBLE, uintptr((sw-dw)/2), uintptr((sh-dh)/2), uintptr(dw), uintptr(dh),
		mainHwnd, 0, hInst, 0)
	if dlgHwnd == 0 {
		return 0, false
	}
	font := call(pCreateFontW, uintptr(int32(-int(14*scale))), 0, 0, 0, 400, 0, 0, 0, 1 /*DEFAULT_CHARSET*/, 0, 0, 5 /*CLEARTYPE*/, 0, u16p("Microsoft YaHei UI"))
	mk := func(exStyle uintptr, class, text string, style uintptr, x, y, w, h int, id uintptr) uintptr {
		hw := call(pCreateWindowExW, exStyle, u16p(class), u16p(text), WS_CHILD|WS_VISIBLE|style,
			s(x), s(y), s(w), s(h), dlgHwnd, id, hInst, 0)
		call(pSendMessageW, hw, WM_SETFONT, font, 1)
		return hw
	}
	mk(0, "STATIC", "连接状态刷新间隔（秒，5 ~ 86400）：", 0, 16, 14, 290, 22, 0)
	dlgEdit = mk(WS_EX_CLIENTEDGE, "EDIT", strconv.Itoa(cur), WS_TABSTOP|0x2000 /*ES_NUMBER*/ |0x80 /*AUTOHSCROLL*/, 16, 42, 290, 26, 10)
	mk(0, "BUTTON", "确定", WS_TABSTOP|1 /*BS_DEFPUSHBUTTON*/, 126, 80, 86, 30, 1)
	mk(0, "BUTTON", "取消", WS_TABSTOP, 220, 80, 86, 30, 2)
	call(pSetForegroundWindow, dlgHwnd)
	call(pSetFocus, dlgEdit)
	call(pSendMessageW, dlgEdit, EM_SETSEL, 0, ^uintptr(0))

	var msg MSG
	for !dlgDone {
		r := int32(call(pGetMessageW, uintptr(unsafe.Pointer(&msg)), 0, 0, 0))
		if r <= 0 {
			call(pPostQuitMessage, 0)
			break
		}
		if call(pIsDialogMessageW, dlgHwnd, uintptr(unsafe.Pointer(&msg))) == 0 {
			call(pTranslateMessage, uintptr(unsafe.Pointer(&msg)))
			call(pDispatchMessageW, uintptr(unsafe.Pointer(&msg)))
		}
	}
	call(pDeleteObject, font)
	return dlgResult, dlgOK
}

// ---------- main window ----------

func wndProc(hwnd, msg, wp, lp uintptr) uintptr {
	switch msg {
	case WM_MOUSEACTIVATE:
		return MA_NOACTIVATE
	case WM_LBUTTONDOWN:
		dragging, dragMoved = true, false
		call(pGetCursorPos, uintptr(unsafe.Pointer(&dragStart)))
		dragWin = POINT{int32(winX), int32(winY)}
		call(pSetCapture, hwnd)
		return 0
	case WM_MOUSEMOVE:
		if dragging {
			var p POINT
			call(pGetCursorPos, uintptr(unsafe.Pointer(&p)))
			dx, dy := p.X-dragStart.X, p.Y-dragStart.Y
			if !dragMoved && (dx*dx+dy*dy) > 9 {
				dragMoved = true
			}
			if dragMoved {
				winX, winY = int(dragWin.X+dx), int(dragWin.Y+dy)
				call(pSetWindowPos, hwnd, 0, uintptr(winX), uintptr(winY), 0, 0, SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE)
			}
		}
		return 0
	case WM_LBUTTONUP:
		if dragging {
			dragging = false
			call(pReleaseCapture)
			if dragMoved {
				clampPos(curW, curH)
				call(pSetWindowPos, hwnd, 0, uintptr(winX), uintptr(winY), 0, 0, SWP_NOSIZE|SWP_NOZORDER|SWP_NOACTIVATE)
				cfg.X, cfg.Y = winX, winY
				saveConfig()
			}
		}
		return 0
	case WM_LBUTTONDBLCLK:
		toggleExpand()
		return 0
	case WM_RBUTTONUP:
		showMenu()
		return 0
	case WM_TIMER:
		switch wp {
		case timerRefresh:
			startCheck()
		case timerNode:
			refreshNode()
		case timerTopmost:
			if !dragging && !inDialog && !inMenu {
				call(pSetWindowPos, hwnd, HWND_TOPMOST, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
			}
		}
		return 0
	case msgUpdated:
		if !dragging {
			render()
		}
		return 0
	case WM_DISPLAYCHANGE:
		render()
		return 0
	case WM_DESTROY:
		saveConfig()
		call(pPostQuitMessage, 0)
		return 0
	}
	return call(pDefWindowProcW, hwnd, msg, wp, lp)
}

var (
	runMu     sync.Mutex
	running   bool
	initOnce  sync.Once
	initErr   string
	wndProcCb uintptr
	dlgProcCb uintptr
)

// Running 悬浮球是否正在显示
func Running() bool {
	runMu.Lock()
	defer runMu.Unlock()
	return running
}

// Start 显示悬浮球（已经在显示时什么也不做）
func Start(o Options) {
	runMu.Lock()
	if running {
		runMu.Unlock()
		return
	}
	running = true
	runMu.Unlock()
	opts = o
	go func() {
		defer func() {
			runMu.Lock()
			running = false
			runMu.Unlock()
		}()
		run()
	}()
}

// Stop 关掉悬浮球
func Stop() {
	runMu.Lock()
	r := running
	runMu.Unlock()
	if r && mainHwnd != 0 {
		call(pPostMessageW, mainHwnd, WM_CLOSE, 0, 0)
	}
}

// globalInit 进程内只做一次：DPI、GDI+、字体、窗口类
func globalInit() {
	call(pSetProcessDPIAware) // EasyClash 已在清单里声明 DPI 感知，这里失败也没关系
	sdc := call(pGetDC, 0)
	dpi := call(pGetDeviceCaps, sdc, 88 /*LOGPIXELSX*/)
	call(pReleaseDC, 0, sdc)
	if dpi > 0 {
		scale = float32(dpi) / 96
	}

	var token uintptr
	in := gdiplusStartupInput{Version: 1}
	if call(pGdiplusStartup, uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&in)), 0) != 0 {
		initErr = "GDI+ 初始化失败"
		return
	}
	for _, name := range []string{"Microsoft YaHei UI", "Microsoft YaHei", "Segoe UI"} {
		if call(pGdipCreateFontFamilyFromName, u16p(name), 0, uintptr(unsafe.Pointer(&fontFam))) == 0 && fontFam != 0 {
			break
		}
		fontFam = 0
	}
	if fontFam == 0 {
		call(pGdipGetGenericFontFamilySansSerif, uintptr(unsafe.Pointer(&fontFam)))
	}

	hInst = call(pGetModuleHandleW, 0)
	icon := call(pLoadIconW, hInst, 1)
	cursor := call(pLoadCursorW, 0, 32649 /*IDC_HAND*/)
	wndProcCb = syscall.NewCallback(wndProc)
	dlgProcCb = syscall.NewCallback(dlgProc)

	wc := WNDCLASSEXW{Style: CS_DBLCLKS, LpfnWndProc: wndProcCb, HInstance: hInst,
		HIcon: icon, HIconSm: icon, HCursor: cursor, LpszClassName: u16(appName)}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	call(pRegisterClassExW, uintptr(unsafe.Pointer(&wc)))

	dc := WNDCLASSEXW{LpfnWndProc: dlgProcCb, HInstance: hInst, HIcon: icon, HIconSm: icon,
		HCursor: call(pLoadCursorW, 0, 32512), HbrBackground: 16 /*COLOR_BTNFACE+1*/, LpszClassName: u16(appName + "Dlg")}
	dc.CbSize = uint32(unsafe.Sizeof(dc))
	call(pRegisterClassExW, uintptr(unsafe.Pointer(&dc)))
}

// run 在当前线程创建窗口并跑消息循环，直到窗口被销毁
func run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	initOnce.Do(globalInit)
	if initErr != "" {
		return
	}
	loadConfig(opts.DataDir)
	mu.Lock()
	hasInfo, status, checking, nodeBusy = false, stUnknown, false, false
	node = Node{Text: "正在读取…", Kind: NodeNone}
	mu.Unlock()

	w, h := windowSize()
	if cfg.X == -99999 && cfg.Y == -99999 {
		var wa RECT
		call(user32.NewProc("SystemParametersInfoW"), 0x30 /*SPI_GETWORKAREA*/, 0, uintptr(unsafe.Pointer(&wa)), 0)
		winX = int(wa.Right) - w - int(px(24))
		winY = int(wa.Top) + int(px(120))
	} else {
		winX, winY = cfg.X, cfg.Y
	}

	mainHwnd = call(pCreateWindowExW, WS_EX_LAYERED|WS_EX_TOPMOST|WS_EX_TOOLWINDOW, u16p(appName), u16p("EasyClash 网络状态"),
		WS_POPUP, uintptr(winX), uintptr(winY), uintptr(w), uintptr(h), 0, 0, hInst, 0)
	if mainHwnd == 0 {
		return
	}
	render()
	call(pShowWindow, mainHwnd, 4 /*SW_SHOWNOACTIVATE*/)
	cfg.X, cfg.Y = winX, winY
	saveConfig()

	call(pSetTimer, mainHwnd, timerRefresh, uintptr(cfg.IntervalSec*1000), 0)
	call(pSetTimer, mainHwnd, timerTopmost, 3000, 0)
	call(pSetTimer, mainHwnd, timerNode, 5000, 0)
	startCheck()

	var msg MSG
	for {
		r := int32(call(pGetMessageW, uintptr(unsafe.Pointer(&msg)), 0, 0, 0))
		if r <= 0 {
			break
		}
		call(pTranslateMessage, uintptr(unsafe.Pointer(&msg)))
		call(pDispatchMessageW, uintptr(unsafe.Pointer(&msg)))
	}
	mainHwnd = 0
}
