// Package app 编排一轮检测：读取节点 → 检测 → 生成分组 → 同步到 Clash，并按间隔循环执行。
package app

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/metacubex/mihomo/component/resolver"
	"github.com/metacubex/mihomo/dns"

	"geoclash/internal/config"
	"geoclash/internal/discover"
	"geoclash/internal/plan"
	"geoclash/internal/probe"
	"geoclash/internal/source"
	"geoclash/internal/store"
	"geoclash/internal/verge"
)

// Snapshot 是对外提供服务的只读数据，每轮检测后整体替换。
type Snapshot struct {
	Plan      *plan.Plan
	Providers map[string][]byte // 分组 ID → provider YAML；没有节点的分组不在其中
	Block     string
	BlockHash string
	Env       *discover.Env
	EnvErr    string
	Notices   []string
	Nodes     []store.NodeState
	Round     store.Round
}

// App 是 geoclash 的运行实例。
type App struct {
	cfg    *config.Config
	store  *store.Store
	prober *probe.Prober
	log    *log.Logger
	client *http.Client // 拉取订阅用，不走系统代理

	mu      sync.Mutex // 串行化检测轮次与切换操作
	snap    atomic.Pointer[Snapshot]
	running atomic.Bool
	trigger chan struct{}
	lastFP  atomicString // 上次读取节点时来源文件的指纹
}

// atomicString 是并发安全的字符串。
type atomicString struct{ v atomic.Value }

func (s *atomicString) Load() string {
	v, _ := s.v.Load().(string)
	return v
}

func (s *atomicString) Store(v string) { s.v.Store(v) }

// New 创建实例并加载上次的状态。
func New(cfg *config.Config, st *store.Store, logger *log.Logger) (*App, error) {
	var providers []probe.Provider
	for _, name := range cfg.Probe.Providers {
		pv, err := probe.NewProvider(name, cfg.CityLang)
		if err != nil {
			return nil, err
		}
		providers = append(providers, pv)
	}
	if err := setupDNS(cfg.DNS); err != nil {
		return nil, err
	}
	a := &App{
		cfg:   cfg,
		store: st,
		log:   logger,
		prober: &probe.Prober{
			Providers: providers,
			Timeout:   cfg.Probe.Timeout.D(),
			MinAgree:  cfg.Probe.MinAgree,
			Interface: cfg.Interface,
		},
		client:  &http.Client{Transport: &http.Transport{Proxy: nil}},
		trigger: make(chan struct{}, 1),
	}
	// 启动时立即用上次的结果提供服务，不必等第一轮检测完成
	env, envErr := a.discover()
	a.publish(a.buildSnapshot(env, envErr, nil))
	return a, nil
}

// setupDNS 为节点服务器域名解析配置自定义 DNS；为空时使用系统 DNS。
func setupDNS(servers []string) error {
	if len(servers) == 0 {
		return nil
	}
	ns, err := dns.ParseNameServer(servers)
	if err != nil {
		return fmt.Errorf("dns 配置无效：%w", err)
	}
	r := dns.NewResolver(dns.Config{Main: ns, Default: ns})
	resolver.DefaultResolver = r.Resolver
	resolver.ProxyServerHostResolver = r.Resolver
	return nil
}

// Snapshot 返回当前快照。
func (a *App) Snapshot() *Snapshot { return a.snap.Load() }

// Running 表示是否正在执行一轮检测。
func (a *App) Running() bool { return a.running.Load() }

func (a *App) publish(s *Snapshot) { a.snap.Store(s) }

// Trigger 请求立即执行一轮检测；已有待执行的请求时忽略。
func (a *App) Trigger() {
	select {
	case a.trigger <- struct{}{}:
	default:
	}
}

// Run 立即执行一轮完整检测，然后按间隔循环；同时按 watch 间隔检查节点来源文件，
// 文件变化时执行增量检测。直到 ctx 结束。
func (a *App) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	var watch <-chan time.Time
	if a.cfg.Watch.D() > 0 {
		t := time.NewTicker(a.cfg.Watch.D())
		defer t.Stop()
		watch = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-watch:
			if a.sourcesChanged(ctx) {
				a.Incremental(ctx)
			}
			continue
		case <-timer.C:
		case <-a.trigger:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		a.Round(ctx)
		timer.Reset(a.cfg.Interval.D())
	}
}

