// Package controller 是 mihomo REST API 的客户端，支持 TCP、Windows 命名管道和 unix socket。
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Proxy 是 GET /proxies 返回的一项（节点或分组）。
type Proxy struct {
	Name string   `json:"name"`
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

// IsGroup 判断是否为会选择成员的分组。
func (p Proxy) IsGroup() bool {
	switch p.Type {
	case "Selector", "URLTest", "Fallback", "LoadBalance", "Relay", "Smart":
		return true
	}
	return false
}

// IsBuiltin 判断是否为内置策略（DIRECT、REJECT 等）。
func (p Proxy) IsBuiltin() bool {
	switch p.Type {
	case "Direct", "Reject", "RejectDrop", "Pass", "Compatible", "Dns":
		return true
	}
	return false
}

// ErrNotFound 表示请求的分组或 provider 不存在。
var ErrNotFound = errors.New("not found")

// Client 是 controller 客户端。
type Client struct {
	addr   string // 用于展示
	base   string // HTTP 基础地址
	secret string
	hc     *http.Client
}

// New 根据地址创建客户端：http://host:port、pipe:\\.\pipe\name、unix:/path。
func New(addr, secret string) (*Client, error) {
	c := &Client{addr: addr, secret: secret}
	var dial func(ctx context.Context) (net.Conn, error)
	switch {
	case strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://"):
		c.base = strings.TrimRight(addr, "/")
	case strings.HasPrefix(addr, "pipe:"):
		path := strings.TrimPrefix(addr, "pipe:")
		dial = func(ctx context.Context) (net.Conn, error) { return dialPipe(ctx, path) }
		c.base = "http://geoclash-pipe"
	case strings.HasPrefix(addr, "unix:"):
		path := strings.TrimPrefix(addr, "unix:")
		dial = func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
		c.base = "http://geoclash-unix"
	default:
		return nil, fmt.Errorf("不支持的 controller 地址 %q", addr)
	}
	tr := &http.Transport{Proxy: nil, MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second}
	if dial != nil {
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }
	}
	c.hc = &http.Client{Transport: tr, Timeout: 60 * time.Second}
	return c, nil
}

// Addr 返回 controller 地址。
func (c *Client) Addr() string { return c.addr }

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("controller %s %s：HTTP %d %s", method, path, resp.StatusCode, e.Message)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Version 用于检测 controller 是否可用。
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	err := c.do(ctx, http.MethodGet, "/version", nil, &v)
	return v.Version, err
}

// Proxies 返回全部节点与分组。
func (c *Client) Proxies(ctx context.Context) (map[string]Proxy, error) {
	var r struct {
		Proxies map[string]Proxy `json:"proxies"`
	}
	if err := c.do(ctx, http.MethodGet, "/proxies", nil, &r); err != nil {
		return nil, err
	}
	return r.Proxies, nil
}

// Select 把 select 分组切换到指定成员。
func (c *Client) Select(ctx context.Context, group, member string) error {
	return c.do(ctx, http.MethodPut, "/proxies/"+url.PathEscape(group), map[string]string{"name": member}, nil)
}

// RefreshProvider 让内核立即重新拉取 proxy-provider。
func (c *Client) RefreshProvider(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPut, "/providers/proxies/"+url.PathEscape(name), nil, nil)
}

// Reload 以 payload 形式提交完整配置并重载。内核会先完整解析，解析失败时返回错误且不会应用。
func (c *Client) Reload(ctx context.Context, payload []byte) error {
	return c.do(ctx, http.MethodPut, "/configs?force=false", map[string]string{"path": "", "payload": string(payload)}, nil)
}
