package discover

import "testing"

func TestParseKernelCommand(t *testing.T) {
	// macOS 上 Clash Verge Rev 服务模式的真实命令行（路径含空格）
	cmd := "/Library/Application Support/clash-verge-service/cores/verge-mihomo -d /Library/Application Support/clash-verge-service/users/501/runtime -f /Library/Application Support/clash-verge-service/users/501/runtime/config.yaml -ext-ctl-unix /var/run/clash-verge-service/users/501/verge-mihomo.sock"
	if !isKernelCommand(cmd) {
		t.Fatal("应识别为内核进程")
	}
	k := parseKernelCommand(cmd)
	if k.Home != "/Library/Application Support/clash-verge-service/users/501/runtime" ||
		k.Config != "/Library/Application Support/clash-verge-service/users/501/runtime/config.yaml" ||
		k.Controller != "unix:/var/run/clash-verge-service/users/501/verge-mihomo.sock" {
		t.Errorf("解析结果 = %+v", k)
	}

	k = parseKernelCommand("/usr/bin/mihomo -d /etc/mihomo -ext-ctl 0.0.0.0:9090")
	if k.Home != "/etc/mihomo" || k.Controller != "http://127.0.0.1:9090" {
		t.Errorf("解析结果 = %+v", k)
	}

	for _, c := range []string{
		"/Applications/Clash Verge.app/Contents/MacOS/clash-verge",
		"/usr/bin/grep -i mihomo",
		"/Library/Application Support/clash-verge-service/clash-verge-service",
	} {
		if isKernelCommand(c) {
			t.Errorf("%q 不应识别为内核进程", c)
		}
	}
}
