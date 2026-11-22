// Package plan 根据检测结果生成实测分组：分组列表、顺序、成员与 provider 内容。
package plan

import (
	"sort"

	"gopkg.in/yaml.v3"

	"geoclash/internal/config"
	"geoclash/internal/naming"
	"geoclash/internal/store"
)

// Member 是分组中的一个实测节点。
type Member struct {
	DisplayName string
	Node        store.NodeState
}

// Group 是一个实测分组。
type Group struct {
	config.Country
	Members []Member
}

// Plan 是一轮检测后的分组结果。Groups 的顺序即「🌍 实测国家」中的顺序。
type Plan struct {
	Groups []Group
	// Names 为节点 Key → 实测名称，供状态页显示
	Names map[string]string
}

// IDs 返回分组 ID 列表。
func (p *Plan) IDs() []string {
	ids := make([]string, len(p.Groups))
	for i, g := range p.Groups {
		ids[i] = g.ID
	}
	return ids
}

// Find 按 ID 查找分组。
func (p *Plan) Find(id string) *Group {
	for i := range p.Groups {
		if p.Groups[i].ID == id {
			return &p.Groups[i]
		}
	}
	return nil
}

// Build 生成分组。nodes 需按来源顺序排列；prevOrder 为上次确定的分组顺序。
//
// 分组集合不变时沿用 prevOrder，避免节点数量波动导致顺序变化、进而触发不必要的脚本更新与热重载；
// 集合变化时重新排序：先按 prefer 排列有节点的分组，再按节点数从多到少排列其余分组。
func Build(cfg *config.Config, nodes []store.NodeState, prevOrder []string) *Plan {
	p := &Plan{Names: map[string]string{}}

	dedupe := naming.NewDeduper()
	var effective []Member
	for _, n := range nodes {
		if !n.Effective(cfg.Probe.StaleRounds) {
			continue
		}
		name := dedupe.Unique(naming.Render(cfg.Rename, naming.Vars{Code: n.Country, City: n.City, IP: n.IP, Name: n.Name}))
		p.Names[n.Key] = name
		effective = append(effective, Member{DisplayName: name, Node: n})
	}

	var defs []config.Country
	explicit := len(cfg.Countries) > 0
	if explicit {
		defs = append(defs, cfg.Countries...)
		if cfg.Others {
			defs = append(defs, config.OthersCountry())
		}
	} else {
		counts := map[string]int{}
		for _, m := range effective {
			counts[m.Node.Country]++
		}
		for code, c := range counts {
			if c >= cfg.AutoMinNodes {
				defs = append(defs, config.AutoCountry(code))
			}
		}
	}

	groups := make([]Group, len(defs))
	covered := map[string]bool{}
	for i, d := range defs {
		groups[i].Country = d
		for _, c := range d.Codes {
			covered[c] = true
		}
	}
	for _, m := range effective {
		for i := range groups {
			if len(groups[i].Codes) == 0 { // 「其他」分组
				if !covered[m.Node.Country] {
					groups[i].Members = append(groups[i].Members, m)
				}
				continue
			}
			if contains(groups[i].Codes, m.Node.Country) {
				groups[i].Members = append(groups[i].Members, m)
			}
		}
	}

	p.Groups = order(groups, cfg.Prefer, prevOrder)
	return p
}

func order(groups []Group, prefer, prev []string) []Group {
	if sameSet(groups, prev) {
		pos := map[string]int{}
		for i, id := range prev {
			pos[id] = i
		}
		sort.SliceStable(groups, func(i, j int) bool { return pos[groups[i].ID] < pos[groups[j].ID] })
		return groups
	}
	rank := func(g Group) int {
		if len(g.Members) == 0 {
			return len(prefer) + 1 // 没有节点的分组排在最后
		}
		for i, code := range prefer {
			if contains(g.Codes, code) {
				return i
			}
		}
		return len(prefer)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		ri, rj := rank(groups[i]), rank(groups[j])
		if ri != rj {
			return ri < rj
		}
		if len(groups[i].Members) != len(groups[j].Members) {
			return len(groups[i].Members) > len(groups[j].Members)
		}
		return groups[i].ID < groups[j].ID
	})
	return groups
}

func sameSet(groups []Group, ids []string) bool {
	if len(groups) != len(ids) || len(ids) == 0 {
		return false
	}
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	for _, g := range groups {
		if !set[g.ID] {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ProviderYAML 生成分组的 provider 内容：节点参数保持原样，只替换名称。
func ProviderYAML(g *Group) ([]byte, error) {
	proxies := make([]map[string]any, 0, len(g.Members))
	for _, m := range g.Members {
		c := make(map[string]any, len(m.Node.Mapping))
		for k, v := range m.Node.Mapping {
			c[k] = v
		}
		c["name"] = m.DisplayName
		proxies = append(proxies, c)
	}
	return yaml.Marshal(map[string]any{"proxies": proxies})
}
