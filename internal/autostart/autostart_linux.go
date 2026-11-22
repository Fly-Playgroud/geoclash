//go:build linux

package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const unitName = "geoclash.service"

func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func configHome() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

func unitPath() string    { return filepath.Join(configHome(), "systemd", "user", unitName) }
func desktopPath() string { return filepath.Join(configHome(), "autostart", "geoclash.desktop") }

func hasSystemd() bool {
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

// shellQuote 按 systemd / desktop 文件的规则给参数加引号。
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func cmdline(s Spec) string {
	parts := []string{shellQuote(s.Exe)}
	for _, a := range s.Args() {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// Install 优先使用 systemd user unit（由 systemd 启动并在异常退出时重启）；没有 systemd 时写入 XDG autostart。
func Install(s Spec) (managed bool, err error) {
	if hasSystemd() {
		unit := fmt.Sprintf(`[Unit]
Description=geoclash - 按实测归属地为 Clash 节点分组
After=network-online.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`, cmdline(s))
		if err := os.MkdirAll(filepath.Dir(unitPath()), 0o755); err != nil {
			return false, err
		}
		if err := os.WriteFile(unitPath(), []byte(unit), 0o644); err != nil {
			return false, err
		}
		for _, args := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
			if out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput(); err != nil {
				return false, fmt.Errorf("systemctl --user %s 失败：%v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
			}
		}
		return true, nil
	}
	desktop := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=geoclash
Exec=%s
X-GNOME-Autostart-enabled=true
NoDisplay=true
`, cmdline(s))
	if err := os.MkdirAll(filepath.Dir(desktopPath()), 0o755); err != nil {
		return false, err
	}
	return false, os.WriteFile(desktopPath(), []byte(desktop), 0o644)
}

// Uninstall 移除 systemd unit 或 XDG autostart 文件。
func Uninstall() error {
	if _, err := os.Stat(unitPath()); err == nil {
		_ = exec.Command("systemctl", "--user", "disable", "--now", unitName).Run()
		if err := os.Remove(unitPath()); err != nil {
			return err
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	if err := os.Remove(desktopPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Status 返回是否已安装。
func Status() (bool, string) {
	for _, p := range []string{unitPath(), desktopPath()} {
		if _, err := os.Stat(p); err == nil {
			return true, p
		}
	}
	return false, ""
}
