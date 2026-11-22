// Package source 读取节点：订阅链接、本地 Clash 配置文件，以及自动发现的 Clash 当前订阅。
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/metacubex/mihomo/common/convert"
	"gopkg.in/yaml.v3"
)

// ProviderPrefix 是 geoclash 注入的 provider 名称前缀；读取节点时跳过，避免循环。
const ProviderPrefix = "geoclash-"

const defaultUA = "clash.meta"

// Node 是一个原始节点。
type Node struct {
	Name    string
	Source  string // 来源描述，用于状态页
	Mapping map[string]any
}

// Spec 描述一个节点来源。
type Spec struct {
	URL       string
	File      string
	BaseDir   string // file 中 provider 相对路径的基准目录；为空时使用文件所在目录
	UserAgent string
	Label     string
}

// Load 依次读取所有来源并合并。单个来源失败不影响其他来源；全部失败时返回错误。
func Load(ctx context.Context, specs []Spec, client *http.Client) ([]Node, []error) {
	var nodes []Node
	var errs []error
	for _, s := range specs {
		var (
			got []Node
			err error
		)
		if s.URL != "" {
			got, err = LoadURL(ctx, client, s)
		} else {
			got, err = LoadFile(s)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s：%w", s.Label, err))
			continue
		}
		nodes = append(nodes, got...)
	}
	return nodes, errs
}

// LoadURL 拉取订阅。
func LoadURL(ctx context.Context, client *http.Client, s Spec) ([]Node, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	ua := s.UserAgent
	if ua == "" {
		ua = defaultUA
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	maps, _, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return toNodes(maps, s.Label), nil
}

// LoadFile 读取本地 Clash 配置或订阅文件，包括其中 proxy-providers 的本地缓存。
func LoadFile(s Spec) ([]Node, error) {
	data, err := os.ReadFile(s.File)
	if err != nil {
		return nil, err
	}
	maps, providers, err := Parse(data)
	if err != nil {
		return nil, err
	}
	base := s.BaseDir
	if base == "" {
		base = filepath.Dir(s.File)
	}
	for name, pv := range providers {
		if strings.HasPrefix(name, ProviderPrefix) {
			continue
		}
		pm, err := pv.load(base)
		if err != nil {
			// provider 缓存可能尚未生成，跳过即可
			continue
		}
		maps = append(maps, pm...)
	}
	if len(maps) == 0 {
		return nil, errors.New("没有找到任何节点")
	}
	return toNodes(maps, s.Label), nil
}

// Files 返回本地来源依赖的文件：来源文件本身，以及其中 proxy-providers 的本地缓存（跳过 geoclash 自己的 provider）。
// 订阅链接来源返回空。用于监视来源变化。
func Files(s Spec) []string {
	if s.File == "" {
		return nil
	}
	files := []string{s.File}
	data, err := os.ReadFile(s.File)
	if err != nil {
		return files
	}
	_, providers, err := Parse(data)
	if err != nil {
		return files
	}
	base := s.BaseDir
	if base == "" {
		base = filepath.Dir(s.File)
	}
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names) // 保证顺序稳定，指纹才可比较
	for _, name := range names {
		pv := providers[name]
		if strings.HasPrefix(name, ProviderPrefix) || pv.Type == "inline" || pv.Path == "" {
			continue
		}
		path := pv.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		files = append(files, path)
	}
	return files
}

type provider struct {
	Type    string           `yaml:"type"`
	Path    string           `yaml:"path"`
	Payload []map[string]any `yaml:"payload"`
}

func (p provider) load(base string) ([]map[string]any, error) {
	if p.Type == "inline" {
		return p.Payload, nil
	}
	if p.Path == "" {
		return nil, errors.New("provider 没有 path")
	}
	path := p.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	maps, _, err := Parse(data)
	return maps, err
}

// Parse 解析 Clash YAML（proxies / proxy-providers），不是 YAML 时按 base64 或明文分享链接解析。
func Parse(data []byte) ([]map[string]any, map[string]provider, error) {
	var doc struct {
		Proxies   []map[string]any    `yaml:"proxies"`
		Providers map[string]provider `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal(data, &doc); err == nil && (len(doc.Proxies) > 0 || len(doc.Providers) > 0) {
		return doc.Proxies, doc.Providers, nil
	}
	maps, err := convert.ConvertsV2Ray(data)
	if err != nil {
		return nil, nil, fmt.Errorf("无法识别的订阅格式：%w", err)
	}
	if len(maps) == 0 {
		return nil, nil, errors.New("无法识别的订阅格式")
	}
	return maps, nil, nil
}

func toNodes(maps []map[string]any, label string) []Node {
	nodes := make([]Node, 0, len(maps))
	for _, m := range maps {
		name, _ := m["name"].(string)
		if name == "" {
			continue
		}
		nodes = append(nodes, Node{Name: name, Source: label, Mapping: m})
	}
	return nodes
}
