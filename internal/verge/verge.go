// Package verge 生成 geoclash 脚本区块，写入 / 移除 Clash Verge Rev 全局扩展脚本中的区块，
// 并用内置 JS 引擎把同一段区块应用到运行时配置（热重载）。
package verge

import (
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/dop251/goja"
	mihomocfg "github.com/metacubex/mihomo/config"
	"gopkg.in/yaml.v3"
)

const (
	BeginMarker = "// ===== geoclash:begin =====（由 geoclash 自动生成，请勿修改此区块）"
	EndMarker   = "// ===== geoclash:end ====="
	beginPrefix = "// ===== geoclash:begin ====="

	ProviderPrefix = "geoclash-"
)

//go:embed apply.js
var applyJS string

// GroupDef 是注入到脚本中的分组定义。
type GroupDef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	URL       string `json:"url"`
	Interval  int    `json:"interval"`
	Tolerance int    `json:"tolerance"`
}

// Attach 是挂接规则。Mode 为 auto / all / list / none。
type Attach struct {
	Mode    string   `json:"mode"`
	Groups  []string `json:"groups"`
	Exclude []string `json:"exclude"`
}

// Data 是脚本区块中的全部数据。
type Data struct {
	Base             string     `json:"base"` // geoclash HTTP 地址，如 http://127.0.0.1:9876
	Prefix           string     `json:"prefix"`
	Selector         string     `json:"selector"`
	Groups           []GroupDef `json:"groups"`
	Attach           Attach     `json:"attach"`
	Processes        []string   `json:"processes"`
	ProviderInterval int        `json:"providerInterval"`
	PathDir          string     `json:"pathDir"`
}

// Block 生成完整的脚本区块（含首尾标记）。
func Block(d Data) (string, error) {
	if d.Prefix == "" {
		d.Prefix = ProviderPrefix
	}
	if d.Groups == nil {
		d.Groups = []GroupDef{}
	}
	if d.Attach.Groups == nil {
		d.Attach.Groups = []string{}
	}
	if d.Attach.Exclude == nil {
		d.Attach.Exclude = []string{}
	}
	if d.Processes == nil {
		d.Processes = []string{}
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", err
	}
	body := strings.Replace(applyJS, "__GEOCLASH_DATA__", string(b), 1)
	body = strings.ReplaceAll(body, "\r\n", "\n")
	return BeginMarker + "\n" + strings.TrimRight(body, "\n") + "\n" + EndMarker, nil
}

// Hash 返回区块内容的短哈希，用于判断是否需要重新写入或热重载。
func Hash(block string) string {
	s := sha1.Sum([]byte(block))
	return hex.EncodeToString(s[:6])
}

var blockRe = regexp.MustCompile(`(?s)\n*` + regexp.QuoteMeta(beginPrefix) + `[^\n]*\n.*?` + regexp.QuoteMeta(EndMarker) + `[^\n]*\n?`)

// Extract 返回脚本中现有的 geoclash 区块。
func Extract(script string) (string, bool) {
	loc := blockRe.FindStringIndex(normalizeEOL(script))
	if loc == nil {
		return "", false
	}
	return strings.Trim(normalizeEOL(script)[loc[0]:loc[1]], "\n"), true
}

// Strip 移除脚本中的 geoclash 区块，标记之外的内容保持不变（末尾空行除外）。
func Strip(script string) string {
	s := blockRe.ReplaceAllString(normalizeEOL(script), "\n")
	return strings.TrimRight(s, "\n") + "\n"
}

