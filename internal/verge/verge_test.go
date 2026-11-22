package verge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testData(mode string, groups ...string) Data {
	return Data{
		Base:     "http://127.0.0.1:9876",
		Selector: "🌍 实测国家",
		Groups: []GroupDef{
			{ID: "US", Name: "🇺🇸 美国-实测", Type: "url-test", URL: "https://www.gstatic.com/generate_204", Interval: 300, Tolerance: 50},
			{ID: "JP", Name: "🇯🇵 日本-实测", Type: "fallback", URL: "https://www.gstatic.com/generate_204", Interval: 300, Tolerance: 50},
		},
		Attach:           Attach{Mode: mode, Groups: groups},
		Processes:        []string{"geoclash.exe"},
		ProviderInterval: 3600,
		PathDir:          "./geoclash",
	}
}

func mustBlock(t *testing.T, d Data) string {
	t.Helper()
	b, err := Block(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sampleConfig 模拟典型的机场订阅结构。
func sampleConfig() map[string]any {
	return map[string]any{
		"mixed-port": 7890,
		"proxies": []any{
			map[string]any{"name": "香港01", "type": "ss", "server": "a.example", "port": 443},
			map[string]any{"name": "日本02", "type": "ss", "server": "b.example", "port": 443},
		},
		"proxy-groups": []any{
			map[string]any{"name": "节点选择", "type": "select", "proxies": []any{"自动选择", "香港01", "日本02", "DIRECT"}},
			map[string]any{"name": "自动选择", "type": "url-test", "proxies": []any{"香港01", "日本02"}},
			map[string]any{"name": "流媒体", "type": "select", "proxies": []any{"节点选择", "香港01"}},
			map[string]any{"name": "苹果服务", "type": "select", "proxies": []any{"DIRECT", "节点选择", "香港01"}},
			map[string]any{"name": "全球直连", "type": "select", "proxies": []any{"DIRECT", "节点选择"}},
			map[string]any{"name": "广告拦截", "type": "select", "proxies": []any{"REJECT", "DIRECT"}},
			map[string]any{"name": "漏网之鱼", "type": "select", "proxies": []any{"节点选择", "DIRECT"}},
		},
		"rules": []any{"DOMAIN-SUFFIX,cn,全球直连", "MATCH,漏网之鱼"},
	}
}

func groupsOf(t *testing.T, cfg map[string]any) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, gi := range cfg["proxy-groups"].([]any) {
		g := gi.(map[string]any)
		var ps []string
		if raw, ok := g["proxies"].([]any); ok {
			for _, p := range raw {
				ps = append(ps, p.(string))
			}
		}
		out[g["name"].(string)] = ps
	}
	return out
}

func run(t *testing.T, user string, d Data, cfg map[string]any) map[string]any {
	t.Helper()
	out, logs, err := Run(user, mustBlock(t, d), cfg, "test")
	if err != nil {
		t.Fatalf("Run: %v (logs: %v)", err, logs)
	}
	return out
}

func TestAttachModes(t *testing.T) {
	const sel = "🌍 实测国家"
	cases := []struct {
		name string
		data Data
		want map[string]string // 分组 → "first" / "last" / ""（未挂接）
	}{
		{"auto", testData("auto"), map[string]string{
			"节点选择": "first", "流媒体": "first", "苹果服务": "last",
			"全球直连": "", "广告拦截": "", "漏网之鱼": "", "自动选择": "",
		}},
		{"all", testData("all"), map[string]string{
			"节点选择": "first", "流媒体": "first", "苹果服务": "last",
			"全球直连": "last", "广告拦截": "last", "漏网之鱼": "first", "自动选择": "",
		}},
		{"list", testData("list", "全球直连", "自动选择", "不存在的组"), map[string]string{
			"节点选择": "", "全球直连": "last", "自动选择": "",
		}},
		{"none", testData("none"), map[string]string{"节点选择": "", "全球直连": ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := groupsOf(t, run(t, "", c.data, sampleConfig()))
			for name, want := range c.want {
				ps := g[name]
				got := ""
				for i, p := range ps {
					if p == sel {
						got = "last"
						if i == 0 {
							got = "first"
						}
					}
				}
				if got != want {
					t.Errorf("%s: 挂接位置 = %q，期望 %q（成员 %v）", name, got, want, ps)
				}
			}
		})
	}
}

func TestAttachExclude(t *testing.T) {
	d := testData("all")
	d.Attach.Exclude = []string{"^全球直连$", "广告"}
	g := groupsOf(t, run(t, "", d, sampleConfig()))
	for _, name := range []string{"全球直连", "广告拦截"} {
		for _, p := range g[name] {
			if p == d.Selector {
				t.Errorf("%s 不应被挂接", name)
			}
		}
	}
}

func TestInjectionContent(t *testing.T) {
	out := run(t, "", testData("auto"), sampleConfig())
	prov := out["proxy-providers"].(map[string]any)
	us := prov["geoclash-US"].(map[string]any)
	if us["url"] != "http://127.0.0.1:9876/provider/US" || us["proxy"] != "DIRECT" || us["path"] != "./geoclash/geoclash-US.yaml" {
		t.Errorf("provider 内容不正确：%v", us)
	}
	rules := out["rules"].([]any)
	if rules[0] != "PROCESS-NAME,geoclash.exe,DIRECT" {
		t.Errorf("第一条规则应为 PROCESS-NAME 直连，实际 %v", rules[0])
	}
	g := groupsOf(t, out)
	if !reflect.DeepEqual(g["🌍 实测国家"], []string{"🇺🇸 美国-实测", "🇯🇵 日本-实测"}) {
		t.Errorf("选择组成员 = %v", g["🌍 实测国家"])
	}
	for _, gi := range out["proxy-groups"].([]any) {
		m := gi.(map[string]any)
		switch m["name"] {
		case "🇺🇸 美国-实测":
			if m["type"] != "url-test" || m["tolerance"] == nil {
				t.Errorf("美国组 = %v", m)
			}
		case "🇯🇵 日本-实测":
			if m["type"] != "fallback" || m["tolerance"] != nil {
				t.Errorf("日本组 = %v", m)
			}
		}
	}
	if out["mixed-port"].(float64) != 7890 {
		t.Error("其他字段不应被修改")
	}
}

// 重复执行（例如热重载作用在已经注入过的运行时配置上）结果不变。
func TestIdempotent(t *testing.T) {
	d := testData("all")
	once := run(t, "", d, sampleConfig())
	twice := run(t, "", d, once)
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("执行两次结果不同：\n%v\n%v", once, twice)
	}
}

