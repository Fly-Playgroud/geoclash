//go:build windows

package discover

// findKernel 在 Windows 上返回空：Clash Verge Rev 服务模式下内核以 LocalSystem 运行，
// 普通用户读不到它的命令行；controller 通过枚举命名管道发现（见 resolvePipe）。
func findKernel(string) Kernel { return Kernel{} }
