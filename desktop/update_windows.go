package main

// Windows 原地升级：从 zip 里取出新的 EasyClash.exe，放在当前程序旁边，
// 把正在运行的程序改名为 .old（Windows 允许给运行中的 exe 改名），新程序改成原来的名字，
// 然后重新启动。下次启动时删掉 .old。程序文件名、位置都不变，开机自启、clash:// 链接照常有效。

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func updateAssetName(ver string) string { return "EasyClash-Windows-v" + ver + ".zip" }

// updatedExe 升级后要重新启动的程序路径（升级前就记下：改名之后 os.Executable 可能指向 .old）
var updatedExe string

func applyUpdate(file, workDir string) error {
	exe := exePath()
	zr, err := zip.OpenReader(file)
	if err != nil {
		return errors.New("安装包损坏：" + err.Error())
	}
	defer zr.Close()
	var src *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(filepath.Base(f.Name), "EasyClash.exe") {
			src = f
		}
	}
	if src == nil {
		return errors.New("安装包里没有 EasyClash.exe")
	}
	newPath := exe + ".new"
	oldPath := exe + ".old"
	if err := extractTo(src, newPath); err != nil {
		return errors.New("程序所在文件夹写不进去（" + filepath.Dir(exe) + "）。请把 EasyClash 放到自己的文件夹里（不要放在 Program Files 或压缩包里），或手动下载新版本覆盖：" + err.Error())
	}
	_ = os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		_ = os.Remove(newPath)
		return errors.New("替换程序失败：" + err.Error())
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(oldPath, exe) // 还原
		return errors.New("替换程序失败：" + err.Error())
	}
	updatedExe = exe
	return nil
}

func extractTo(f *zip.File, dst string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		_ = os.Remove(dst)
		return err
	}
	return w.Close()
}

// cleanupAfterUpdate 启动时删掉上次升级留下的旧程序和安装包
func cleanupAfterUpdate(dataDir string) {
	_ = os.Remove(exePath() + ".old")
	_ = os.Remove(exePath() + ".new")
	_ = os.RemoveAll(filepath.Join(dataDir, "update"))
}

// launchUpdated 启动升级后的新程序。当前若是管理员身份（TUN 模式），新程序也是管理员身份，不会再弹 UAC
func launchUpdated() error {
	exe := updatedExe
	if exe == "" {
		exe = exePath()
	}
	v, _ := windows.UTF16PtrFromString("open")
	e, _ := windows.UTF16PtrFromString(exe)
	a, _ := windows.UTF16PtrFromString("--wait-instance")
	d, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, v, e, a, d, windows.SW_SHOWNORMAL)
}
