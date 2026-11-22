package store

import (
	"path/filepath"
	"testing"

	"geoclash/internal/probe"
	"geoclash/internal/source"
)

func n(name, server string) source.Node {
	return source.Node{Name: name, Mapping: map[string]any{"name": name, "type": "ss", "server": server, "port": 443}}
}

func TestNodeKeyIgnoresName(t *testing.T) {
	a := NodeKey(map[string]any{"name": "a", "server": "x", "port": 1})
	b := NodeKey(map[string]any{"name": "b", "server": "x", "port": 1})
	c := NodeKey(map[string]any{"name": "a", "server": "y", "port": 1})
	if a != b || a == c {
		t.Error("标识应忽略名称、区分参数")
	}
}

func TestStaleRounds(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	nodes := []source.Node{n("美国01", "s1")}
	key := NodeKey(nodes[0].Mapping)
	ok := probe.Result{Country: "US", City: "洛杉矶", IP: "1.1.1.1", Confident: true}
	bad := probe.Result{Err: "timeout"}

	s.ApplyRound(nodes, map[string]probe.Result{key: ok})
	if st := s.Nodes()[0]; !st.Effective(2) || st.Country != "US" {
		t.Fatalf("%+v", st)
	}
	s.ApplyRound(nodes, map[string]probe.Result{key: bad})
	if st := s.Nodes()[0]; !st.Effective(2) || st.FailCount != 1 || st.Country != "US" {
		t.Errorf("失败 1 轮应保留上次的国家：%+v", st)
	}
	s.ApplyRound(nodes, map[string]probe.Result{key: bad})
	if st := s.Nodes()[0]; st.Effective(2) {
		t.Errorf("连续失败 2 轮应移出：%+v", st)
	}
	s.ApplyRound(nodes, map[string]probe.Result{key: ok})
	if st := s.Nodes()[0]; !st.Effective(2) || st.FailCount != 0 {
		t.Errorf("恢复后应重新计入：%+v", st)
	}
}

func TestRemovedNodesAndCityCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, _ := Open(path)
	a, b := n("a", "s1"), n("b", "s2")
	ka, kb := NodeKey(a.Mapping), NodeKey(b.Mapping)
	s.ApplyRound([]source.Node{a, b}, map[string]probe.Result{
		ka: {Country: "US", City: "洛杉矶", IP: "1.1.1.1"},
		kb: {Country: "JP", City: "东京", IP: "2.2.2.2"},
	})
	if c, ok := s.City("2.2.2.2"); !ok || c != "东京" {
		t.Error("应缓存城市")
	}
	s.ApplyRound([]source.Node{a}, map[string]probe.Result{ka: {Country: "US", IP: "1.1.1.1"}})
	if len(s.Nodes()) != 1 {
		t.Error("订阅中已删除的节点应被移除")
	}
	if _, ok := s.City("2.2.2.2"); ok {
		t.Error("不再使用的出口 IP 的城市缓存应被清理")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, _ := Open(path)
	if len(s2.Nodes()) != 1 {
		t.Error("应能重新加载")
	}
}

func TestSwitchRecords(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	s.RecordSwitch("p1", "节点选择", "switched")
	s.RecordSwitch("p1", "Netflix", "skip-builtin")
	s.RecordSwitch("p2", "节点选择", "switched")
	if got := s.ResetSwitch("p1", []string{"Netflix", "不存在"}, false); len(got) != 1 {
		t.Errorf("只清除指定分组：%v", got)
	}
	if _, ok := s.Switched("p1", "节点选择"); !ok {
		t.Error("其他分组的记录应保留")
	}
	if got := s.ResetSwitch("p1", nil, false); len(got) != 1 {
		t.Errorf("清除当前订阅：%v", got)
	}
	if _, ok := s.Switched("p2", "节点选择"); !ok {
		t.Error("其他订阅的记录应保留")
	}
	s.ResetSwitch("", nil, true)
	if _, ok := s.Switched("p2", "节点选择"); ok {
		t.Error("--all-profiles 应清除全部")
	}
}

// 增量检测：没有检测结果的节点保留原有状态，新节点按结果写入，删除的节点被移除。
func TestIncrementalApply(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	a, b, c := n("a", "s1"), n("b", "s2"), n("c", "s3")
	ka, kc := NodeKey(a.Mapping), NodeKey(c.Mapping)
	s.ApplyRound([]source.Node{a, b}, map[string]probe.Result{
		ka: {Country: "US", IP: "1.1.1.1"}, NodeKey(b.Mapping): {Country: "JP", IP: "2.2.2.2"},
	})
	renamed := a
	renamed.Name = "a-改名"
	s.ApplyRound([]source.Node{renamed, c}, map[string]probe.Result{kc: {Country: "SG", IP: "3.3.3.3"}})
	got := map[string]string{}
	for _, st := range s.Nodes() {
		got[st.Name] = st.Country
	}
	if len(got) != 2 || got["a-改名"] != "US" || got["c"] != "SG" {
		t.Errorf("增量结果 = %v", got)
	}
	if keys := s.Keys(); !keys[ka] || !keys[kc] || len(keys) != 2 {
		t.Errorf("Keys = %v", keys)
	}
}

// 订阅更新后节点密码变化（标识改变）但名称、协议、服务器、端口不变：继承原有结果，检测失败时仍计入分组。
func TestInheritOnCredentialRotation(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "state.json"))
	old := source.Node{Name: "美国01", Mapping: map[string]any{"name": "美国01", "type": "hysteria2", "server": "a.example", "port": 443, "password": "old"}}
	s.ApplyRound([]source.Node{old}, map[string]probe.Result{NodeKey(old.Mapping): {Country: "US", City: "洛杉矶", IP: "1.1.1.1"}})

	rotated := source.Node{Name: "美国01", Mapping: map[string]any{"name": "美国01", "type": "hysteria2", "server": "a.example", "port": 443, "password": "new"}}
	k := NodeKey(rotated.Mapping)
	if k == NodeKey(old.Mapping) {
		t.Fatal("密码不同，标识应不同")
	}
	s.ApplyRound([]source.Node{rotated}, map[string]probe.Result{k: {Err: "network down"}})
	st := s.Nodes()[0]
	if st.Key != k || st.Country != "US" || st.City != "洛杉矶" || st.FailCount != 1 || !st.Effective(2) {
		t.Errorf("应继承原有结果并仍计入分组：%+v", st)
	}

	// 名称或服务器不同则不继承
	other := source.Node{Name: "美国02", Mapping: map[string]any{"name": "美国02", "type": "hysteria2", "server": "b.example", "port": 443}}
	s.ApplyRound([]source.Node{other}, map[string]probe.Result{NodeKey(other.Mapping): {Err: "x"}})
	if st := s.Nodes()[0]; st.Country != "" {
		t.Errorf("不同节点不应继承：%+v", st)
	}
}
