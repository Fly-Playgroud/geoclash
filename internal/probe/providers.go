package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"geoclash/internal/geo"
)

// Answer 是一个归属地接口的查询结果。
type Answer struct {
	Provider string `json:"provider"`
	IP       string `json:"ip"`
	Country  string `json:"country"`
	City     string `json:"city,omitempty"`
	Err      string `json:"error,omitempty"`
}

// Provider 是一个 IP 归属地接口。
type Provider interface {
	Name() string
	// HasCity 表示该接口能提供城市；ZhCity 表示城市名为中文。
	HasCity() bool
	ZhCity() bool
	Query(ctx context.Context, c *http.Client) (Answer, error)
}

// NewProvider 按名称创建接口；lang 为 zh-CN 或 en。
func NewProvider(name, lang string) (Provider, error) {
	switch name {
	case "cloudflare":
		return cloudflare{}, nil
	case "ipapi":
		return ipapi{lang: lang}, nil
	case "ipinfo":
		return ipinfo{}, nil
	case "ipwhois":
		return ipwhois{}, nil
	}
	return nil, fmt.Errorf("未知的归属地接口 %q（可选：cloudflare、ipapi、ipinfo、ipwhois）", name)
}

const userAgent = "geoclash"

func get(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func finish(a Answer, rawCountry string) (Answer, error) {
	a.Country = geo.Normalize(rawCountry)
	if a.Country == "" {
		return a, fmt.Errorf("无效的国家代码 %q", rawCountry)
	}
	a.City = strings.TrimSpace(a.City)
	return a, nil
}

// cloudflare：/cdn-cgi/trace 返回 ip= 和 loc=，无频率限制，没有城市。
type cloudflare struct{}

func (cloudflare) Name() string  { return "cloudflare" }
func (cloudflare) HasCity() bool { return false }
func (cloudflare) ZhCity() bool  { return false }
func (cloudflare) Query(ctx context.Context, c *http.Client) (Answer, error) {
	body, err := get(ctx, c, "https://www.cloudflare.com/cdn-cgi/trace")
	if err != nil {
		return Answer{}, err
	}
	a := Answer{Provider: "cloudflare"}
	var loc string
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			a.IP = v
		case "loc":
			loc = v
		}
	}
	return finish(a, loc)
}

// ipapi：ip-api.com，免费版只支持 HTTP，支持中文城市名。
type ipapi struct{ lang string }

func (ipapi) Name() string   { return "ipapi" }
func (ipapi) HasCity() bool  { return true }
func (p ipapi) ZhCity() bool { return p.lang == "zh-CN" }
func (p ipapi) Query(ctx context.Context, c *http.Client) (Answer, error) {
	body, err := get(ctx, c, "http://ip-api.com/json/?lang="+p.lang+"&fields=status,message,countryCode,city,query")
	if err != nil {
		return Answer{}, err
	}
	var r struct {
		Status, Message, CountryCode, City, Query string
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Answer{}, err
	}
	if r.Status != "success" {
		return Answer{}, errors.New("ip-api: " + r.Message)
	}
	return finish(Answer{Provider: "ipapi", IP: r.Query, City: r.City}, r.CountryCode)
}

// ipinfo：ipinfo.io，英文城市名。
type ipinfo struct{}

func (ipinfo) Name() string  { return "ipinfo" }
func (ipinfo) HasCity() bool { return true }
func (ipinfo) ZhCity() bool  { return false }
func (ipinfo) Query(ctx context.Context, c *http.Client) (Answer, error) {
	body, err := get(ctx, c, "https://ipinfo.io/json")
	if err != nil {
		return Answer{}, err
	}
	var r struct{ IP, City, Country string }
	if err := json.Unmarshal(body, &r); err != nil {
		return Answer{}, err
	}
	return finish(Answer{Provider: "ipinfo", IP: r.IP, City: r.City}, r.Country)
}

// ipwhois：ipwho.is，英文城市名。
type ipwhois struct{}

func (ipwhois) Name() string  { return "ipwhois" }
func (ipwhois) HasCity() bool { return true }
func (ipwhois) ZhCity() bool  { return false }
func (ipwhois) Query(ctx context.Context, c *http.Client) (Answer, error) {
	body, err := get(ctx, c, "https://ipwho.is/?fields=success,message,ip,country_code,city")
	if err != nil {
		return Answer{}, err
	}
	var r struct {
		Success     bool   `json:"success"`
		Message     string `json:"message"`
		IP          string `json:"ip"`
		CountryCode string `json:"country_code"`
		City        string `json:"city"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Answer{}, err
	}
	if !r.Success {
		return Answer{}, errors.New("ipwho.is: " + r.Message)
	}
	return finish(Answer{Provider: "ipwhois", IP: r.IP, City: r.City}, r.CountryCode)
}
