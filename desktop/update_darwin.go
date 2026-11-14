package main

// macOS 原地升级：挂载下载的 dmg，把里面的 EasyClash.app 复制到当前程序旁边，
// 把正在运行的 EasyClash.app 换下来，然后用 open -n 启动新版本。程序位置不变，开机自启照常有效。

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func updateAssetName(ver string) string { return "EasyClash-macOS-v" + ver + ".dmg" }

// appBundle 当前运行的 EasyClash.app 路径（…/EasyClash.app/Contents/MacOS/EasyClash 往上三级）
func appBundle() (string, error) {
	exe, _ := filepath.EvalSymlinks(exePath())
	b := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if !strings.HasSuffix(b, ".app") {
		return "", errors.New("没有从 EasyClash.app 运行，无法自动升级，请手动下载新版本")
	}
	return b, nil
}

func applyUpdate(file, workDir string) error {
	bundle, err := appBundle()
	if err != nil {
		return err
	}
	mnt := filepath.Join(workDir, "mnt")
	_ = os.MkdirAll(mnt, 0o755)
	if out, err := exec.Command("/usr/bin/hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, file).CombinedOutput(); err != nil {
		return errors.New("打开安装包失败：" + strings.TrimSpace(string(out)))
	}
	defer exec.Command("/usr/bin/hdiutil", "detach", "-force", mnt).Run()

	src := filepath.Join(mnt, "EasyClash.app")
	if _, err := os.Stat(src); err != nil {
		return errors.New("安装包里没有 EasyClash.app")
	}
	newPath := bundle + ".new"
	oldPath := bundle + ".old"
	_ = os.RemoveAll(newPath)
	if out, err := exec.Command("/usr/bin/ditto", src, newPath).CombinedOutput(); err != nil {
		_ = os.RemoveAll(newPath)
		return errors.New("程序所在文件夹写不进去（" + filepath.Dir(bundle) + "）。请手动下载新版本拖进「应用程序」：" + strings.TrimSpace(string(out)))
	}
	_ = exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", newPath).Run()
	_ = os.RemoveAll(oldPath)
	if err := os.Rename(bundle, oldPath); err != nil {
		_ = os.RemoveAll(newPath)
		return errors.New("替换程序失败：" + err.Error())
	}
	if err := os.Rename(newPath, bundle); err != nil {
		_ = os.Rename(oldPath, bundle)
		return errors.New("替换程序失败：" + err.Error())
	}
	_ = os.RemoveAll(oldPath) // 正在运行的旧程序已经载入内存，删掉文件不影响它继续跑到退出
	updatedBundle = bundle
	return nil
}

var updatedBundle string

func cleanupAfterUpdate(dataDir string) {
	if b, err := appBundle(); err == nil {
		_ = os.RemoveAll(b + ".old")
		_ = os.RemoveAll(b + ".new")
	}
	_ = os.RemoveAll(filepath.Join(dataDir, "update"))
}

func launchUpdated() error {
	b := updatedBundle
	if b == "" {
		var err error
		if b, err = appBundle(); err != nil {
			return err
		}
	}
	return exec.Command("/usr/bin/open", "-n", b, "--args", "--wait-instance").Start()
}
