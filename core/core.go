// Package core is a minimal Android bridge for the mihomo core.
//
// Design: the Go side only starts/stops the core and protects sockets.
// Everything else (proxy groups, node selection, delay tests, connections, logs)
// is done from Android through the core's own RESTful API (external-controller),
// so this file stays tiny and survives mihomo upgrades.
//
// Built with gomobile bind into core.aar.
package core

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/geodata"
	"github.com/metacubex/mihomo/component/updater"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/log"
)

// SocketProtector is implemented on the Android side: it calls VpnService.protect(fd)
// so the core's own outbound sockets (to proxy servers) are not looped back into the VPN.
type SocketProtector interface {
	Protect(fd int) bool
}

var (
	mu        sync.Mutex
	running   bool
	protector SocketProtector

	logMu       sync.Mutex
	startupLogs []string
	logCaptured bool
)

// captureLogs subscribes to the core's log stream once, keeping the most recent
// lines so that errors emitted during startup (before the REST API is reachable)
// can be read back from Android with StartupLog().
func captureLogs() {
	logMu.Lock()
	if logCaptured {
		logMu.Unlock()
		return
	}
	logCaptured = true
	logMu.Unlock()
	sub := log.Subscribe()
	go func() {
		for ev := range sub {
			line := fmt.Sprintf("[%s] %s", strings.ToUpper(ev.Type()), ev.Payload)
			logMu.Lock()
			startupLogs = append(startupLogs, line)
			if len(startupLogs) > 400 {
				startupLogs = startupLogs[len(startupLogs)-400:]
			}
			logMu.Unlock()
		}
	}()
}

// StartupLog returns the log lines captured since the last Start (newline separated).
func StartupLog() string {
	logMu.Lock()
	defer logMu.Unlock()
	return strings.Join(startupLogs, "\n")
}

// Version returns the mihomo core version string.
func Version() string {
	return C.Version
}

// SetProtector registers the socket-protect callback. Call once before Start.
func SetProtector(p SocketProtector) {
	mu.Lock()
	defer mu.Unlock()
	protector = p
	if p == nil {
		dialer.DefaultSocketHook = nil
		return
	}
	// mihomo 在每次建立出站 TCP/UDP 连接时都会调用这个钩子
	dialer.DefaultSocketHook = func(network, address string, conn syscall.RawConn) error {
		return conn.Control(func(fd uintptr) {
			if !p.Protect(int(fd)) {
				log.Warnln("[Core] protect fd %d failed", fd)
			}
		})
	}
}

// Start boots the core. homeDir is the working directory (geo databases, cache);
// configPath is the final config already generated on the Android side
// (including tun.file-descriptor, external-controller and secret).
// A non-nil error is shown to the user verbatim.
func Start(homeDir string, configPath string) error {
	mu.Lock()
	defer mu.Unlock()
	if running {
		return errors.New("内核已在运行")
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return err
	}
	buf, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	// geoip/geosite/缓存等文件都落在 homeDir 下
	C.SetHomeDir(homeDir)
	C.SetConfig(configPath)
	logMu.Lock()
	startupLogs = nil
	logMu.Unlock()
	captureLogs()

	cfg, err := executor.ParseWithBytes(buf)
	if err != nil {
		return err
	}
	installDirectFallback(cfg, buf)
	hub.ApplyConfig(cfg)
	running = true
	return nil
}

// Reload applies a new config without tearing down the VPN.
func Reload(configPath string) error {
	mu.Lock()
	defer mu.Unlock()
	if !running {
		return errors.New("内核未运行")
	}
	buf, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	cfg, err := executor.ParseWithBytes(buf)
	if err != nil {
		return err
	}
	C.SetConfig(configPath)
	installDirectFallback(cfg, buf)
	executor.ApplyConfig(cfg, false)
	return nil
}

// Stop shuts the core down and releases all listeners and the TUN.
func Stop() {
	mu.Lock()
	defer mu.Unlock()
	if !running {
		return
	}
	executor.Shutdown()
	running = false
}

// IsRunning reports whether the core is running.
func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return running
}

// UpdateGeo downloads the latest versions of the geo databases that the current
// rules actually use (GeoIP / GeoSite / ASN) and hot-reloads them. Downloads go
// through the running core and follow its rules. Blocks until finished (up to
// ~90 s per file), so call it off the main thread. Returns "" on success
// (including "already up to date"), otherwise the error text.
//
// mihomo only starts its own geo auto-updater from the CLI main(), and the
// /configs/geo API is disabled in Android embed mode, hence this bridge.
func UpdateGeo() string {
	if !IsRunning() {
		return "内核未运行"
	}
	if err := updater.UpdateGeoDatabases(); err != nil {
		if errors.Is(err, updater.ErrGetDatabaseUpdateSkip) {
			return "正在更新中，请稍候"
		}
		return err.Error()
	}
	return ""
}

// GeoInUse reports which geo databases the loaded rules use, comma separated
// ("geoip", "geosite", "asn"). Empty when none.
func GeoInUse() string {
	var s []string
	if geodata.GeoIpEnable() {
		s = append(s, "geoip")
	}
	if geodata.GeoSiteEnable() {
		s = append(s, "geosite")
	}
	if geodata.ASNEnable() {
		s = append(s, "asn")
	}
	return strings.Join(s, ",")
}

// Validate parses a config without starting the core. It returns "" when valid,
// otherwise the error text. Configs with GEOIP/GEOSITE rules need the geo
// databases present under homeDir.
func Validate(homeDir string, configPath string) string {
	C.SetHomeDir(homeDir)
	buf, err := os.ReadFile(configPath)
	if err != nil {
		return err.Error()
	}
	if _, err := executor.ParseWithBytes(buf); err != nil {
		return err.Error()
	}
	return ""
}
