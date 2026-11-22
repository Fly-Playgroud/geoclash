//go:build !windows

package discover

import (
	"os"
	"path/filepath"
	"strings"
)

func resolvePipe(configured string) string { return configured }

// resolveSocket 校验 unix socket 是否存在；不存在时在同目录和 /tmp/verge 下寻找 verge-mihomo*.sock
// （Clash Verge Rev 的服务模式与普通模式使用的 socket 可能不同）。
func resolveSocket(configured string) string {
	if st, err := os.Stat(configured); err == nil && st.Mode()&os.ModeSocket != 0 {
		return configured
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
