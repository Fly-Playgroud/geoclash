//go:build !windows

package discover

import (
	"os/exec"
	"strings"
)

// findKernel 从进程列表中找到运行中的内核并解析其启动参数；prefer 非空时优先选择命令中包含它的进程
// （例如 Clash Verge Rev 环境下优先 verge-mihomo）。macOS / Linux 的 ps 可以看到 root 进程的命令行，
// 因此即使内核由 Verge 服务以 root 身份运行也能读取。
func findKernel(prefer string) Kernel {
	out, err := exec.Command("ps", "-A", "-ww", "-o", "args=").Output()
	if err != nil {
		return Kernel{}
	}
	var first string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !isKernelCommand(line) {
			continue
		}
		if prefer == "" || strings.Contains(line, prefer) {
			return parseKernelCommand(line)
		}
		if first == "" {
			first = line
		}
	}
	if first == "" {
		return Kernel{}
	}
	return parseKernelCommand(first)
}