// 分组集合变化、选择组改名后，旧的注入内容被完整清除。
func TestCleanupOnChange(t *testing.T) {
	first := run(t, "", testData("auto"), sampleConfig())
	d := testData("auto")
	d.Selector = "🌐 新选择组"
	d.Groups = d.Groups[:1]
	out := run(t, "", d, first)
	g := groupsOf(t, out)
	if _, ok := g["🌍 实测国家"]; ok {
		t.Error("旧选择组没有被清除")
	}
	if _, ok := g["🇯🇵 日本-实测"]; ok {
		t.Error("旧国家组没有被清除")
	}
	if _, ok := out["proxy-providers"].(map[string]any)["geoclash-JP"]; ok {
		t.Error("旧 provider 没有被清除")
	}
	for _, p := range g["节点选择"] {
		if p == "🌍 实测国家" {
			t.Error("原有分组中残留旧选择组的引用")
		}
	}
	if g["节点选择"][0] != "🌐 新选择组" {
		t.Errorf("新选择组应位于第一位：%v", g["节点选择"])
	}
	rules := out["rules"].([]any)
	n := 0
	for _, r := range rules {
		if strings.HasPrefix(r.(string), "PROCESS-NAME,geoclash") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("PROCESS-NAME 规则应只有 1 条，实际 %d", n)
	}
}

func TestNoGroupsOnlyCleans(t *testing.T) {
	first := run(t, "", testData("auto"), sampleConfig())
	d := testData("none")
	d.Groups = nil
	d.Processes = nil
	out := run(t, "", d, first)
	if !reflect.DeepEqual(groupsOf(t, out), groupsOf(t, run(t, "", d, sampleConfig()))) {
		t.Error("清理后的分组应与原始配置一致")
	}
	if len(out["proxy-providers"].(map[string]any)) != 0 {
		t.Error("清理后不应残留 provider")
	}
}