// sourcesChanged 判断节点来源文件自上次读取后是否变化；变化后等待 watch-debounce，
// 确认文件不再变化（Clash 已写完）再返回 true。
func (a *App) sourcesChanged(ctx context.Context) bool {
	fp := a.fingerprint()
	if fp == "" || fp == a.lastFP.Load() {
		return false
	}
	wait := a.cfg.WatchDebounce.D()
	if wait <= 0 {
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		next := a.fingerprint()
		if next == fp {
			return true
		}
		fp = next // 仍在写入，继续等待
	}
}

// fingerprint 汇总本地节点来源文件（当前订阅文件及其 provider 缓存）的路径、修改时间与大小。
// 切换订阅会改变文件路径，因此同样会被发现。订阅链接不在监视范围内，由定期检测负责。
func (a *App) fingerprint() string {
	env, _ := a.discover()
	var b strings.Builder
	for _, s := range a.specs(env) {
		if s.URL != "" {
			continue
		}
		for _, f := range source.Files(s) {
			if st, err := os.Stat(f); err == nil {
				fmt.Fprintf(&b, "%s|%d|%d;", f, st.ModTime().UnixNano(), st.Size())
			} else {
				fmt.Fprintf(&b, "%s|-;", f)
			}
		}
	}
	return b.String()
}

// loadTargets 读取全部来源中的节点并排除不需要检测的节点。
func (a *App) loadTargets(ctx context.Context) (env *discover.Env, envErr error, targets []source.Node, excluded int, errs []string, ok bool) {
	a.lastFP.Store(a.fingerprint())
	env, envErr = a.discover()
	specs := a.specs(env)
	if len(specs) == 0 {
		msg := "没有可用的节点来源"
		if envErr != nil {
			msg += "：" + envErr.Error()
		}
		errs = append(errs, msg)
		a.log.Print(msg)
	}
	nodes, loadErrs := source.Load(ctx, specs, a.client)
	for _, e := range loadErrs {
		errs = append(errs, "读取节点失败："+e.Error())
		a.log.Printf("读取节点失败：%v", e)
	}
	for _, n := range nodes {
		if config.MatchAny(a.cfg.ExcludeRe, n.Name) {
			excluded++
			continue
		}
		targets = append(targets, n)
	}
	// 读取节点完全失败时不应覆盖上次的结果，避免订阅暂时不可用导致分组被清空
	ok = len(targets) > 0 || len(loadErrs) == 0 && len(specs) > 0
	return
}

// Round 执行一轮完整检测并同步到 Clash。
func (a *App) Round(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running.Store(true)
	defer a.running.Store(false)

	round := store.Round{Start: time.Now()}
	env, envErr, targets, excluded, errs, ok := a.loadTargets(ctx)
	round.Errors, round.Excluded, round.Total = errs, excluded, len(targets)
	a.log.Printf("开始检测：%d 个节点（排除 %d 个）", len(targets), excluded)

	results := a.probeAll(ctx, targets)
	if ctx.Err() != nil {
		return
	}
	for _, r := range results {
		if r.OK() {
			round.OK++
		} else {
			round.Failed++
		}
	}
	round.End = time.Now()
	if ok {
		a.store.ApplyRound(targets, results)
		a.store.SetRound(round)
	}
	a.log.Printf("检测完成：成功 %d，失败 %d，耗时 %s", round.OK, round.Failed, round.End.Sub(round.Start).Round(time.Second))
	a.finish(ctx, env, envErr, round.Errors)
}

