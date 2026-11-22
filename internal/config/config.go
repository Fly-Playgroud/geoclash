// Package config 负责 geoclash 配置文件的加载、默认值与校验。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"geoclash/internal/geo"
)

const (
	TypeURLTest  = "url-test"
	TypeFallback = "fallback"

	AttachAuto = "auto"
	AttachAll  = "all"
	AttachList = "list"
	AttachNone = "none"

	SwitchOnce  = "once"
	SwitchNever = "never"

	DefaultTestURL = "https://www.gstatic.com/generate_204"
)

// Duration 支持 "6h"、"10s" 这样的写法。
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(strings.TrimSpace(n.Value))
	if err != nil {
		return fmt.Errorf("第 %d 行：无效的时长 %q", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }

// Source 是一个节点来源：订阅链接或本地文件，二选一。
type Source struct {
	URL       string `yaml:"url"`
	File      string `yaml:"file"`
	UserAgent string `yaml:"user-agent"`
}

type Probe struct {
	Concurrency int      `yaml:"concurrency"`
	Timeout     Duration `yaml:"timeout"`
	MinAgree    int      `yaml:"min-agree"`
	StaleRounds int      `yaml:"stale-rounds"`
	Retries     int      `yaml:"retries"`
	Providers   []string `yaml:"providers"`
}

// Country 是一个实测分组，对应一个或多个国家代码。
type Country struct {
	ID        string   `yaml:"id"`
	Name      string   `yaml:"name"`
	Code      string   `yaml:"code"`
	Codes     []string `yaml:"codes"`
	Type      string   `yaml:"type"`
	TestURL   string   `yaml:"test-url"`
	Interval  int      `yaml:"interval"`
	Tolerance int      `yaml:"tolerance"`
}

// UnmarshalYAML 同时接受 "US" 和 {code: US, ...} 两种写法。
func (c *Country) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		c.Code = n.Value
		return nil
	}
	type plain Country
	return n.Decode((*plain)(c))
}

// AttachTo 取值为 auto / all / 分组名列表 / []。
type AttachTo struct {
	Mode   string
	Groups []string
}

func (a *AttachTo) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		switch v := strings.ToLower(strings.TrimSpace(n.Value)); v {
		case AttachAuto, AttachAll:
			a.Mode, a.Groups = v, nil
		case "", "none", "null", "~":
			a.Mode, a.Groups = AttachNone, nil
		default:
			// 单个分组名也接受
			a.Mode, a.Groups = AttachList, []string{n.Value}
		}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := n.Decode(&list); err != nil {
			return err
		}
		if len(list) == 0 {
			a.Mode, a.Groups = AttachNone, nil
		} else {
			a.Mode, a.Groups = AttachList, list
		}
		return nil
	}
	return fmt.Errorf("第 %d 行：attach-to 只能是 auto、all 或分组名列表", n.Line)
}

type Controller struct {
	URL    string `yaml:"url"`
	Secret string `yaml:"secret"`
}

type Verge struct {
	WriteScript bool `yaml:"write-script"`
	HotReload   bool `yaml:"hot-reload"`
}

type Config struct {
	Listen        string     `yaml:"listen"`
	Interval      Duration   `yaml:"interval"`
	Watch         Duration   `yaml:"watch"`
	WatchDebounce Duration   `yaml:"watch-debounce"`
	Sources       []Source   `yaml:"sources"`
	Exclude       []string   `yaml:"exclude"`
	Probe         Probe      `yaml:"probe"`
	DNS           []string   `yaml:"dns"`
	Interface     string     `yaml:"interface"`
	Rename        string     `yaml:"rename"`
	CityLang      string     `yaml:"city-lang"`
	Countries     []Country  `yaml:"countries"`
	AutoMinNodes  int        `yaml:"auto-min-nodes"`
	Others        bool       `yaml:"others"`
	SelectorName  string     `yaml:"selector-name"`
	Prefer        []string   `yaml:"prefer"`
	AttachTo      AttachTo   `yaml:"attach-to"`
	AttachExclude []string   `yaml:"attach-exclude"`
	AutoSwitch    string     `yaml:"auto-switch"`
	Controller    Controller `yaml:"controller"`
	Verge         Verge      `yaml:"verge"`

	// 以下为校验后填充的字段
	ExcludeRe       []*regexp.Regexp `yaml:"-"`
	AttachExcludeRe []*regexp.Regexp `yaml:"-"`
}

