package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/metacubex/mihomo/component/geodata"
	"github.com/metacubex/mihomo/component/updater"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/log"
)

// 内核在本进程内运行（与安卓版 core.go 相同的做法），启动 / 重载 / 停止都直接调用 mihomo。

var (
	coreMu      sync.Mutex
	coreRunning bool

	logMu    sync.Mutex
	logLines []string
	logSeq   int // 已产生的日志总行数，界面按它增量拉取
	logOnce  sync.Once
)

const (
	maxLogLines   = 800
	maxLogFileLen = 1 << 20 // 日志文件总上限 1 MB：超过后只保留最后一半，不另存旧文件
)

var (
	logFile     *os.File
	logFilePath string
	logFileLen  int64
)

// openLogFile 日志同时写进数据目录的 logs\easyclash.log，软件重启后也能查
func openLogFile(dir string) {
	_ = os.MkdirAll(dir, 0o755)
	logMu.Lock()
	defer logMu.Unlock()
	logFilePath = filepath.Join(dir, "easyclash.log")
	_ = os.Remove(filepath.Join(dir, "easyclash.old.log")) // 旧版本留下的
	if fi, err := os.Stat(logFilePath); err == nil && fi.Size() > maxLogFileLen {
		trimLogFileLocked()
	}
	reopenLogFileLocked()
}

// trimLogFileLocked 把日志文件裁到只剩最后一半（按整行截断），保证总体积不超过上限
func trimLogFileLocked() {
	data, err := os.ReadFile(logFilePath)
	if err != nil {
		return
	}
	keep := maxLogFileLen / 2
	if len(data) > keep {
		data = data[len(data)-keep:]
		if i := bytes.IndexByte(data, '\n'); i >= 0 && i+1 < len(data) {
			data = data[i+1:]
		}
	}
	_ = os.WriteFile(logFilePath, data, 0o644)
}

func reopenLogFileLocked() {
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		logFile = nil
		return
	}
	logFile = f
	logFileLen = 0
	if fi, err := f.Stat(); err == nil {
		logFileLen = fi.Size()
	}
}

func appendLog(line string) {
	now := time.Now()
	logMu.Lock()
	logLines = append(logLines, now.Format("15:04:05 ")+line)
	if len(logLines) > maxLogLines {
		logLines = logLines[len(logLines)-maxLogLines:]
	}
	logSeq++
	if logFile != nil {
		n, _ := logFile.WriteString(now.Format("2006-01-02 15:04:05 ") + line + logEOL)
		logFileLen += int64(n)
		if logFileLen > maxLogFileLen {
			logFile.Close()
			trimLogFileLocked()
			reopenLogFileLocked()
		}
	}
	logMu.Unlock()
}

func logDir(dataDir string) string { return filepath.Join(dataDir, "logs") }

// logsSince 返回第 since 行之后的日志，以及当前总行数
func logsSince(since int) ([]string, int) {
	logMu.Lock()
	defer logMu.Unlock()
	start := len(logLines) - (logSeq - since)
	if start < 0 {
		start = 0
	}
	if start > len(logLines) {
		start = len(logLines)
	}
	return append([]string{}, logLines[start:]...), logSeq
}

func clearLogs() {
	logMu.Lock()
	logLines = nil
	logMu.Unlock()
}

// captureCoreLogs 订阅内核日志流（只订阅一次），连接、断开都持续收集
func captureCoreLogs() {
	logOnce.Do(func() {
		sub := log.Subscribe()
		go func() {
			for ev := range sub {
				// 内核会把所有级别的日志都推过来，这里按设置里的日志级别过滤（默认 info，不记 debug）
				if ev.LogLevel < log.Level() {
					continue
				}
				appendLog(fmt.Sprintf("[%s] %s", strings.ToUpper(ev.Type()), ev.Payload))
			}
		}()
	})
}

func coreVersion() string { return C.Version }

// coreStart 启动内核。homeDir 是工作目录（geo 数据库、缓存）；configPath 是已生成好的最终配置。
func coreStart(homeDir, configPath string) error {
	coreMu.Lock()
	defer coreMu.Unlock()
	if coreRunning {
		return errors.New("内核已在运行")
	}
	buf, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	C.SetHomeDir(homeDir)
	C.SetConfig(configPath)
	captureCoreLogs()
	if err := hub.Parse(buf); err != nil {
		executor.Shutdown()
		return err
	}
	coreRunning = true
	return nil
}

// coreReload 不断开直接换配置
func coreReload(configPath string) error {
	coreMu.Lock()
	defer coreMu.Unlock()
	if !coreRunning {
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
	executor.ApplyConfig(cfg, false)
	return nil
}

// 断开时换上一份空配置：mihomo 自带的 Shutdown 只关 TUN，不关代理端口、控制接口和节点测速，
// 不换掉的话断开后端口还在服务、后台还在定时测速。
const blankConfig = "mixed-port: 0\nallow-lan: false\nmode: direct\nlog-level: warning\nexternal-controller: ''\nproxies: []\nproxy-groups: []\nrules: []\ndns:\n  enable: false\n"

func coreStop() {
	coreMu.Lock()
	defer coreMu.Unlock()
	if !coreRunning {
		return
	}
	if cfg, err := executor.ParseWithBytes([]byte(blankConfig)); err == nil {
		hub.ApplyConfig(cfg)
	}
	executor.Shutdown()
	coreRunning = false
}

func coreIsRunning() bool {
	coreMu.Lock()
	defer coreMu.Unlock()
	return coreRunning
}

// coreValidate 只解析不启动，用来在连接前给出清楚的报错
func coreValidate(homeDir, configPath string) error {
	C.SetHomeDir(homeDir)
	buf, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	_, err = executor.ParseWithBytes(buf)
	return err
}

// coreUpdateGeo 下载当前规则用到的地理数据库的最新版并热加载。返回 nil 表示成功（含"已是最新"）。
func coreUpdateGeo() error {
	if !coreIsRunning() {
		return errors.New("内核未运行")
	}
	if err := updater.UpdateGeoDatabases(); err != nil {
		if errors.Is(err, updater.ErrGetDatabaseUpdateSkip) {
			return errors.New("正在更新中，请稍候")
		}
		return err
	}
	return nil
}

// coreGeoInUse 当前规则用到了哪些地理数据库
func coreGeoInUse() []string {
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
	return s
}
