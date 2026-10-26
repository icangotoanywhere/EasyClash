package main

import (
	"os"
	"strconv"
)

// 各平台通用的小工具。平台相关的实现见 winutil_windows.go / util_darwin.go。

func itoa(n int) string { return strconv.Itoa(n) }

// ourProxyServer 本软件的本机代理地址
func ourProxyServer(port int) string { return "127.0.0.1:" + itoa(port) }

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return p
}

// ProcInfo 正在运行的程序（分程序代理页面的候选列表）
type ProcInfo struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	System bool   `json:"system"`
}
