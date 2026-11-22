// Package naming 负责实测节点的命名模板渲染与去重。
package naming

import (
	"fmt"
	"net/netip"
	"strings"

	"geoclash/internal/geo"
)

// Vars 是命名模板可用的变量。
type Vars struct {
	Code string // 国家代码
	City string
	IP   string // 出口 IP
	Name string // 原节点名
}

// Render 用 Vars 渲染模板；空占位符留下的多余空格会被合并，首尾的分隔符空白会被去掉。
func Render(tmpl string, v Vars) string {
	r := strings.NewReplacer(
		"{flag}", geo.Flag(v.Code),
		"{code}", v.Code,
		"{country}", countryName(v.Code),
		"{city}", v.City,
		"{ip}", v.IP,
		"{ip_tail}", IPTail(v.IP),
		"{name}", v.Name,
	)
	out := strings.Join(strings.Fields(r.Replace(tmpl)), " ")
	if out == "" {
		return v.Name
	}
	return out
}

func countryName(code string) string {
	if code == "" {
		return ""
	}
	return geo.Name(code)
}

// IPTail 返回出口 IP 的尾段：IPv4 为后两段（".23.7"），IPv6 为后两组（":ab12:34"）。
func IPTail(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	if addr.Is4() {
		b := addr.As4()
		return fmt.Sprintf(".%d.%d", b[2], b[3])
	}
	b := addr.As16()
	return fmt.Sprintf(":%x:%x", uint16(b[12])<<8|uint16(b[13]), uint16(b[14])<<8|uint16(b[15]))
}

// Deduper 为重名追加 " #2"、" #3" 后缀。
type Deduper struct{ seen map[string]int }

func NewDeduper() *Deduper { return &Deduper{seen: map[string]int{}} }

func (d *Deduper) Unique(name string) string {
	d.seen[name]++
	n := d.seen[name]
	if n == 1 {
		return name
	}
	for {
		cand := fmt.Sprintf("%s #%d", name, n)
		if _, used := d.seen[cand]; !used {
			d.seen[cand] = 1
			return cand
		}
		n++
	}
}
