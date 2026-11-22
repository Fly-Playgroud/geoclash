package source

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestParseYAML(t *testing.T) {
	maps, providers, err := Parse([]byte("proxies:\n  - {name: a, type: ss, server: x, port: 1}\nproxy-providers:\n  p1: {type: file, path: ./p1.yaml}\n"))
	if err != nil || len(maps) != 1 || len(providers) != 1 {
		t.Fatalf("%v %v %v", maps, providers, err)
	}
}

func TestParseShareLinks(t *testing.T) {
	links := "trojan://pass@example.com:443?sni=example.com#%E7%BE%8E%E5%9B%BD01\n" +
		"hysteria2://auth@example.org:443?sni=example.org#%E6%97%A5%E6%9C%AC02\n"
	for _, data := range [][]byte{[]byte(links), []byte(base64.StdEncoding.EncodeToString([]byte(links)))} {
		maps, _, err := Parse(data)
		if err != nil || len(maps) != 2 {
			t.Fatalf("%v %v", maps, err)
		}
		if maps[0]["name"] != "美国01" || maps[1]["type"] != "hysteria2" {
			t.Errorf("解析结果 = %v", maps)
		}
	}
}

func TestLoadFileWithProviders(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("p1.yaml", "proxies:\n  - {name: b, type: ss, server: y, port: 2}\n")
	write("geo.yaml", "proxies:\n  - {name: 不应读取, type: ss, server: z, port: 3}\n")
	write("config.yaml", `proxies:
  - {name: a, type: ss, server: x, port: 1}
proxy-providers:
  p1: {type: http, path: ./p1.yaml}
  geoclash-US: {type: http, path: ./geo.yaml}
  missing: {type: http, path: ./nope.yaml}
  inline1: {type: inline, payload: [{name: c, type: ss, server: w, port: 4}]}
`)
	nodes, err := LoadFile(Spec{File: filepath.Join(dir, "config.yaml"), Label: "t"})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range nodes {
		names[n.Name] = true
	}
	if !names["a"] || !names["b"] || !names["c"] || names["不应读取"] || len(nodes) != 3 {
		t.Errorf("节点 = %v（应包含 provider 缓存，跳过 geoclash 自己的 provider）", names)
	}
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte("proxy-providers:\n  b: {type: http, path: ./b.yaml}\n  a: {type: file, path: ./a.yaml}\n  geoclash-US: {type: http, path: ./geo.yaml}\n  i: {type: inline, payload: []}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Files(Spec{File: cfg})
	want := []string{cfg, filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml")}
	if len(got) != len(want) {
		t.Fatalf("Files = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Files = %v，期望 %v", got, want)
		}
	}
	if Files(Spec{URL: "https://x"}) != nil {
		t.Error("订阅链接来源不应返回文件")
	}
}