// Default 返回全部字段取默认值的配置。
func Default() *Config {
	return &Config{
		Listen:   "127.0.0.1:9876",
		Interval: Duration(6 * time.Hour),
		Watch:    Duration(30 * time.Second),
		// 发现来源文件变化后等待文件写完的时间
		WatchDebounce: Duration(10 * time.Second),
		Exclude:       []string{"剩余流量", "套餐到期", "过期时间", "下次重置", "官网", "电报群", "防失联"},
		Probe: Probe{
			Concurrency: 16,
			Timeout:     Duration(10 * time.Second),
			MinAgree:    2,
			StaleRounds: 2,
			Retries:     1,
			Providers:   []string{"cloudflare", "ipapi", "ipinfo", "ipwhois"},
		},
		Rename:       "{flag} {city} {ip_tail} | {name}",
		CityLang:     "zh-CN",
		AutoMinNodes: 1,
		SelectorName: "🌍 实测国家",
		Prefer:       []string{"US"},
		AttachTo:     AttachTo{Mode: AttachAuto},
		AutoSwitch:   SwitchOnce,
		Verge:        Verge{WriteScript: true, HotReload: true},
	}
}

// Dir 返回 geoclash 的数据目录（配置、状态、日志）：数据始终和可执行文件放在一起；
// 可执行文件所在目录不可写时（例如 /usr/local/bin）回退到 DefaultDir。
func Dir() (string, error) {
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		if dir := filepath.Dir(exe); writable(dir) {
			return dir, nil
		}
	}
	return DefaultDir()
}

// DefaultDir 返回默认安装目录：用户主目录下的 .geoclash（三个平台一致）。
// geoclash install 默认把程序复制到这里，数据随之存放在这里。
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".geoclash"), nil
}

// LegacyDir 返回旧版本使用的数据目录（用户配置目录下的 geoclash），只用于迁移。
func LegacyDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "geoclash"), nil
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".geoclash-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// DefaultPath 返回默认配置文件路径。
func DefaultPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.yaml"), nil
}

// Load 读取配置文件；文件不存在时写入带注释的默认配置。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(Template), 0o644); err != nil {
			return nil, fmt.Errorf("写入默认配置失败：%w", err)
		}
		data = []byte(Template)
	} else if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Read 读取配置文件；文件不存在时返回默认配置，但不写入文件（供 status、stop 等客户端命令使用）。
