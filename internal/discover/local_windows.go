//go:build windows

package discover

import (
	"os"
	"strings"
)

const pipeRoot = `\\.\pipe\`

// resolvePipe 校验命名管道是否存在。Clash Verge Rev 在服务模式下使用的管道名（verge-mihomo-production-<hash>）
// 与运行时配置中写的（verge-mihomo-sidecar-release-<hash>）不同，因此配置中的管道不存在时，
// 枚举 verge-mihomo 开头的管道，优先选择哈希后缀相同的那个。
func resolvePipe(configured string) string {
	entries, err := os.ReadDir(pipeRoot)
	if err != nil {
		return configured
	}
	want := strings.TrimPrefix(configured, pipeRoot)
	var candidates []string
	for _, e := range entries {
		name := e.Name()
		if name == want {
			return configured
		}
		if strings.HasPrefix(name, "verge-mihomo") {
			candidates = append(candidates, name)
		}
	}
	return pickSameSuffix(configured, candidates, pipeRoot)
}

func resolveSocket(configured string) string { return configured }

// pickSameSuffix 优先选择与 configured 最后一段（哈希）相同的候选。
func pickSameSuffix(configured string, candidates []string, prefix string) string {
	if len(candidates) == 0 {
		return configured
	}
	suffix := configured
	if i := strings.LastIndex(configured, "-"); i >= 0 {
		suffix = configured[i:]
	}
	for _, c := range candidates {
		if strings.HasSuffix(c, suffix) {
			return prefix + c
		}
	}
	return prefix + candidates[0]
}