// Upsert 把区块写到脚本末尾（替换已有区块）；返回新内容以及内容是否变化。
func Upsert(script, block string) (string, bool) {
	if cur, ok := Extract(script); ok && cur == block {
		return script, false
	}
	user := strings.TrimRight(Strip(script), "\n")
	var out string
	if strings.TrimSpace(user) == "" {
		out = block + "\n"
	} else {
		out = user + "\n\n" + block + "\n"
	}
	if strings.Contains(script, "\r\n") {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, true
}

var constMainRe = regexp.MustCompile(`(?m)\bconst\s+main\s*=`)

// HasConstMain 判断用户脚本（区块之外）是否用 const 声明了 main；这种情况下区块无法包装 main。
func HasConstMain(script string) bool {
	return constMainRe.MatchString(Strip(script))
}

func normalizeEOL(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// WriteScript 把区块写入脚本文件。第一次写入前把原文件备份为 <path>.geoclash.bak（已有备份时不覆盖）。
// 只在内容变化时写入；每次都重新读取文件，保留用户对标记外内容的修改。
func WriteScript(path, block string) (changed bool, err error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	next, changed := Upsert(string(old), block)
	if !changed {
		return false, nil
	}
	bak := path + ".geoclash.bak"
	if _, statErr := os.Stat(bak); errors.Is(statErr, os.ErrNotExist) && len(old) > 0 {
		if _, had := Extract(string(old)); !had {
			if err := os.WriteFile(bak, old, 0o644); err != nil {
				return false, fmt.Errorf("备份脚本失败：%w", err)
			}
		}
	}
	return true, writeAtomic(path, []byte(next))
}

// RemoveFromScript 从脚本文件中移除区块；没有区块时不做任何修改。
func RemoveFromScript(path string) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if _, ok := Extract(string(old)); !ok {
		return false, nil
	}
	next := Strip(string(old))
	if strings.Contains(string(old), "\r\n") {
		next = strings.ReplaceAll(next, "\n", "\r\n")
	}
	return true, writeAtomic(path, []byte(next))
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".geoclash.tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Run 用 goja 执行「用户脚本 + 区块」（userScript 可为空），模拟 Verge 的调用方式，返回 main 的结果。
// 供热重载与测试使用。
func Run(userScript, block string, config map[string]any, profileName string) (map[string]any, []string, error) {
	vm := goja.New()
	var logs []string
	logf := func(level string) func(goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			parts := make([]string, len(c.Arguments))
			for i, a := range c.Arguments {
				parts[i] = a.String()
			}
			logs = append(logs, level+": "+strings.Join(parts, " "))
			return goja.Undefined()
		}
	}
	console := vm.NewObject()
	for _, l := range []string{"log", "info", "warn", "error", "debug"} {
		_ = console.Set(l, logf(l))
	}
	_ = vm.Set("console", console)

	in, err := json.Marshal(config)
	if err != nil {
		return nil, logs, err
	}
	_ = vm.Set("__input__", string(in))
	_ = vm.Set("__profile__", profileName)
	// 与 Verge 一致：脚本原文放在 try 块中，随后调用 main
	code := "(function(){ try {\n" + userScript + "\n;\n" + block + "\n;\n" +
		"return JSON.stringify(main(JSON.parse(__input__), __profile__));\n} catch (err) { return '__error__' + err; } })()"
	v, err := vm.RunString(code)
	if err != nil {
		return nil, logs, err
	}
	out := v.String()
	if strings.HasPrefix(out, "__error__") {
		return nil, logs, errors.New(strings.TrimPrefix(out, "__error__"))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, logs, fmt.Errorf("main 没有返回对象：%w", err)
	}
	return result, logs, nil
}

// ApplyRuntime 把区块应用到内核的运行时配置（YAML），只改动 proxy-providers、proxy-groups、rules，
// 返回可直接提交给 PUT /configs 的 YAML。
func ApplyRuntime(runtimeYAML []byte, block string) ([]byte, error) {
	var cfg map[string]any
	if err := yaml.Unmarshal(runtimeYAML, &cfg); err != nil {
		return nil, fmt.Errorf("解析运行时配置失败：%w", err)
	}
	if cfg == nil {
		return nil, errors.New("运行时配置为空")
	}
	result, _, err := Run("", block, jsonSafe(cfg).(map[string]any), "geoclash")
	if err != nil {
		return nil, fmt.Errorf("执行脚本区块失败：%w", err)
	}
	// 只取回允许修改的三个字段，其余字段保持运行时配置的原值
	for _, k := range []string{"proxy-providers", "proxy-groups", "rules"} {
		if v, ok := result[k]; ok {
			cfg[k] = restoreInts(v)
		}
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := mihomocfg.UnmarshalRawConfig(out); err != nil {
		return nil, fmt.Errorf("预校验失败：%w", err)
	}
	return out, nil
}

// jsonSafe 把 yaml.v3 解出的 map[interface{}]interface{} 等类型转换为可 JSON 序列化的类型。
func jsonSafe(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = jsonSafe(e)
		}
		return t
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, e := range t {
			m[fmt.Sprint(k)] = jsonSafe(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = jsonSafe(e)
		}
		return t
	}
	return v
}

// restoreInts 把 JSON 往返后变成 float64 的整数还原为 int，避免 YAML 中出现 1.0e+03 之类的写法。
func restoreInts(v any) any {
	switch t := v.(type) {
	case float64:
		if t == float64(int64(t)) {
			return int64(t)
		}
		return t
	case map[string]any:
		for k, e := range t {
			t[k] = restoreInts(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = restoreInts(e)
		}
		return t
	}
	return v
}

// AttachPreview 描述一个分组在某种挂接模式下的处理结果。
type AttachPreview struct {
	Name     string
	Type     string
	Attached bool
	First    bool // true 插入第一位，false 插入末尾
}

// Preview 对运行时配置试运行区块，返回每个原有分组的挂接结果（不修改任何文件）。
func Preview(runtimeYAML []byte, d Data) ([]AttachPreview, error) {
	var cfg map[string]any
	if err := yaml.Unmarshal(runtimeYAML, &cfg); err != nil {
		return nil, err
	}
	if len(d.Groups) == 0 {
		// 没有检测结果时用占位分组，挂接判断与分组内容无关
		d.Groups = []GroupDef{{ID: "PREVIEW", Name: "__geoclash_preview__", Type: "url-test", URL: "http://x", Interval: 300, Tolerance: 50}}
	}
	block, err := Block(d)
	if err != nil {
		return nil, err
	}
	result, _, err := Run("", block, jsonSafe(cfg).(map[string]any), "preview")
	if err != nil {
		return nil, err
	}
	own := map[string]bool{d.Selector: true}
	for _, g := range d.Groups {
		own[g.Name] = true
	}
	var out []AttachPreview
	groups, _ := result["proxy-groups"].([]any)
	for _, gi := range groups {
		g, _ := gi.(map[string]any)
		name, _ := g["name"].(string)
		if own[name] {
			continue
		}
		typ, _ := g["type"].(string)
		p := AttachPreview{Name: name, Type: typ}
		members, _ := g["proxies"].([]any)
		for i, m := range members {
			if m == d.Selector {
				p.Attached = true
				p.First = i == 0
			}
		}
		out = append(out, p)
	}
	return out, nil
}