func Read(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Parse(nil)
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse 在默认值基础上解析配置并校验。
func Parse(data []byte) (*Config, error) {
	c := Default()
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("解析配置失败：%w", err)
	}
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) normalize() error {
	if c.Interval.D() < time.Minute {
		return errors.New("interval 不能小于 1m")
	}
	if c.Watch.D() < 0 || c.Watch.D() > 0 && c.Watch.D() < 5*time.Second {
		return errors.New("watch 只能为 0（关闭）或不小于 5s")
	}
	if c.WatchDebounce.D() < 0 || c.WatchDebounce.D() > 5*time.Minute {
		return errors.New("watch-debounce 只能在 0 到 5m 之间")
	}
	if c.Probe.Concurrency <= 0 {
		c.Probe.Concurrency = 1
	}
	if c.Probe.Timeout.D() <= 0 {
		c.Probe.Timeout = Duration(10 * time.Second)
	}
	if c.Probe.MinAgree <= 0 {
		c.Probe.MinAgree = 1
	}
	if c.Probe.Retries < 0 {
		c.Probe.Retries = 0
	}
	if c.Probe.StaleRounds <= 0 {
		c.Probe.StaleRounds = 1
	}
	if len(c.Probe.Providers) == 0 {
		return errors.New("probe.providers 不能为空")
	}
	if c.AutoMinNodes <= 0 {
		c.AutoMinNodes = 1
	}
	if c.SelectorName == "" {
		return errors.New("selector-name 不能为空")
	}
	switch c.AutoSwitch {
	case SwitchOnce, SwitchNever:
	default:
		return fmt.Errorf("auto-switch 只能是 once 或 never，当前为 %q", c.AutoSwitch)
	}
	switch c.CityLang {
	case "zh-CN", "en":
	default:
		return fmt.Errorf("city-lang 只能是 zh-CN 或 en，当前为 %q", c.CityLang)
	}
	for i, s := range c.Sources {
		if (s.URL == "") == (s.File == "") {
			return fmt.Errorf("sources[%d] 必须且只能设置 url 或 file 之一", i)
		}
	}
	var err error
	if c.ExcludeRe, err = compileAll("exclude", c.Exclude); err != nil {
		return err
	}
	if c.AttachExcludeRe, err = compileAll("attach-exclude", c.AttachExclude); err != nil {
		return err
	}
	for i, p := range c.Prefer {
		n := geo.Normalize(p)
		if n == "" {
			return fmt.Errorf("prefer[%d]：无效的国家代码 %q", i, p)
		}
		c.Prefer[i] = n
	}

	seenID := map[string]bool{}
	for i := range c.Countries {
		g := &c.Countries[i]
		if g.Code != "" && len(g.Codes) > 0 {
			return fmt.Errorf("countries[%d]：code 和 codes 只能设置一个", i)
		}
		if g.Code != "" {
			g.Codes = []string{g.Code}
		}
		if len(g.Codes) == 0 {
			return fmt.Errorf("countries[%d]：缺少 code 或 codes", i)
		}
		for j, code := range g.Codes {
			n := geo.Normalize(code)
			if n == "" {
				return fmt.Errorf("countries[%d]：无效的国家代码 %q", i, code)
			}
			g.Codes[j] = n
		}
		if g.ID == "" {
			if len(g.Codes) > 1 {
				return fmt.Errorf("countries[%d]：使用 codes 时必须设置 id", i)
			}
			g.ID = g.Codes[0]
		}
		if !validID(g.ID) {
			return fmt.Errorf("countries[%d]：id %q 只能包含字母、数字、- 和 _", i, g.ID)
		}
		if seenID[g.ID] {
			return fmt.Errorf("countries[%d]：id %q 重复", i, g.ID)
		}
		seenID[g.ID] = true
		g.applyDefaults()
		if g.Type != TypeURLTest && g.Type != TypeFallback {
			return fmt.Errorf("countries[%d]：type 只能是 url-test 或 fallback", i)
		}
	}
	return nil
}

// applyDefaults 补齐分组的默认值，名称按单国家自动生成。
func (g *Country) applyDefaults() {
	if g.Type == "" {
		g.Type = TypeURLTest
	}
	if g.TestURL == "" {
		g.TestURL = DefaultTestURL
	}
	if g.Interval <= 0 {
		g.Interval = 300
	}
	if g.Tolerance <= 0 {
		g.Tolerance = 50
	}
	if g.Name == "" && len(g.Codes) == 1 {
		g.Name = AutoGroupName(g.Codes[0])
	}
	if g.Name == "" {
		g.Name = g.ID + "-实测"
	}
}

// AutoGroupName 生成 "🇺🇸 美国-实测" 形式的分组名。
func AutoGroupName(code string) string {
	return geo.Flag(code) + " " + geo.Name(code) + "-实测"
}

// AutoCountry 为自动模式下检测到的国家生成分组定义。
func AutoCountry(code string) Country {
	g := Country{ID: code, Codes: []string{code}}
	g.applyDefaults()
	return g
}

// OthersCountry 是显式列表模式下「其他」分组的定义。
func OthersCountry() Country {
	g := Country{ID: "OTHERS", Name: "🏳️ 其他-实测"}
	g.applyDefaults()
	return g
}

func validID(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return s != ""
}

func compileAll(field string, patterns []string) ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for i, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] 不是有效的正则：%w", field, i, err)
		}
		out = append(out, re)
	}
	return out, nil
}

// MatchAny 判断 s 是否匹配任意一个正则。
func MatchAny(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
