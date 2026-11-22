//go:build !windows

package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func resolvePipe(configured string) string { return configured }

func isSocket(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

// resolveSocket 校验 unix socket 是否存在；不存在时依次寻找 Clash Verge Rev 服务模式的固定位置
// （/var/run/clash-verge-service/users/<uid>/verge-mihomo.sock）以及同目录、/tmp/verge 下的 verge-mihomo*.sock。
// 正常情况下会先从运行中的内核进程读到实际地址（见 withKernel），这里是读取失败时的兜底。
func resolveSocket(configured string) string {
	if isSocket(configured) {
		return configured
	}
	if p := fmt.Sprintf("/var/run/clash-verge-service/users/%d/verge-mihomo.sock", os.Getuid()); isSocket(p) {
		return p
	}
	for _, dir := range []string{filepath.Dir(configured), "/tmp/verge"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "verge-mihomo*.sock"))
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && st.Mode()&os.ModeSocket != 0 && !strings.HasSuffix(m, ".lock") {
				return m
			}
		}
	}
	return configured
}
