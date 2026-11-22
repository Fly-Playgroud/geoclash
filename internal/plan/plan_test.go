package plan

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"geoclash/internal/config"
	"geoclash/internal/store"
)

func node(key, name, country, city, ip string) store.NodeState {
	return store.NodeState{Key: key, Name: name, Country: country, City: city, IP: ip,
		Mapping: map[string]any{"name": name, "type": "ss", "server": key, "port": 443}}
}

func cfg(t *testing.T, y string) *config.Config {
	t.Helper()
	c, err := config.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sample() []store.NodeState {
	return []store.NodeState{
		node("a", "日本07", "US", "洛杉矶", "1.1.23.7"),
		node("b", "日本01", "JP", "东京", "2.2.5.140"),
		node("c", "日本02", "JP", "东京", "2.2.5.141"),
		node("d", "日本03", "JP", "大阪", "2.2.5.142"),
		node("e", "新加坡01", "SG", "", "3.3.3.3"),
		node("f", "新加坡02", "SG", "", "3.3.3.4"),
		{Key: "g", Name: "失效", Country: "US", FailCount: 2, Mapping: map[string]any{"name": "失效"}},
	}
}

func TestAutoModeOrder(t *testing.T) {
	p := Build(cfg(t, ""), sample(), nil)
	// prefer US 排第一，其余按节点数：JP(3) > SG(2)
	if got := p.IDs(); !reflect.DeepEqual(got, []string{"US", "JP", "SG"}) {
		t.Errorf("分组顺序 = %v", got)
	}
	if n := len(p.Find("US").Members); n != 1 {
		t.Errorf("失效节点（连续失败达到阈值）不应计入：%d", n)
	}
	if got := p.Find("US").Members[0].DisplayName; got != "🇺🇸 洛杉矶 .23.7 | 日本07" {
		t.Errorf("实测名称 = %q", got)
	}
}

// 分组集合不变时沿用上次的顺序，避免节点数波动引起脚本更新和热重载。
func TestStableOrder(t *testing.T) {
	p := Build(cfg(t, ""), sample(), []string{"SG", "US", "JP"})
	if got := p.IDs(); !reflect.DeepEqual(got, []string{"SG", "US", "JP"}) {
		t.Errorf("集合不变时应沿用上次顺序：%v", got)
	}
	p = Build(cfg(t, ""), sample(), []string{"SG", "US"})
	if got := p.IDs(); !reflect.DeepEqual(got, []string{"US", "JP", "SG"}) {
		t.Errorf("集合变化时应重新排序：%v", got)
	}
}

func TestAutoMinNodes(t *testing.T) {
	p := Build(cfg(t, "auto-min-nodes: 2"), sample(), nil)
	if got := p.IDs(); !reflect.DeepEqual(got, []string{"JP", "SG"}) {
		t.Errorf("分组 = %v", got)
	}
}

func TestExplicitWithOthers(t *testing.T) {
	p := Build(cfg(t, "countries: [JP, {id: NA, name: 北美, codes: [US, CA]}, KR]\nothers: true"), sample(), nil)
	// NA 命中 prefer 的 US 排第一；其余按节点数：JP(3) > OTHERS(2)；没有节点的 KR 排最后
	if got := p.IDs(); !reflect.DeepEqual(got, []string{"NA", "JP", "OTHERS", "KR"}) {
		t.Errorf("分组 = %v", got)
	}
	if len(p.Find("KR").Members) != 0 {
		t.Error("显式列出但没有节点的分组应保留且为空")
	}
	if len(p.Find("OTHERS").Members) != 2 {
		t.Errorf("SG 节点应归入其他：%d", len(p.Find("OTHERS").Members))
	}
}

func TestProviderYAML(t *testing.T) {
	p := Build(cfg(t, ""), sample(), nil)
	b, err := ProviderYAML(p.Find("JP"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Proxies) != 3 || doc.Proxies[0]["server"] != "b" || !strings.HasPrefix(doc.Proxies[0]["name"].(string), "🇯🇵 东京") {
		t.Errorf("provider 内容 = %v", doc.Proxies)
	}
	if sample()[1].Mapping["name"] != "日本01" {
		t.Error("不应修改原始节点")
	}
}

func TestDedupeNames(t *testing.T) {
	nodes := []store.NodeState{
		node("a", "同名", "US", "洛杉矶", "1.1.1.1"),
		node("b", "同名", "US", "洛杉矶", "1.1.1.1"),
	}
	p := Build(cfg(t, ""), nodes, nil)
	m := p.Find("US").Members
	if m[0].DisplayName == m[1].DisplayName || !strings.HasSuffix(m[1].DisplayName, " #2") {
		t.Errorf("重名应去重：%q %q", m[0].DisplayName, m[1].DisplayName)
	}
}