// Incremental 在节点来源变化后执行增量检测：只检测新增的节点，移除已删除的节点，其余节点保留原有结果。
func (a *App) Incremental(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running.Store(true)
	defer a.running.Store(false)

	env, envErr, targets, _, errs, ok := a.loadTargets(ctx)
	if !ok {
		a.log.Print("节点来源已变化，但读取节点失败，保留现有结果")
		return
	}
	known := a.store.Keys()
	var fresh []source.Node
	current := map[string]bool{}
	for _, n := range targets {
		k := store.NodeKey(n.Mapping)
		if !known[k] && !current[k] {
			fresh = append(fresh, n)
		}
		current[k] = true
	}
	removed := 0
	for k := range known {
		if !current[k] {
			removed++
		}
	}
	if len(fresh) == 0 && removed == 0 {
		a.log.Print("节点来源已更新，节点没有变化")
	} else {
		a.log.Printf("节点来源已变化：新增 %d 个，删除 %d 个，开始增量检测", len(fresh), removed)
	}
	results := a.probeAll(ctx, fresh)
	if ctx.Err() != nil {
		return
	}
	okCount := 0
	for _, r := range results {
		if r.OK() {
			okCount++
		}
	}
	// 未出现在 results 中的节点保持原有结果；名称、顺序随来源更新
	a.store.ApplyRound(targets, results)
	if len(fresh) > 0 {
		a.log.Printf("增量检测完成：新增节点成功 %d，失败 %d", okCount, len(results)-okCount)
	}
	a.finish(ctx, env, envErr, errs)
}

// finish 重新生成分组并同步到 Clash，然后保存状态。
func (a *App) finish(ctx context.Context, env *discover.Env, envErr error, errs []string) {
	snap := a.buildSnapshot(env, envErr, errs)
	a.store.SetOrder(snap.Plan.IDs())
	a.publish(snap)

	notices := a.syncClash(ctx, env, snap)
	if len(notices) > 0 {
		s := *a.Snapshot()
		s.Notices = append(append([]string(nil), s.Notices...), notices...)
		a.publish(&s)
	}
	if err := a.store.Save(); err != nil {
		a.log.Printf("保存状态失败：%v", err)
	}
}

// probeAll 并发检测全部节点；失败的节点在本轮内按 retries 重试，应对时通时断的节点。
func (a *App) probeAll(ctx context.Context, nodes []source.Node) map[string]probe.Result {
	results := a.probePass(ctx, nodes)
	// 全部失败多半是网络尚未就绪（例如刚从睡眠中唤醒，Clash 就更新了订阅），等一会儿再整体重测一次
	if len(nodes) >= 3 && countOK(results) == 0 && ctx.Err() == nil {
		a.log.Printf("全部 %d 个节点检测失败，可能网络尚未就绪，%s 后重试", len(nodes), outageWait)
		select {
		case <-ctx.Done():
			return results
		case <-time.After(outageWait):
		}
		results = a.probePass(ctx, nodes)
	}
	for i := 0; i < a.cfg.Probe.Retries && ctx.Err() == nil; i++ {
		var failed []source.Node
		for _, n := range nodes {
			if r, ok := results[store.NodeKey(n.Mapping)]; ok && !r.OK() {
				failed = append(failed, n)
			}
		}
		if len(failed) == 0 {
			break
		}
		a.log.Printf("重试失败的节点：%d 个", len(failed))
		for k, r := range a.probePass(ctx, failed) {
			if r.OK() {
				results[k] = r
			}
		}
	}
	return results
}

// outageWait 是全部节点检测失败时，整体重测前的等待时间。
const outageWait = 60 * time.Second

func countOK(results map[string]probe.Result) int {
	n := 0
	for _, r := range results {
		if r.OK() {
			n++
		}
	}
	return n
}

func (a *App) probePass(ctx context.Context, nodes []source.Node) map[string]probe.Result {
	results := make(map[string]probe.Result, len(nodes))
	var mu sync.Mutex
	sem := make(chan struct{}, a.cfg.Probe.Concurrency)
	var wg sync.WaitGroup
	perNode := a.cfg.Probe.Timeout.D()*time.Duration(len(a.prober.Providers)+1) + 5*time.Second
	for _, n := range nodes {
		key := store.NodeKey(n.Mapping)
		mu.Lock()
		_, dup := results[key]
		results[key] = probe.Result{} // 占位，避免重复节点被检测两次
		mu.Unlock()
		if dup {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(n source.Node, key string) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if p := recover(); p != nil {
					mu.Lock()
					results[key] = probe.Result{Err: fmt.Sprintf("检测时发生异常：%v", p)}
					mu.Unlock()
				}
			}()
			nctx, cancel := context.WithTimeout(ctx, perNode)
			defer cancel()
			r := a.prober.Probe(nctx, n.Mapping, a.store.City)
			mu.Lock()
			results[key] = r
			mu.Unlock()
		}(n, key)
	}
	wg.Wait()
	return results
}

