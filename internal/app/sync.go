package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"geoclash/internal/config"
	"geoclash/internal/controller"
	"geoclash/internal/discover"
	"geoclash/internal/verge"
)

// 自动切换的处理结果。
const (
	OutcomeSwitched   = "switched"     // 已切换到实测分组
	OutcomeBuiltin    = "skip-builtin" // 当前最终走 DIRECT / REJECT 等内置策略，不切换
	OutcomeAlready    = "already"      // 当前链路已经经过实测分组
	OutcomeNotFound   = "not-found"    // 分组不存在
	OutcomeNotAttach  = "not-attached" // 分组存在，但没有挂接实测分组（不是 select 组或不在挂接范围内）
	OutcomeFailed     = "failed"       // 切换请求失败，下次重试
	OutcomeNoSelector = "no-selector"  // 实测分组尚未加载到内核
)

// OutcomeText 返回处理结果的中文说明。
func OutcomeText(o string) string {
	switch o {
	case OutcomeSwitched:
		return "已切换"
	case OutcomeBuiltin:
		return "跳过（当前走内置策略）"
	case OutcomeAlready:
		return "跳过（已经经过实测分组）"
	case OutcomeNotFound:
		return "跳过（分组不存在）"
	case OutcomeNotAttach:
		return "跳过（不是 select 组或不在挂接范围内）"
	case OutcomeFailed:
		return "失败（下次重试）"
	case OutcomeNoSelector:
		return "跳过（实测分组尚未加载到内核）"
	}
	return o
}

// SwitchResult 是一个分组的自动切换结果。
type SwitchResult struct {
	Group   string `json:"group"`
	Outcome string `json:"outcome"`
	Text    string `json:"text"`
}

// controllerFor 返回可用的 controller 客户端：优先使用配置，其次自动发现；不可用时返回 nil。
func (a *App) controllerFor(ctx context.Context, env *discover.Env) (*controller.Client, error) {
	c := a.cfg.Controller
	if c.URL == "" && env != nil {
		c.URL = env.Controller.URL
		if c.Secret == "" {
			c.Secret = env.Controller.Secret
		}
	}
	if c.URL == "" {
		return nil, errors.New("没有找到可用的 controller")
	}
	cl, err := controller.New(c.URL, c.Secret)
	if err != nil {
		return nil, err
	}
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := cl.Version(vctx); err != nil {
		return nil, fmt.Errorf("controller %s 不可用：%w", c.URL, err)
	}
	return cl, nil
}

// syncClash 把本轮结果同步到 Clash：写入脚本区块 → 热重载或刷新 provider → 首次自动切换。
// 返回需要在状态页提示的信息。
func (a *App) syncClash(ctx context.Context, env *discover.Env, snap *Snapshot) []string {
	var notices []string
	isVerge := env != nil && env.Kind == discover.KindVerge
	scriptChanged := false

	// 1. 写入全局扩展脚本
	if isVerge && a.cfg.Verge.WriteScript {
		switch {
		case env.GlobalScript == "":
			notices = append(notices, "没有找到 Clash Verge Rev 的全局扩展脚本，无法自动写入")
		default:
			if script, err := os.ReadFile(env.GlobalScript); err == nil && verge.HasConstMain(string(script)) {
				notices = append(notices, "全局扩展脚本用 const 声明了 main，geoclash 区块无法生效；请改为 function main 或 var main")
			}
			changed, err := verge.WriteScript(env.GlobalScript, snap.Block)
			if err != nil {
				notices = append(notices, "写入全局扩展脚本失败："+err.Error())
			} else if changed {
				scriptChanged = true
				a.log.Printf("已更新全局扩展脚本中的 geoclash 区块：%s", env.GlobalScript)
			}
		}
	}

	ctl, err := a.controllerFor(ctx, env)
	if err != nil {
		notices = append(notices, err.Error()+"；provider 将依靠自身的刷新周期更新")
		return notices
	}
	proxies, err := ctl.Proxies(ctx)
	if err != nil {
		return append(notices, "读取内核分组失败："+err.Error())
	}

	// 2. 实测分组结构与内核不一致（或区块内容变化，例如修改了分组类型）时热重载，否则只刷新 provider
	want := make([]string, len(snap.Plan.Groups))
	for i, g := range snap.Plan.Groups {
		want[i] = g.Name
	}
	sel, loaded := proxies[a.cfg.SelectorName]
	needReload := len(want) > 0 && (!loaded || !sameSet(sel.All, want) || scriptChanged)
	reloaded := false
	if needReload {
		switch {
		case !isVerge:
			notices = append(notices, "实测分组已变化：请用 geoclash script 获取最新脚本区块并重新生成 Clash 配置")
		case !a.cfg.Verge.HotReload:
			notices = append(notices, "实测分组已变化：将在 Clash Verge Rev 下次生成配置时生效（hot-reload 已关闭）")
		default:
			if err := a.hotReload(ctx, ctl, env, snap.Block); err != nil {
				notices = append(notices, "热重载失败："+err.Error())
			} else {
				reloaded = true
				a.log.Printf("已热重载内核配置（实测分组：%v）", want)
			}
		}
	}
	if !reloaded {
		for _, g := range snap.Plan.Groups {
			if _, ok := snap.Providers[g.ID]; !ok {
				continue
			}
			err := ctl.RefreshProvider(ctx, verge.ProviderPrefix+g.ID)
			if err != nil && !errors.Is(err, controller.ErrNotFound) {
				a.log.Printf("刷新 provider %s 失败：%v", g.ID, err)
			}
		}
	}

	// 3. 首次自动切换
	if a.cfg.AutoSwitch == config.SwitchOnce {
		if proxies, err = ctl.Proxies(ctx); err == nil {
			for _, r := range a.autoSwitch(ctx, ctl, proxies, profileOf(env), nil) {
				if r.Outcome == OutcomeSwitched {
					a.log.Printf("首次挂接，已把分组「%s」切换到「%s」", r.Group, a.cfg.SelectorName)
				}
			}
		}
	}
	notices = append(notices, a.attachNotices(proxies)...)
	// 确认实测分组已加载到内核（热重载后需要重新读取）
	if latest, err := ctl.Proxies(ctx); err == nil {
		_, ok := latest[a.cfg.SelectorName]
		a.synced.Store(ok)
	}
	return notices
}

