//go:build windows

package autostart

import (
	"errors"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName = "geoclash"
)

func detachAttr() *syscall.SysProcAttr {
	const (
		createNewProcessGroup = 0x00000200
		detachedProcess       = 0x00000008
	)
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess, HideWindow: true}
}

func quote(s string) string { return `"` + s + `"` }

// Install 写入 HKCU\...\Run。返回 false 表示需要调用方自行启动进程。
func Install(s Spec) (managed bool, err error) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	parts := []string{quote(s.Exe)}
	for _, a := range s.Args() {
		if strings.ContainsAny(a, ` \`) || a == "" {
			a = quote(a)
		}
		parts = append(parts, a)
	}
	return false, k.SetStringValue(valueName, strings.Join(parts, " "))
}

// Uninstall 删除自启项；不存在时不报错。
func Uninstall() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// Status 返回是否已安装以及自启命令。
func Status() (bool, string) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue(valueName)
	if err != nil {
		return false, ""
	}
	return true, `HKCU\` + runKey + `\` + valueName + ` = ` + v
}