// discover 在需要时探测本机 Clash 环境。
func (a *App) discover() (*discover.Env, error) {
	env, err := discover.Discover()
	if err != nil && env == nil {
		return nil, err
	}
	return env, err
}

// specs 返回本轮的节点来源：优先使用配置，否则使用自动发现的当前订阅。
func (a *App) specs(env *discover.Env) []source.Spec {
	if len(a.cfg.Sources) > 0 {
		out := make([]source.Spec, 0, len(a.cfg.Sources))
		for _, s := range a.cfg.Sources {
			sp := source.Spec{URL: s.URL, File: s.File, UserAgent: s.UserAgent}
			if s.URL != "" {
				sp.Label = "订阅 " + hostOf(s.URL)
			} else {
				sp.Label = "文件 " + filepath.Base(s.File)
			}
			out = append(out, sp)
		}
		return out
	}
	if env != nil && env.ProfileFile != "" {
		return []source.Spec{{File: env.ProfileFile, BaseDir: env.Home, Label: env.ProfileName}}
	}
	return nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(无效链接)"
	}
	return u.Host
}

// buildSnapshot 根据当前状态生成快照：分组、provider 内容、脚本区块。
func (a *App) buildSnapshot(env *discover.Env, envErr error, roundErrs []string) *Snapshot {
	nodes := a.store.Nodes()
	p := plan.Build(a.cfg, nodes, a.store.Order())
	s := &Snapshot{
		Plan:      p,
		Providers: map[string][]byte{},
		Env:       env,
		Nodes:     nodes,
		Round:     a.store.Round(),
		Notices:   append([]string(nil), roundErrs...),
	}
	if envErr != nil {
		s.EnvErr = envErr.Error()
	}
	for i := range p.Groups {
		g := &p.Groups[i]
		if len(g.Members) == 0 {
			continue
		}
		y, err := plan.ProviderYAML(g)
		if err != nil {
			a.log.Printf("生成 provider %s 失败：%v", g.ID, err)
			continue
		}
		s.Providers[g.ID] = y
	}
	block, err := verge.Block(a.blockData(p))
	if err != nil {
		a.log.Printf("生成脚本区块失败：%v", err)
	}
	s.Block, s.BlockHash = block, verge.Hash(block)
	return s
}

// BlockData 返回当前分组对应的脚本区块数据。
func (a *App) blockData(p *plan.Plan) verge.Data {
	d := verge.Data{
		Base:             "http://" + a.publicAddr(),
		Prefix:           verge.ProviderPrefix,
		Selector:         a.cfg.SelectorName,
		Processes:        processNames(),
		ProviderInterval: 3600,
		PathDir:          "./geoclash",
		Attach: verge.Attach{
			Mode:    a.cfg.AttachTo.Mode,
			Groups:  a.cfg.AttachTo.Groups,
			Exclude: a.cfg.AttachExclude,
		},
	}
	for _, g := range p.Groups {
		d.Groups = append(d.Groups, verge.GroupDef{
			ID: g.ID, Name: g.Name, Type: g.Type, URL: g.TestURL, Interval: g.Interval, Tolerance: g.Tolerance,
		})
	}
	return d
}

// BlockData 供命令行预览使用。
func (a *App) BlockData() verge.Data { return a.blockData(a.Snapshot().Plan) }

// publicAddr 把监听地址转换为 Clash 可以访问的地址。
func (a *App) publicAddr() string {
	host, port, err := net.SplitHostPort(a.cfg.Listen)
	if err != nil {
		return a.cfg.Listen
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// processNames 返回 PROCESS-NAME 规则中使用的进程名：当前可执行文件名，以及标准名称。
func processNames() []string {
	std := "geoclash"
	if runtime.GOOS == "windows" {
		std += ".exe"
	}
	names := []string{std}
	if exe, err := os.Executable(); err == nil {
		if b := filepath.Base(exe); b != std {
			names = append(names, b)
		}
	}
	return names
}

// Config 返回配置。
func (a *App) Config() *config.Config { return a.cfg }
