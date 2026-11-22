package naming

import "testing"

func TestRender(t *testing.T) {
	const tmpl = "{flag} {city} {ip_tail} | {name}"
	cases := []struct {
		v    Vars
		want string
	}{
		{Vars{Code: "US", City: "洛杉矶", IP: "104.28.23.7", Name: "日本07"}, "🇺🇸 洛杉矶 .23.7 | 日本07"},
		{Vars{Code: "TW", City: "", IP: "1.2.61.9", Name: "台湾01家宽"}, "🇹🇼 .61.9 | 台湾01家宽"},
		{Vars{Code: "JP", City: "Tokyo", IP: "2001:db8::ab12:34", Name: "x"}, "🇯🇵 Tokyo :ab12:34 | x"},
	}
	for _, c := range cases {
		if got := Render(tmpl, c.v); got != c.want {
			t.Errorf("Render(%+v) = %q，期望 %q", c.v, got, c.want)
		}
	}
	if got := Render("[{code}] {country}-{name}", Vars{Code: "GB", Name: "n"}); got != "[GB] 英国-n" {
		t.Errorf("got %q", got)
	}
	if got := Render("{city}", Vars{Name: "原名"}); got != "原名" {
		t.Errorf("渲染结果为空时应回退为原名，got %q", got)
	}
}

func TestDeduper(t *testing.T) {
	d := NewDeduper()
	got := []string{d.Unique("a"), d.Unique("a"), d.Unique("a #2"), d.Unique("a")}
	want := []string{"a", "a #2", "a #2 #2", "a #3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v，期望 %v", got, want)
		}
	}
}
