package probe

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

type fake struct {
	name    string
	city    bool
	zh      bool
	answer  Answer
	err     error
	queried *[]string
}

func (f fake) Name() string  { return f.name }
func (f fake) HasCity() bool { return f.city }
func (f fake) ZhCity() bool  { return f.zh }
func (f fake) Query(context.Context, *http.Client) (Answer, error) {
	*f.queried = append(*f.queried, f.name)
	a := f.answer
	a.Provider = f.name
	return a, f.err
}

func newProber(minAgree int, providers ...Provider) *Prober {
	return &Prober{Providers: providers, Timeout: time.Second, MinAgree: minAgree}
}

func TestVote(t *testing.T) {
	cases := []struct {
		answers   []string
		country   string
		confident bool
	}{
		{[]string{"US", "US"}, "US", true},
		{[]string{"US", "JP", "JP"}, "JP", true},
		{[]string{"US", "JP"}, "US", false}, // 平票取最先出现的
		{[]string{"KR"}, "KR", false},
		{nil, "", false},
	}
	for _, c := range cases {
		var as []Answer
		for _, x := range c.answers {
			as = append(as, Answer{Country: x})
		}
		got, conf := Vote(as, 2)
		if got != c.country || conf != c.confident {
			t.Errorf("Vote(%v) = %s,%v，期望 %s,%v", c.answers, got, conf, c.country, c.confident)
		}
	}
}

func TestStopsWhenAgreed(t *testing.T) {
	var q []string
	p := newProber(2,
		fake{name: "cloudflare", answer: Answer{IP: "1.2.3.4", Country: "US"}, queried: &q},
		fake{name: "ipapi", city: true, zh: true, answer: Answer{IP: "1.2.3.4", Country: "US", City: "洛杉矶"}, queried: &q},
		fake{name: "ipinfo", city: true, answer: Answer{Country: "US", City: "Los Angeles"}, queried: &q},
	)
	r := p.run(context.Background(), nil, nil)
	if r.Country != "US" || !r.Confident || r.City != "洛杉矶" || r.IP != "1.2.3.4" {
		t.Errorf("结果 = %+v", r)
	}
	if len(q) != 2 {
		t.Errorf("达成一致后应停止查询，实际查询了 %v", q)
	}
}

// 城市只采信与投票结果一致的接口。
func TestCityFromAgreeingProviderOnly(t *testing.T) {
	var q []string
	p := newProber(2,
		fake{name: "ipapi", city: true, zh: true, answer: Answer{IP: "5.6.7.8", Country: "KR", City: "首尔"}, queried: &q},
		fake{name: "cloudflare", answer: Answer{IP: "5.6.7.8", Country: "JP"}, queried: &q},
		fake{name: "ipinfo", city: true, answer: Answer{Country: "JP", City: "Tokyo"}, queried: &q},
	)
	r := p.run(context.Background(), nil, nil)
	if r.Country != "JP" || r.City != "Tokyo" {
		t.Errorf("结果 = %+v", r)
	}
}

// 投票阶段没有问到中文城市接口时，补查一次。
func TestSupplementZhCity(t *testing.T) {
	var q []string
	p := newProber(2,
		fake{name: "cloudflare", answer: Answer{IP: "1.1.1.1", Country: "US"}, queried: &q},
		fake{name: "ipinfo", city: true, answer: Answer{IP: "1.1.1.1", Country: "US", City: "San Jose"}, queried: &q},
		fake{name: "ipapi", city: true, zh: true, answer: Answer{IP: "1.1.1.1", Country: "US", City: "圣何塞"}, queried: &q},
	)
	r := p.run(context.Background(), nil, nil)
	if r.City != "圣何塞" || len(q) != 3 {
		t.Errorf("应补查中文城市：%+v，查询 %v", r, q)
	}
}

func TestCityCacheWins(t *testing.T) {
	var q []string
	p := newProber(2,
		fake{name: "cloudflare", answer: Answer{IP: "1.1.1.1", Country: "US"}, queried: &q},
		fake{name: "ipinfo", city: true, answer: Answer{IP: "1.1.1.1", Country: "US", City: "San Jose"}, queried: &q},
		fake{name: "ipapi", city: true, zh: true, answer: Answer{Country: "US", City: "洛杉矶"}, queried: &q},
	)
	r := p.run(context.Background(), nil, func(ip string) (string, bool) { return "旧金山", ip == "1.1.1.1" })
	if r.City != "旧金山" || len(q) != 2 {
		t.Errorf("同一出口 IP 应使用缓存的城市：%+v，查询 %v", r, q)
	}
}

func TestDeadNodeStopsEarly(t *testing.T) {
	var q []string
	dead := errors.New("dial timeout")
	p := newProber(2,
		fake{name: "a", err: dead, queried: &q},
		fake{name: "b", err: dead, queried: &q},
		fake{name: "c", answer: Answer{Country: "US"}, queried: &q},
	)
	r := p.run(context.Background(), nil, nil)
	if r.OK() || r.Err == "" || len(q) != 2 {
		t.Errorf("连续两个接口失败应停止：%+v，查询 %v", r, q)
	}
}

func TestLowConfidence(t *testing.T) {
	var q []string
	p := newProber(2,
		fake{name: "a", answer: Answer{Country: "US"}, queried: &q},
		fake{name: "b", answer: Answer{Country: "JP"}, queried: &q},
		fake{name: "c", err: errors.New("429"), queried: &q},
	)
	r := p.run(context.Background(), nil, nil)
	if r.Country != "US" || r.Confident {
		t.Errorf("未达成一致应为低置信：%+v", r)
	}
}

func TestPickIPPrefersV4(t *testing.T) {
	as := []Answer{
		{Provider: "cloudflare", Country: "US", IP: "2001:db8::1"},
		{Provider: "x", Country: "JP", IP: "9.9.9.9"},
		{Provider: "ipapi", Country: "US", IP: "1.2.3.4"},
	}
	if got := PickIP(as, "US"); got != "1.2.3.4" {
		t.Errorf("应优先 IPv4：%s", got)
	}
	if got := PickIP(as[:1], "US"); got != "2001:db8::1" {
		t.Errorf("只有 IPv6 时使用 IPv6：%s", got)
	}
}
