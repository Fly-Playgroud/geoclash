// Package autostart 实现用户级开机自启（不需要管理员 / root 权限），以及在后台启动 geoclash。
package autostart

import (
	"os"
	"os/exec"
)

// Spec 描述自启时执行的命令。
type Spec struct {
	Exe    string // 可执行文件（自启副本）
	Config string
	Log    string
}

// Args 返回 run 子命令的参数。
func (s Spec) Args() []string {
	return []string{"run", "-c", s.Config, "-log", s.Log}
}

// Spawn 在后台启动 geoclash，并与当前终端脱离。
func Spawn(s Spec) error {
	cmd := exec.Command(s.Exe, s.Args()...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Dir = os.TempDir()
	cmd.SysProcAttr = detachAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
