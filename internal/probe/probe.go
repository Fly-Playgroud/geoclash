// Package probe 通过节点本身访问 IP 归属地接口，得到节点的出口 IP、国家和城市。
package probe

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/metacubex/mihomo/adapter"
	C "github.com/metacubex/mihomo/constant"
)

// Result 是一个节点的检测结果。
type Result struct {
	IP        string   `json:"ip,omitempty"`
	Country   string   `json:"country,omitempty"`
	City      string   `json:"city,omitempty"`
	Confident bool     `json:"confident"`
	Answers   []Answer `json:"answers,omitempty"`
	Err       string   `json:"error,omitempty"`
}

// OK 表示成功得到了国家。
func (r Result) OK() bool { return r.Country != "" }

// CityLookup 按出口 IP 查询已缓存的城市。
type CityLookup func(ip string) (city string, ok bool)

// Prober 负责检测单个节点。可以被多个 goroutine 并发使用。
type Prober struct {
	Providers []Provider
	Timeout   time.Duration
	MinAgree  int
	Interface string // 非空时把检测流量绑定到该网卡
}

// Probe 检测一个节点。mapping 是 mihomo 格式的节点配置，不会被修改。
func (p *Prober) Probe(ctx context.Context, mapping map[string]any, cities CityLookup) Result {
	m := make(map[string]any, len(mapping)+1)
	for k, v := range mapping {
		m[k] = v
	}
	if p.Interface != "" {
		m["interface-name"] = p.Interface
	}
	proxy, err := adapter.ParseProxy(m)
	if err != nil {
		return Result{Err: "解析节点失败：" + err.Error()}
	}
	defer proxy.Close()

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			md := &C.Metadata{NetWork: C.TCP}
			if err := md.SetRemoteAddress(addr); err != nil {
				return nil, err
			}
			return proxy.DialContext(ctx, md)
		},
		TLSHandshakeTimeout:   p.Timeout,
		ResponseHeaderTimeout: p.Timeout,
		MaxIdleConnsPerHost:   2,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: p.Timeout}

	return p.run(ctx, client, cities)
}

// run 按顺序查询接口并投票；与网络无关的部分便于测试。
func (p *Prober) run(ctx context.Context, client *http.Client, cities CityLookup) Result {
	var (
		res     Result
		ok      []Answer
		counts  = map[string]int{}
		failRun int
		queried = map[string]bool{}
		lastErr error
	)
	for _, pv := range p.Providers {
		if ctx.Err() != nil {
			break
		}
		a, err := p.query(ctx, client, pv)
		queried[pv.Name()] = true
		if err != nil {
			lastErr = err
			res.Answers = append(res.Answers, Answer{Provider: pv.Name(), Err: err.Error()})
			failRun++
			// 一开始就连续两个接口失败，基本可以断定节点不可用，不再浪费时间
			if failRun >= 2 && len(ok) == 0 {
				break
			}
			continue
		}
		failRun = 0
		ok = append(ok, a)
		res.Answers = append(res.Answers, a)
		counts[a.Country]++
		if counts[a.Country] >= p.MinAgree {
			break
		}
	}

	country, confident := Vote(ok, p.MinAgree)
	if country == "" {
		if lastErr == nil {
			lastErr = errors.New("没有可用的归属地接口结果")
		}
		res.Err = lastErr.Error()
		return res
	}
	res.Country, res.Confident = country, confident
	res.IP = PickIP(ok, country)

	if city, hit := lookup(cities, res.IP); hit {
		res.City = city
		return res
	}
	var from Provider
	res.City, from = PickCity(ok, country, p.Providers)
	// 已经拿到中文城市，或者根本没有中文城市接口时，不需要补查
	if res.City != "" && (from.ZhCity() || !hasZh(p.Providers)) {
		return res
	}
	// 投票阶段没有问到能提供（中文）城市的接口：补查一个
	for _, pv := range p.Providers {
		if queried[pv.Name()] || !pv.HasCity() || (res.City != "" && !pv.ZhCity()) {
			continue
		}
		a, err := p.query(ctx, client, pv)
		if err != nil {
			res.Answers = append(res.Answers, Answer{Provider: pv.Name(), Err: err.Error()})
		} else {
			res.Answers = append(res.Answers, a)
			ok = append(ok, a)
			res.City, _ = PickCity(ok, country, p.Providers)
		}
		break
	}
	return res
}

func (p *Prober) query(ctx context.Context, client *http.Client, pv Provider) (Answer, error) {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	return pv.Query(ctx, client)
}

// PickIP 从国家与投票结果一致的接口中选出口 IP，优先 IPv4：
// 双栈节点的 IPv6 出口通常每个连接都不同，用它命名会导致节点名每轮都变化。
func PickIP(answers []Answer, country string) string {
	first := ""
	for _, a := range answers {
		if a.Country != country || a.IP == "" {
			continue
		}
		if addr, err := netip.ParseAddr(a.IP); err == nil && addr.Unmap().Is4() {
			return a.IP
		}
		if first == "" {
			first = a.IP
		}
	}
	return first
}

func hasZh(providers []Provider) bool {
	for _, pv := range providers {
		if pv.ZhCity() {
			return true
		}
	}
	return false
}

func lookup(cities CityLookup, ip string) (string, bool) {
	if cities == nil || ip == "" {
		return "", false
	}
	return cities(ip)
}

// Vote 统计各接口给出的国家：有 minAgree 个一致即为高置信；否则取多数（平票取最先出现的），为低置信。
func Vote(answers []Answer, minAgree int) (country string, confident bool) {
	counts := map[string]int{}
	var order []string
	for _, a := range answers {
		if a.Country == "" {
			continue
		}
		if counts[a.Country] == 0 {
			order = append(order, a.Country)
		}
		counts[a.Country]++
	}
	best := 0
	for _, c := range order {
		if counts[c] > best {
			country, best = c, counts[c]
		}
	}
	return country, country != "" && best >= minAgree
}

// PickCity 只采信国家与投票结果一致的接口：中文城市接口优先，其次按接口配置顺序。
// 返回城市及其来源接口；没有城市时返回空串和 nil。
func PickCity(answers []Answer, country string, providers []Provider) (string, Provider) {
	var (
		best     string
		bestFrom Provider
		bestRank = 1 << 30
	)
	for _, a := range answers {
		if a.Country != country || a.City == "" {
			continue
		}
		for i, pv := range providers {
			if pv.Name() != a.Provider {
				continue
			}
			r := i
			if pv.ZhCity() {
				r -= len(providers)
			}
			if r < bestRank {
				best, bestFrom, bestRank = a.City, pv, r
			}
		}
	}
	return best, bestFrom
}
