// Package store 保存检测结果、城市缓存和自动切换记录，并持久化为 JSON。
package store

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"geoclash/internal/probe"
	"geoclash/internal/source"
)

// NodeState 是一个节点的最新状态。
type NodeState struct {
	Key       string         `json:"key"`
	Order     int            `json:"order"` // 在来源中的顺序，用于稳定排序
	Name      string         `json:"name"`
	Source    string         `json:"source"`
	Mapping   map[string]any `json:"mapping"`
	Country   string         `json:"country,omitempty"`
	City      string         `json:"city,omitempty"`
	IP        string         `json:"ip,omitempty"`
	Confident bool           `json:"confident"`
	Answers   []probe.Answer `json:"answers,omitempty"`
	LastError string         `json:"lastError,omitempty"`
	FailCount int            `json:"failCount"` // 连续失败轮数
	CheckedAt time.Time      `json:"checkedAt"`
	LastOK    time.Time      `json:"lastOK,omitempty"`
}

// Effective 表示该节点当前是否计入国家组：有国家，且连续失败轮数未达到阈值。
func (n *NodeState) Effective(staleRounds int) bool {
	return n.Country != "" && n.FailCount < staleRounds
}

// SwitchRecord 记录某个分组首次挂接时的自动切换处理结果。
type SwitchRecord struct {
	Outcome string    `json:"outcome"`
	At      time.Time `json:"at"`
}

// Round 是最近一轮检测的摘要。
type Round struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Total    int       `json:"total"`
	OK       int       `json:"ok"`
	Failed   int       `json:"failed"`
	Excluded int       `json:"excluded"`
	Errors   []string  `json:"errors,omitempty"`
}

type data struct {
	Nodes    map[string]*NodeState              `json:"nodes"`
	Cities   map[string]string                  `json:"cities"`
	Switched map[string]map[string]SwitchRecord `json:"switched"` // 订阅名 → 分组名 → 记录
	Order    []string                           `json:"order"`    // 上次确定的分组顺序（分组 ID）
	Round    Round                              `json:"round"`
}

// Store 是并发安全的状态存储。
type Store struct {
	mu   sync.RWMutex
	path string
	d    data
}

// Open 读取状态文件；文件不存在时返回空状态。
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	s.d = data{
		Nodes:    map[string]*NodeState{},
		Cities:   map[string]string{},
		Switched: map[string]map[string]SwitchRecord{},
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		// 状态文件损坏不应阻止启动，丢弃即可
		return s, nil
	}
	if s.d.Nodes == nil {
		s.d.Nodes = map[string]*NodeState{}
	}
	if s.d.Cities == nil {
		s.d.Cities = map[string]string{}
	}
	if s.d.Switched == nil {
		s.d.Switched = map[string]map[string]SwitchRecord{}
	}
	return s, nil
}

// Save 原子写入状态文件。
func (s *Store) Save() error {
	s.mu.RLock()
	b, err := json.MarshalIndent(&s.d, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// NodeKey 用节点除名称以外的全部参数计算标识：机场改名不影响缓存，参数变化视为新节点。
func NodeKey(m map[string]any) string {
	c := make(map[string]any, len(m))
	for k, v := range m {
		if k != "name" {
			c[k] = v
		}
	}
	b, _ := json.Marshal(c) // map 的键会被排序，结果稳定
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:8])
}

// City 按出口 IP 查询缓存的城市。
func (s *Store) City(ip string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.d.Cities[ip]
	return c, ok
}

