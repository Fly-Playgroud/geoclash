package discover

import "strings"

// Kernel 是从运行中的内核进程启动参数解析出的信息。
type Kernel struct {
	Home       string // -d：内核工作目录（provider 缓存等相对路径的基准）
	Config     string // -f：内核加载的配置文件
	Controller string // -ext-ctl / -ext-ctl-unix / -ext-ctl-pipe 转换成的 controller 地址
}

// kernelFlags 是 mihomo 中带值的命令行参数。
var kernelFlags = []string{"-d", "-f", "-ext-ctl", "-ext-ctl-unix", "-ext-ctl-pipe", "-secret", "-ext-ui", "-config"}

// isKernelCommand 判断一行进程命令是否为 mihomo 内核（Clash Verge Rev 的 verge-mihomo、mihomo、clash-meta）。
func isKernelCommand(cmd string) bool {
	exe := cmd
	if i := strings.Index(cmd, " -"); i >= 0 {
		exe = cmd[:i]
	}
	exe = strings.ToLower(exe)
	return strings.Contains(exe, "mihomo") || strings.Contains(exe, "clash-meta")
}

// parseKernelCommand 解析内核进程的命令行。路径中可能含有空格（如 /Library/Application Support），
// 因此参数值取到下一个已知参数为止。
func parseKernelCommand(cmd string) Kernel {
	values := map[string]string{}
	rest := " " + cmd + " "
	for {
		// 找到最靠前的已知参数
		pos, flag := -1, ""
		for _, f := range kernelFlags {
			if i := strings.Index(rest, " "+f+" "); i >= 0 && (pos < 0 || i < pos) {
				pos, flag = i, f
			}
		}
		if pos < 0 {
			break
		}
		rest = rest[pos+len(flag)+2:]
		// 值到下一个已知参数为止
		end := len(rest)
		for _, f := range kernelFlags {
			if i := strings.Index(rest, " "+f+" "); i >= 0 && i < end {
				end = i
			}
		}
		values[flag] = strings.TrimSpace(rest[:end])
		rest = " " + rest[end:]
	}
	k := Kernel{Home: values["-d"], Config: values["-f"]}
	switch {
	case values["-ext-ctl"] != "":
		k.Controller = "http://" + loopbackAddr(values["-ext-ctl"])
	case values["-ext-ctl-unix"] != "":
		k.Controller = "unix:" + values["-ext-ctl-unix"]
	case values["-ext-ctl-pipe"] != "":
		k.Controller = "pipe:" + values["-ext-ctl-pipe"]
	}
	return k
}
