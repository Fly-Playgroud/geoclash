package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTemplateParsesToDefaults(t *testing.T) {
	c, err := Parse([]byte(Template))
	if err != nil {
		t.Fatal(err)
	}
	if c.Interval.D() != 6*time.Hour || c.AttachTo.Mode != AttachAuto || c.AutoSwitch != SwitchOnce ||
		!c.Verge.WriteScript || !c.Verge.HotReload || len(c.Countries) != 0 || c.Prefer[0] != "US" {
		t.Errorf("默认配置不正确：%+v", c)
	}
}

func TestEmptyFile(t *testing.T) {
	c, err := Parse(nil)
	if err != nil || c.Listen != "127.0.0.1:9876" || c.Probe.MinAgree != 2 {
		t.Fatalf("空文件应使用默认值：%v %+v", err, c)
	}
}

func TestCountries(t *testing.T) {
	c, err := Parse([]byte(`
countries:
  - us
  - { code: JP, type: fallback }
  - { code: UK, name: 英国组, tolerance: 100 }
  - { id: EU, name: "🇪🇺 欧洲-实测", codes: [DE, fr] }
`))
	if err != nil {
		t.Fatal(err)
	}
	us, jp, gb, eu := c.Countries[0], c.Countries[1], c.Countries[2], c.Countries[3]
	if us.ID != "US" || us.Name != "🇺🇸 美国-实测" || us.Type != TypeURLTest || us.Interval != 300 || us.Tolerance != 50 {
		t.Errorf("US = %+v", us)
	}
	if jp.Type != TypeFallback {
		t.Errorf("JP = %+v", jp)
	}
	if gb.ID != "GB" || gb.Name != "英国组" || gb.Tolerance != 100 {
		t.Errorf("UK 应规范化为 GB：%+v", gb)
	}
	if eu.ID != "EU" || len(eu.Codes) != 2 || eu.Codes[1] != "FR" {
		t.Errorf("EU = %+v", eu)
	}
}

func TestAttachTo(t *testing.T) {
	cases := map[string]AttachTo{
		"attach-to: auto": {Mode: AttachAuto},
		"attach-to: ALL":  {Mode: AttachAll},
		"attach-to: []":   {Mode: AttachNone},
		"attach-to: [节点选择, 人工智能]": {Mode: AttachList, Groups: []string{"节点选择", "人工智能"}},
		"attach-to: 节点选择":         {Mode: AttachList, Groups: []string{"节点选择"}},
	}
	for in, want := range cases {
		c, err := Parse([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if c.AttachTo.Mode != want.Mode || len(c.AttachTo.Groups) != len(want.Groups) {
			t.Errorf("%s → %+v，期望 %+v", in, c.AttachTo, want)
		}
	}
}

func TestInvalid(t *testing.T) {
	for _, in := range []string{
		"countries: [XYZ]",
		"countries: [{ codes: [DE, FR] }]",
		"countries: [US, US]",
		"countries: [{ code: US, type: load-balance }]",
		"exclude: ['(']",
		"auto-switch: always",
		"interval: 10s",
		"prefer: [abc]",
		"sources: [{ url: a, file: b }]",
		"city-lang: ja",
		"watch: 2s",
		"watch-debounce: -1s",
		"watch-debounce: 10m",
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%q 应报错", in)
		}
	}
}

func TestLoadWritesTemplate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != Template {
		t.Error("首次运行应写入默认配置")
	}
}

func TestDirDefaultsToExecutableDir(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Dir(exe) {
		t.Errorf("Dir() = %q，期望可执行文件所在目录 %q", got, filepath.Dir(exe))
	}
}

func TestWatchSettings(t *testing.T) {
	c, err := Parse(nil)
	if err != nil || c.Watch.D() != 30*time.Second || c.WatchDebounce.D() != 10*time.Second {
		t.Fatalf("默认值不正确：%v %v %v", err, c.Watch.D(), c.WatchDebounce.D())
	}
	c, err = Parse([]byte("watch: 0\nwatch-debounce: 0s"))
	if err != nil || c.Watch.D() != 0 || c.WatchDebounce.D() != 0 {
		t.Fatalf("0 应表示关闭 / 立即处理：%v %+v", err, c)
	}
}
