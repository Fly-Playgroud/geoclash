//go:build darwin

package autostart

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const label = "com.geoclash.agent"

func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// Install 写入 LaunchAgent 并通过 launchctl 启动。KeepAlive 只在异常退出时重启，geoclash stop 不会被拉起。
func Install(s Spec) (managed bool, err error) {
	var args strings.Builder
	for _, a := range append([]string{s.Exe}, s.Args()...) {
		fmt.Fprintf(&args, "\n    <string>%s</string>", html.EscapeString(a))
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>%s
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ProcessType</key><string>Background</string>
</dict>
</plist>
`, label, args.String())
	p := plistPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		return false, err
	}
	_ = exec.Command("launchctl", "bootout", domain()+"/"+label).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain(), p).CombinedOutput(); err != nil {
		return false, fmt.Errorf("launchctl bootstrap 失败：%v %s", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// Uninstall 卸载 LaunchAgent 并删除 plist。
func Uninstall() error {
	_ = exec.Command("launchctl", "bootout", domain()+"/"+label).Run()
	if err := os.Remove(plistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Status 返回是否已安装。
func Status() (bool, string) {
	if _, err := os.Stat(plistPath()); err != nil {
		return false, ""
	}
	return true, plistPath()
}
