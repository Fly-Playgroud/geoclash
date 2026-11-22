package discover

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadVerge(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "profiles.yaml"), `current: R1
items:
- {uid: Merge, type: merge, file: Merge.yaml}
- {uid: Script, type: script, file: Script.js}
- {uid: R1, type: remote, name: 示例订阅, file: R1.yaml}
`)
	write(t, filepath.Join(dir, "clash-verge.yaml"), "external-controller: ''\nexternal-controller-pipe: \\\\.\\pipe\\verge-mihomo\nexternal-controller-unix: verge.sock\nsecret: s3\n")
	env, err := loadVerge(dir)
	if err != nil {
		t.Fatal(err)
	}
	if env.ProfileName != "示例订阅" || env.ProfileFile != filepath.Join(dir, "profiles", "R1.yaml") ||
		env.GlobalScript != filepath.Join(dir, "profiles", "Script.js") || env.Controller.Secret != "s3" {
		t.Errorf("env = %+v", env)
	}
	want := "unix:" + filepath.Join(dir, "verge.sock")
	if runtime.GOOS == "windows" {
		want = `pipe:\\.\pipe\verge-mihomo`
	}
	// Windows 上配置中的管道不存在时会回退到本机实际存在的 verge-mihomo* 管道
	if env.Controller.URL != want && !(runtime.GOOS == "windows" && strings.HasPrefix(env.Controller.URL, want)) {
		t.Errorf("controller = %q，期望 %q", env.Controller.URL, want)
	}
}

func TestControllerTCPFirst(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	write(t, p, "external-controller: 0.0.0.0:9097\nexternal-controller-pipe: x\n")
	if c := ControllerFromConfig(p); c.URL != "http://127.0.0.1:9097" {
		t.Errorf("TCP 优先，且监听地址应转换为本机地址：%q", c.URL)
	}
}

func TestNoActiveProfile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "profiles.yaml"), "items: []\n")
	if _, err := loadVerge(dir); err == nil {
		t.Error("没有激活的订阅时应报错")
	}
}