// userScript 结构与真实的全局脚本一致：var 常量、辅助函数、function main 改写规则与分组。
const userScript = `// Define main function (script entry)
var DIRECT_RULES = ["DOMAIN-SUFFIX,example.com,DIRECT"];
var FOLLOW = { leader: "节点选择", followers: ["流媒体"] };
function followLeader(config) {
  var groups = config["proxy-groups"] || [];
  for (var j = 0; j < groups.length; j++) {
    var g = groups[j];
    if (FOLLOW.followers.indexOf(g.name) < 0 || g.type !== "select") continue;
    g.proxies = [FOLLOW.leader].concat((g.proxies || []).filter(function (n) { return n !== FOLLOW.leader; }));
  }
}
function main(config, profileName) {
  followLeader(config);
  config.rules = DIRECT_RULES.concat(config.rules || []);
  config["user-ran"] = profileName;
  return config;
}
`

func TestCoexistWithUserScript(t *testing.T) {
	cases := map[string]string{
		"function main": userScript,
		"var main":      strings.Replace(userScript, "function main(config, profileName) {", "var main = function (config, profileName) {", 1),
		"let main":      strings.Replace(userScript, "function main(config, profileName) {", "let main = function (config, profileName) {", 1),
	}
	for name, user := range cases {
		t.Run(name, func(t *testing.T) {
			out := run(t, user, testData("auto"), sampleConfig())
			if out["user-ran"] != "test" {
				t.Fatal("用户的 main 没有执行")
			}
			rules := out["rules"].([]any)
			if rules[0] != "PROCESS-NAME,geoclash.exe,DIRECT" || rules[1] != "DOMAIN-SUFFIX,example.com,DIRECT" {
				t.Errorf("执行顺序不对，应先执行用户 main 再执行 geoclash：%v", rules[:2])
			}
			if g := groupsOf(t, out); g["节点选择"][0] != "🌍 实测国家" {
				t.Errorf("geoclash 部分没有生效：%v", g["节点选择"])
			}
		})
	}
}

func TestEmptyUserScript(t *testing.T) {
	out := run(t, "// Define main function (script entry)\n", testData("auto"), sampleConfig())
	if g := groupsOf(t, out); g["节点选择"][0] != "🌍 实测国家" {
		t.Errorf("没有 main 时区块应自己提供 main：%v", g["节点选择"])
	}
}

// const main 无法被包装：用户脚本必须照常运行，只是 geoclash 部分不生效。
func TestConstMainKeepsUserScript(t *testing.T) {
	user := strings.Replace(userScript, "function main(config, profileName) {", "const main = function (config, profileName) {", 1)
	if !HasConstMain(user) {
		t.Fatal("应检测到 const main")
	}
	out, logs, err := Run(user, mustBlock(t, testData("auto")), sampleConfig(), "test")
	if err != nil {
		t.Fatalf("用户脚本应照常运行：%v", err)
	}
	if out["user-ran"] != "test" {
		t.Error("用户的 main 没有执行")
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "无法包装 main") {
		t.Errorf("应输出提示：%v", logs)
	}
	if HasConstMain(userScript) {
		t.Error("function main 不应被判定为 const main")
	}
}

func TestUpsertAndStrip(t *testing.T) {
	block := mustBlock(t, testData("auto"))
	got, changed := Upsert(userScript, block)
	if !changed || !strings.HasPrefix(got, strings.TrimRight(userScript, "\n")+"\n\n"+BeginMarker) {
		t.Fatal("区块应追加在用户脚本之后")
	}
	if again, changed := Upsert(got, block); changed || again != got {
		t.Error("内容相同时不应改写")
	}
	block2 := mustBlock(t, testData("all"))
	got2, changed := Upsert(got, block2)
	if !changed || strings.Count(got2, BeginMarker) != 1 || !strings.Contains(got2, `"mode": "all"`) {
		t.Error("应替换已有区块")
	}
	if Strip(got2) != userScript {
		t.Errorf("移除区块后应还原用户脚本：\n%q", Strip(got2))
	}
	if b, ok := Extract(got2); !ok || b != block2 {
		t.Error("Extract 应返回当前区块")
	}
	// 用户在区块之后追加的内容不会丢失
	edited := got2 + "\n// 用户新加的注释\n"
	got3, _ := Upsert(edited, block)
	if !strings.Contains(got3, "// 用户新加的注释") {
		t.Error("标记之外的内容不应丢失")
	}
	// CRLF 换行保持不变
	crlf := strings.ReplaceAll(userScript, "\n", "\r\n")
	got4, _ := Upsert(crlf, block)
	if strings.Count(got4, "\r\n") != strings.Count(got4, "\n") {
		t.Error("应保持 CRLF 换行")
	}
	if Strip(got4) != userScript {
		t.Error("CRLF 文件移除区块后应还原")
	}
}

func TestWriteScriptBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Script.js")
	if err := os.WriteFile(path, []byte(userScript), 0o644); err != nil {
		t.Fatal(err)
	}
	block := mustBlock(t, testData("auto"))
	changed, err := WriteScript(path, block)
	if err != nil || !changed {
		t.Fatalf("WriteScript: %v %v", changed, err)
	}
	bak, err := os.ReadFile(path + ".geoclash.bak")
	if err != nil || string(bak) != userScript {
		t.Fatal("应备份原始脚本")
	}
	if changed, _ := WriteScript(path, block); changed {
		t.Error("内容未变化时不应写入")
	}
	if _, err := WriteScript(path, mustBlock(t, testData("all"))); err != nil {
		t.Fatal(err)
	}
	if bak2, _ := os.ReadFile(path + ".geoclash.bak"); string(bak2) != userScript {
		t.Error("已有备份时不应覆盖")
	}
	removed, err := RemoveFromScript(path)
	if err != nil || !removed {
		t.Fatalf("RemoveFromScript: %v %v", removed, err)
	}
	if b, _ := os.ReadFile(path); string(b) != userScript {
		t.Errorf("移除后应与原始脚本一致：%q", b)
	}
}

func TestApplyRuntime(t *testing.T) {
	raw := `mixed-port: 7890
tun:
  enable: true
  stack: mixed
dns:
  enable: true
  enhanced-mode: fake-ip
external-controller-pipe: \\.\pipe\verge-mihomo
proxies:
  - {name: 香港01, type: ss, server: a.example, port: 443, cipher: aes-128-gcm, password: x}
proxy-groups:
  - {name: 节点选择, type: select, proxies: [香港01, DIRECT]}
rules:
  - MATCH,节点选择
`
	out, err := ApplyRuntime([]byte(raw), mustBlock(t, testData("auto")))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	_ = yaml.Unmarshal([]byte(raw), &before)
	if err := yaml.Unmarshal(out, &after); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"mixed-port", "tun", "dns", "external-controller-pipe", "proxies"} {
		if !reflect.DeepEqual(before[k], after[k]) {
			t.Errorf("字段 %s 不应被修改：\n%v\n%v", k, before[k], after[k])
		}
	}
	if !strings.Contains(string(out), "interval: 3600") {
		t.Error("整数应保持为整数")
	}
	var g map[string]any
	for _, gi := range after["proxy-groups"].([]any) {
		if m := gi.(map[string]any); m["name"] == "节点选择" {
			g = m
		}
	}
	if g == nil || g["proxies"].([]any)[0] != "🌍 实测国家" {
		t.Errorf("「节点选择」应挂接实测分组：%v", g)
	}
}

func TestPreview(t *testing.T) {
	raw, _ := yaml.Marshal(sampleConfig())
	p, err := Preview(raw, testData("auto"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]AttachPreview{}
	for _, x := range p {
		got[x.Name] = x
	}
	if !got["节点选择"].Attached || !got["节点选择"].First || got["全球直连"].Attached || len(p) != 7 {
		t.Errorf("预览结果不正确：%+v", p)
	}
}

// 实测分组位于分组列表最前面：选择组在首位，其后是国家组，再后是原有分组（顺序不变）。
func TestGroupOrder(t *testing.T) {
	out := run(t, "", testData("auto"), sampleConfig())
	var names []string
	for _, gi := range out["proxy-groups"].([]any) {
		names = append(names, gi.(map[string]any)["name"].(string))
	}
	want := []string{"🌍 实测国家", "🇺🇸 美国-实测", "🇯🇵 日本-实测", "节点选择", "自动选择", "流媒体", "苹果服务", "全球直连", "广告拦截", "漏网之鱼"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("分组顺序 = %v\n期望 %v", names, want)
	}
	// 重复执行顺序不变
	again := run(t, "", testData("auto"), out)
	if !reflect.DeepEqual(again["proxy-groups"], out["proxy-groups"]) {
		t.Error("重复执行后分组顺序变化")
	}
}