// ApplyRound 用检测结果更新状态。不在节点列表中的旧节点会被删除。
// results 的键为 NodeKey；缺失的键视为本次未检测（保留原有结果，用于增量检测）。
func (s *Store) ApplyRound(nodes []source.Node, results map[string]probe.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// 本次将被删除的旧节点，按身份（名称、协议、服务器、端口）索引：机场更新订阅时常常更换节点密码，
	// 节点标识随之改变，但它仍是同一个节点，先继承原有结果，避免分组在重新检测前被清空
	current := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		current[NodeKey(n.Mapping)] = true
	}
	retired := map[string]*NodeState{}
	for k, st := range s.d.Nodes {
		if !current[k] {
			retired[identity(st.Name, st.Mapping)] = st
		}
	}
	keep := make(map[string]*NodeState, len(nodes))
	for i, n := range nodes {
		key := NodeKey(n.Mapping)
		if _, dup := keep[key]; dup {
			continue // 完全相同的节点只保留一个
		}
		st := s.d.Nodes[key]
		if st == nil {
			st = &NodeState{Key: key}
			if old := retired[identity(n.Name, n.Mapping)]; old != nil {
				st.Country, st.City, st.IP, st.Confident = old.Country, old.City, old.IP, old.Confident
				st.Answers, st.LastOK, st.CheckedAt, st.FailCount = old.Answers, old.LastOK, old.CheckedAt, old.FailCount
			}
		}
		st.Order, st.Name, st.Source, st.Mapping = i, n.Name, n.Source, n.Mapping
		if r, probed := results[key]; probed {
			st.CheckedAt = now
			st.Answers = r.Answers
			if r.OK() {
				st.Country, st.City, st.IP, st.Confident = r.Country, r.City, r.IP, r.Confident
				st.LastError, st.FailCount, st.LastOK = "", 0, now
				if r.IP != "" && r.City != "" {
					s.d.Cities[r.IP] = r.City
				}
			} else {
				// 保留上次的国家，由 FailCount 决定是否仍计入国家组
				st.LastError = r.Err
				st.FailCount++
			}
		}
		keep[key] = st
	}
	s.d.Nodes = keep
	s.pruneCities()
}

// SetRound 记录最近一轮完整检测的摘要。
func (s *Store) SetRound(r Round) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Round = r
}

// Keys 返回当前所有节点的标识。
func (s *Store) Keys() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.d.Nodes))
	for k := range s.d.Nodes {
		out[k] = true
	}
	return out
}

// identity 返回节点的身份：名称、协议、服务器、端口相同即视为同一个节点（不含密码等凭据）。
func identity(name string, m map[string]any) string {
	return fmt.Sprint(name, "\x00", m["type"], "\x00", m["server"], "\x00", m["port"])
}

// pruneCities 只保留仍被节点使用的出口 IP 的城市缓存。
func (s *Store) pruneCities() {
	used := map[string]bool{}
	for _, n := range s.d.Nodes {
		used[n.IP] = true
	}
	for ip := range s.d.Cities {
		if !used[ip] {
			delete(s.d.Cities, ip)
		}
	}
}

// Nodes 返回按来源顺序排列的节点快照。
func (s *Store) Nodes() []NodeState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]NodeState, 0, len(s.d.Nodes))
	for _, n := range s.d.Nodes {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// Round 返回最近一轮检测的摘要。
func (s *Store) Round() Round {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.d.Round
}

// Order 返回上次确定的分组顺序。
func (s *Store) Order() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.d.Order...)
}

// SetOrder 记录分组顺序。
func (s *Store) SetOrder(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Order = append([]string(nil), ids...)
}

// Switched 查询分组的切换记录。
func (s *Store) Switched(profile, group string) (SwitchRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.d.Switched[profile][group]
	return r, ok
}

// RecordSwitch 记录分组的切换处理结果。
func (s *Store) RecordSwitch(profile, group, outcome string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.d.Switched[profile]
	if m == nil {
		m = map[string]SwitchRecord{}
		s.d.Switched[profile] = m
	}
	m[group] = SwitchRecord{Outcome: outcome, At: time.Now()}
}

// ResetSwitch 清除切换记录：allProfiles 为真时清除全部；groups 为空时清除该订阅的全部记录。
// 返回被清除的分组名。
func (s *Store) ResetSwitch(profile string, groups []string, allProfiles bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cleared []string
	if allProfiles {
		for _, m := range s.d.Switched {
			for g := range m {
				cleared = append(cleared, g)
			}
		}
		s.d.Switched = map[string]map[string]SwitchRecord{}
		return cleared
	}
	m := s.d.Switched[profile]
	if len(groups) == 0 {
		for g := range m {
			cleared = append(cleared, g)
		}
		delete(s.d.Switched, profile)
		return cleared
	}
	for _, g := range groups {
		if _, ok := m[g]; ok {
			delete(m, g)
			cleared = append(cleared, g)
		}
	}
	return cleared
}