// hotReload 把区块应用到 Verge 的运行时配置并提交给内核。
func (a *App) hotReload(ctx context.Context, ctl *controller.Client, env *discover.Env, block string) error {
	raw, err := os.ReadFile(env.RuntimeConfig)
	if err != nil {
		return fmt.Errorf("读取运行时配置失败：%w", err)
	}
	payload, err := verge.ApplyRuntime(raw, block)
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return ctl.Reload(rctx, payload)
}

// attachNotices 提示显式列表中不存在或无法挂接的分组。
func (a *App) attachNotices(proxies map[string]controller.Proxy) []string {
	if a.cfg.AttachTo.Mode != config.AttachList || proxies == nil {
		return nil
	}
	if _, ok := proxies[a.cfg.SelectorName]; !ok {
		return nil
	}
	var out []string
	for _, name := range a.cfg.AttachTo.Groups {
		p, ok := proxies[name]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("attach-to 中的分组「%s」在当前订阅中不存在，已跳过", name))
		case p.Type != "Selector":
			out = append(out, fmt.Sprintf("attach-to 中的分组「%s」不是 select 组（%s），已跳过", name, p.Type))
		}
	}
	return out
}

func profileOf(env *discover.Env) string {
	if env == nil || env.ProfileName == "" {
		return "default"
	}
	return env.ProfileName
}

// autoSwitch 对已挂接、尚无切换记录的分组执行一次切换判断。only 非空时只处理这些分组（同时报告不存在的分组）。
func (a *App) autoSwitch(ctx context.Context, ctl *controller.Client, proxies map[string]controller.Proxy, profile string, only []string) []SwitchResult {
	selName := a.cfg.SelectorName
	var results []SwitchResult
	add := func(g, o string) { results = append(results, SwitchResult{Group: g, Outcome: o, Text: OutcomeText(o)}) }

	if _, ok := proxies[selName]; !ok {
		for _, g := range only {
			add(g, OutcomeNoSelector)
		}
		return results
	}

	var names []string
	if len(only) > 0 {
		names = only
	} else {
		for name := range proxies {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	for _, name := range names {
		p, ok := proxies[name]
		if !ok {
			add(name, OutcomeNotFound)
			continue
		}
		// GLOBAL 是内核内置的全局模式选择组，包含所有分组，不属于挂接对象
		if name == selName || name == "GLOBAL" || p.Type != "Selector" || !contains(p.All, selName) {
			if len(only) > 0 {
				add(name, OutcomeNotAttach)
			}
			continue
		}
		if _, done := a.store.Switched(profile, name); done {
			continue
		}
		outcome := decide(proxies, name, selName)
		if outcome == OutcomeSwitched {
			if err := ctl.Select(ctx, name, selName); err != nil {
				a.log.Printf("切换分组「%s」失败：%v", name, err)
				add(name, OutcomeFailed)
				continue
			}
		}
		a.store.RecordSwitch(profile, name, outcome)
		add(name, outcome)
	}
	return results
}

// decide 沿着 now 逐层解析分组当前的选择，决定是否需要切换。
func decide(proxies map[string]controller.Proxy, group, selector string) string {
	seen := map[string]bool{}
	cur := group
	for i := 0; i < 64; i++ {
		if cur == selector {
			return OutcomeAlready
		}
		if seen[cur] {
			return OutcomeBuiltin // 出现环路时保守处理，不切换
		}
		seen[cur] = true
		p, ok := proxies[cur]
		if !ok {
			return OutcomeSwitched // 未知名称视为真实节点
		}
		if p.IsBuiltin() {
			return OutcomeBuiltin
		}
		if !p.IsGroup() {
			return OutcomeSwitched
		}
		if p.Now == "" {
			return OutcomeBuiltin
		}
		cur = p.Now
	}
	return OutcomeBuiltin
}

// ResetSwitch 清除切换记录并立即重新判断。geoclash 正在运行时由 HTTP 接口调用。
func (a *App) ResetSwitch(ctx context.Context, groups []string, allProfiles bool) ([]SwitchResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	env, _ := a.discover()
	profile := profileOf(env)
	a.store.ResetSwitch(profile, groups, allProfiles)
	defer func() {
		if err := a.store.Save(); err != nil {
			a.log.Printf("保存状态失败：%v", err)
		}
	}()
	ctl, err := a.controllerFor(ctx, env)
	if err != nil {
		return nil, fmt.Errorf("记录已清除，但无法立即切换：%w", err)
	}
	proxies, err := ctl.Proxies(ctx)
	if err != nil {
		return nil, err
	}
	return a.autoSwitch(ctx, ctl, proxies, profile, groups), nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
		if m[s] < 0 {
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
