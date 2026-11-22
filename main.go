// geoclash：定期实测 Clash 节点的出口归属地，按实际国家生成分组并接入 Clash Verge Rev / mihomo。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	mlog "github.com/metacubex/mihomo/log"

	"geoclash/internal/app"
	"geoclash/internal/autostart"
	"geoclash/internal/config"
	"geoclash/internal/console"
	"geoclash/internal/controller"
	"geoclash/internal/discover"
	"geoclash/internal/server"
	"geoclash/internal/store"
	"geoclash/internal/verge"
)

var version = "dev"

const usage = `geoclash —— 按实测出口归属地为 Clash 节点分组

用法：geoclash <命令> [-c 配置文件] [参数]

命令：
  run           前台运行（默认命令）
  install       [--here] [--wait]  把程序复制到 %s 并安装开机自启、在后台启动；
                --here 表示程序留在当前位置，数据也放在当前位置；
                --wait 表示等实测分组接入 Clash 后再返回
  uninstall     移除开机自启、停止后台进程、移除 Verge 脚本中的 geoclash 区块
  start         [--wait]  在后台启动
  stop          停止后台进程
  status        查看运行状态、自启状态、上一轮检测摘要
  refresh       立即触发一轮检测
  reset-switch  [组名…] [--all-profiles]  清除切换记录，重新执行一次「首次自动切换」
  script        输出 geoclash 脚本区块
  discover      打印自动发现的结果与挂接预览，用于排查
  version       显示版本

数据位置：
  配置、状态、日志始终与程序放在同一目录；该目录不可写时使用 %s。

通用参数：
  -c <路径>     配置文件，默认为程序所在目录下的 config.yaml
  -log <路径>   日志文件（run），默认与配置文件同目录的 geoclash.log
`

func main() {
	console.Attach()
	mlog.SetLevel(mlog.SILENT)

	cmd := "run"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	opts, rest, err := parseArgs(args)
	if err != nil {
		fail(err)
	}

	switch cmd {
	case "run":
		err = cmdRun(opts)
	case "install":
		err = cmdInstall(opts)
	case "uninstall":
		err = cmdUninstall(opts)
	case "start":
		err = cmdStart(opts)
	case "stop":
		err = cmdStop(opts)
	case "status":
		err = cmdStatus(opts)
	case "refresh":
		err = cmdRefresh(opts)
	case "reset-switch":
		err = cmdResetSwitch(opts, rest)
	case "script":
		err = cmdScript(opts)
	case "discover":
		err = cmdDiscover(opts)
	case "version":
		fmt.Println("geoclash", version, runtime.GOOS+"/"+runtime.GOARCH)
	case "help", "-h", "--help":
		printUsage()
	default:
		printUsage()
		err = fmt.Errorf("未知命令 %q", cmd)
	}
	if err != nil {
		fail(err)
	}
}

type options struct {
	config         string
	log            string
	allProfiles    bool
	here           bool // install：程序留在原位置，不复制到默认目录
	wait           bool // install / start：等实测分组接入 Clash 后再返回
	configExplicit bool // 通过 -c 指定了配置文件
	logExplicit    bool // 通过 -log 指定了日志文件
}

// parseArgs 解析参数；允许参数与位置参数混排（如 reset-switch 组名 --all-profiles）。
func parseArgs(args []string) (options, []string, error) {
	var o options
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s 需要一个值", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "-c", "--config":
			o.config, err = next()
		case "-log", "--log":
			o.log, err = next()
			o.logExplicit = true
		case "--all-profiles":
			o.allProfiles = true
		case "--here":
			o.here = true
		case "--wait":
			o.wait = true
		case "-h", "--help":
			printUsage()
			os.Exit(0)
		default:
			if strings.HasPrefix(a, "-") {
				return o, nil, fmt.Errorf("未知参数 %s", a)
			}
			rest = append(rest, a)
		}
		if err != nil {
			return o, nil, err
		}
	}
	if o.config != "" {
		abs, err := filepath.Abs(o.config)
		if err != nil {
			return o, nil, err
		}
		o.config, o.configExplicit = abs, true
	} else {
		p, err := defaultConfig()
		if err != nil {
			return o, nil, err
		}
		o.config = p
	}
	o.setLog()
	return o, rest, nil
}

// setLog 未通过 -log 指定时，日志放在配置文件所在目录。
func (o *options) setLog() {
	if !o.logExplicit {
		o.log = filepath.Join(filepath.Dir(o.config), "geoclash.log")
	}
}

// defaultConfig 返回默认配置路径：程序所在目录有配置时使用它（数据和程序放在一起）；
// 否则若默认安装目录（~/.geoclash）已有配置，使用它，这样从其他位置运行 status、stop 等命令也能找到已安装的实例；
// 都没有时使用程序所在目录。
func defaultConfig() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	own := filepath.Join(dir, "config.yaml")
	if fileExists(own) {
		return own, nil
	}
	if def, err := config.DefaultDir(); err == nil {
		if p := filepath.Join(def, "config.yaml"); fileExists(p) {
			return p, nil
		}
	}
	return own, nil
}

func printUsage() {
	p, _ := config.DefaultDir()
	fmt.Printf(usage, p, p)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "错误：", err)
	os.Exit(1)
}

func statePath(o options) string { return filepath.Join(filepath.Dir(o.config), "state.json") }

// ---------- run ----------

func cmdRun(o options) error {
	cfg, err := config.Load(o.config)
	if err != nil {
		return err
	}
	logger, closeLog, err := openLog(o.log)
	if err != nil {
		return err
	}
	defer closeLog()

	ln, err := server.Listen(cfg.Listen)
	if err != nil {
		if isAddrInUse(err) {
			return fmt.Errorf("%s 已被占用，geoclash 可能已在运行（geoclash status 查看）", cfg.Listen)
		}
		return err
	}
	st, err := store.Open(statePath(o))
	if err != nil {
		return err
	}
	a, err := app.New(cfg, st, logger)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	srv := server.New(a, version, cancel)
	go func() {
		if err := srv.Serve(ctx, ln); err != nil {
			logger.Printf("HTTP 服务异常退出：%v", err)
			cancel()
		}
	}()
	logger.Printf("geoclash %s 已启动：http://%s ，配置 %s", version, cfg.Listen, o.config)
	a.Run(ctx)
	logger.Print("geoclash 已停止")
	return nil
}

func openLog(path string) (*log.Logger, func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	// 超过 5 MB 时轮转一次
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	// 文件在前：即使没有控制台（标准输出写入失败），日志也已写入文件
	return log.New(io.MultiWriter(f, os.Stdout), "", log.LstdFlags), func() { f.Close() }, nil
}

func isAddrInUse(err error) bool {
	var se *os.SyscallError
	if errors.As(err, &se) {
		return errors.Is(se.Err, syscall.EADDRINUSE) || strings.Contains(se.Error(), "Only one usage")
	}
	return strings.Contains(err.Error(), "address already in use")
}

// ---------- 与运行中实例通信 ----------

type api struct{ base string }

func newAPI(o options) (*api, error) {
	cfg, err := config.Read(o.config)
	if err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return nil, err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return &api{base: "http://" + net.JoinHostPort(host, port)}, nil
}

var apiClient = &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 90 * time.Second}

func (c *api) call(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set(server.ControlHeader, "1")
	resp, err := apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if s, ok := out.(*string); ok {
			*s = string(data)
			return nil
		}
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *api) health() (server.Health, bool) {
	var h server.Health
	err := c.call(http.MethodGet, "/api/health", nil, &h)
	return h, err == nil
}

func (c *api) waitUp(d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		if _, ok := c.health(); ok {
			return true
		}
	}
	return false
}

// syncWait 是 --wait 等待实测分组接入 Clash 的最长时间（包含全新安装时的第一轮检测）。
const syncWait = 5 * time.Minute

// reportSync 说明实测分组何时接入 Clash；--wait 时等到接入完成再返回。
func (c *api) reportSync(o options) error {
	// 已有检测结果时，启动后会立即接入，稍等片刻
	var h server.Health
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		if hh, ok := c.health(); ok {
			h = hh
			if h.Synced {
				break
			}
		}
	}
	switch {
	case h.Synced:
		fmt.Printf("已接入 Clash：%d 个实测分组，正在后台重新检测\n", h.Groups)
		return nil
	case !o.wait && h.Groups > 0:
		fmt.Println("正在用上次的检测结果接入 Clash；如果没有生效，请用 geoclash status 查看提示")
		return nil
	case !o.wait:
		fmt.Println("首次检测约需 1 分钟，完成后会自动接入 Clash（加 --wait 可等待完成）")
		return nil
	}
	fmt.Println("等待首次检测完成并接入 Clash…")
	for end := time.Now().Add(syncWait); time.Now().Before(end); time.Sleep(2 * time.Second) {
		if hh, ok := c.health(); ok && hh.Synced {
			fmt.Printf("已接入 Clash：%d 个实测分组\n", hh.Groups)
			return nil
		}
	}
	return fmt.Errorf("%s 内没有接入 Clash，请用 geoclash status 查看提示，或查看日志：%s", syncWait, o.log)
}

func (c *api) waitDown(d time.Duration) bool {
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(300 * time.Millisecond) {
		if _, ok := c.health(); !ok {
			return true
		}
	}
	return false
}

// ---------- install / uninstall / start / stop ----------

// selfPath 返回当前可执行文件的真实路径；开机自启直接指向它。
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "geoclash.exe"
	}
	return "geoclash"
}

// migrateLegacy 在目标目录还没有配置时，把旧版本数据目录（用户配置目录下的 geoclash）中的
// 配置、状态与日志迁移过来，并删除旧的自启副本 bin/。保留切换记录，避免各分组被重新切换一次。
func migrateLegacy(o options) {
	legacy, err := config.LegacyDir()
	if err != nil || !fileExists(filepath.Join(legacy, "config.yaml")) {
		return
	}
	dst := filepath.Dir(o.config)
	if samePath(legacy, dst) || fileExists(o.config) {
		return
	}
	moved := 0
	for _, name := range []string{"config.yaml", "state.json", "geoclash.log"} {
		src := filepath.Join(legacy, name)
		if !fileExists(src) {
			continue
		}
		if err := copyFile(src, filepath.Join(dst, name)); err != nil {
			fmt.Println("迁移", name, "失败：", err)
			return
		}
		moved++
	}
	for _, name := range []string{"config.yaml", "state.json", "state.json.tmp", "geoclash.log", "geoclash.log.1"} {
		_ = os.Remove(filepath.Join(legacy, name))
	}
	_ = os.RemoveAll(filepath.Join(legacy, "bin"))
	_ = os.Remove(legacy) // 目录为空时才会成功
	fmt.Println("已把旧位置", legacy, "的", moved, "个文件迁移到", dst)
}

// stopAny 停止运行中的实例：依次用目标配置、程序所在目录、默认目录、旧版本目录中的配置寻找。
func stopAny(o options) error {
	candidates := []string{o.config}
	if d, err := config.Dir(); err == nil {
		candidates = append(candidates, filepath.Join(d, "config.yaml"))
	}
	if d, err := config.DefaultDir(); err == nil {
		candidates = append(candidates, filepath.Join(d, "config.yaml"))
	}
	if d, err := config.LegacyDir(); err == nil {
		candidates = append(candidates, filepath.Join(d, "config.yaml"))
	}
	seen := map[string]bool{}
	for _, p := range candidates {
		c, err := newAPI(options{config: p})
		if err != nil || seen[c.base] {
			continue
		}
		seen[c.base] = true
		if _, running := c.health(); running {
			fmt.Println("正在停止运行中的实例…")
			_ = c.call(http.MethodPost, "/api/stop", nil, nil)
			if !c.waitDown(15 * time.Second) {
				return errors.New("无法停止运行中的实例")
			}
		}
	}
	return nil
}

// cmdInstall 默认把程序复制到 ~/.geoclash 并从那里开机自启；--here 时程序留在原位置。数据始终与程序放在一起。
func cmdInstall(o options) error {
	self, err := selfPath()
	if err != nil {
		return err
	}
	var target string
	if o.here {
		target = filepath.Dir(self)
		if d, err := config.Dir(); err != nil || !samePath(d, target) {
			return fmt.Errorf("%s 不可写，无法使用 --here", target)
		}
	} else if target, err = config.DefaultDir(); err != nil {
		return err
	}
	exe := filepath.Join(target, exeName())
	if o.here {
		exe = self
	}
	if !o.configExplicit {
		o.config = filepath.Join(target, "config.yaml")
		o.setLog()
	}

	if err := stopAny(o); err != nil {
		return err
	}
	if !samePath(self, exe) {
		if err := copyFileRetry(self, exe); err != nil {
			return fmt.Errorf("复制程序到 %s 失败：%w", exe, err)
		}
		fmt.Println("已复制程序：", exe)
	}
	migrateLegacy(o)
	// 旧版本的自启副本已不再使用
	if legacy, err := config.LegacyDir(); err == nil && !samePath(filepath.Dir(exe), filepath.Join(legacy, "bin")) {
		_ = os.RemoveAll(filepath.Join(legacy, "bin"))
	}
	if _, err := config.Load(o.config); err != nil {
		return err
	}
	c, err := newAPI(o)
	if err != nil {
		return err
	}
	spec := autostart.Spec{Exe: exe, Config: o.config, Log: o.log}
	managed, err := autostart.Install(spec)
	if err != nil {
		return fmt.Errorf("安装开机自启失败：%w", err)
	}
	if !managed {
		if err := autostart.Spawn(spec); err != nil {
			return fmt.Errorf("后台启动失败：%w", err)
		}
	}
	_, detail := autostart.Status()
	fmt.Println("数据目录：", filepath.Dir(o.config))
	fmt.Println("已安装开机自启：", detail)
	if !c.waitUp(15 * time.Second) {
		fmt.Println("后台进程尚未就绪，请查看日志：", o.log)
		return nil
	}
	fmt.Println("已在后台启动：", c.base)
	return c.reportSync(o)
}

// copyFileRetry 复制程序；刚停止的进程可能还没释放文件，失败时重试几次。
func copyFileRetry(src, dst string) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = copyFile(src, dst); err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return err
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func cmdUninstall(o options) error {
	if err := autostart.Uninstall(); err != nil {
		return fmt.Errorf("移除开机自启失败：%w", err)
	}
	fmt.Println("已移除开机自启")
	if c, err := newAPI(o); err == nil {
		if _, running := c.health(); running {
			_ = c.call(http.MethodPost, "/api/stop", nil, nil)
			if c.waitDown(15 * time.Second) {
				fmt.Println("已停止后台进程")
			}
		}
	}
	cfg, _ := config.Read(o.config)
	env, _ := discover.Discover()
	if env != nil && env.Kind == discover.KindVerge && env.GlobalScript != "" {
		removed, err := verge.RemoveFromScript(env.GlobalScript)
		switch {
		case err != nil:
			fmt.Println("移除全局扩展脚本中的 geoclash 区块失败：", err)
		case removed:
			fmt.Println("已移除全局扩展脚本中的 geoclash 区块：", env.GlobalScript)
			if cfg != nil {
				cleanRuntime(cfg, env)
			}
		}
	}
	fmt.Println("配置与状态文件已保留：", filepath.Dir(o.config))
	return nil
}

// cleanRuntime 用一个不含分组的区块热重载内核，立即移除正在运行的实测分组。
func cleanRuntime(cfg *config.Config, env *discover.Env) {
	c := cfg.Controller
	if c.URL == "" {
		c = config.Controller{URL: env.Controller.URL, Secret: env.Controller.Secret}
	}
	ctl, err := controller.New(c.URL, c.Secret)
	if err != nil {
		return
	}
	block, err := verge.Block(verge.Data{Selector: cfg.SelectorName, Attach: verge.Attach{Mode: config.AttachNone}})
	if err != nil {
		return
	}
	raw, err := os.ReadFile(env.RuntimeConfig)
	if err != nil {
		return
	}
	payload, err := verge.ApplyRuntime(raw, block)
	if err != nil {
		fmt.Println("清理运行中的实测分组失败：", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := ctl.Reload(ctx, payload); err != nil {
		fmt.Println("清理运行中的实测分组失败：", err, "（Clash Verge Rev 下次生成配置时会自动移除）")
		return
	}
	fmt.Println("已从运行中的内核移除实测分组")
}

func cmdStart(o options) error {
	c, err := newAPI(o)
	if err != nil {
		return err
	}
	if h, running := c.health(); running {
		fmt.Printf("已在运行（PID %d）：%s\n", h.PID, c.base)
		return nil
	}
	// 数据与程序放在一起：优先启动配置所在目录中的程序
	exe := filepath.Join(filepath.Dir(o.config), exeName())
	if !fileExists(exe) {
		if exe, err = selfPath(); err != nil {
			return err
		}
	}
	if err := autostart.Spawn(autostart.Spec{Exe: exe, Config: o.config, Log: o.log}); err != nil {
		return err
	}
	if !c.waitUp(15 * time.Second) {
		return fmt.Errorf("启动超时，请查看日志：%s", o.log)
	}
	fmt.Println("已在后台启动：", c.base)
	return c.reportSync(o)
}

func cmdStop(o options) error {
	c, err := newAPI(o)
	if err != nil {
		return err
	}
	if _, running := c.health(); !running {
		fmt.Println("没有运行中的实例")
		return nil
	}
	if err := c.call(http.MethodPost, "/api/stop", nil, nil); err != nil {
		return err
	}
	if !c.waitDown(15 * time.Second) {
		return errors.New("停止超时")
	}
	fmt.Println("已停止")
	return nil
}

// ---------- status / refresh / reset-switch / script / discover ----------

func cmdStatus(o options) error {
	installed, detail := autostart.Status()
	if installed {
		fmt.Println("开机自启：已安装 —", detail)
	} else {
		fmt.Println("开机自启：未安装")
	}
	c, err := newAPI(o)
	if err != nil {
		return err
	}
	h, running := c.health()
	if !running {
		fmt.Println("运行状态：未运行")
		return nil
	}
	state := "空闲"
	if h.Running {
		state = "正在检测"
	}
	fmt.Printf("运行状态：运行中（PID %d，版本 %s，%s）— %s\n", h.PID, h.Version, state, c.base)
	var v server.StatusView
	if err := c.call(http.MethodGet, "/status", nil, &v); err != nil {
		return err
	}
	if b, err := json.Marshal(v.Round); err == nil {
		var r store.Round
		if json.Unmarshal(b, &r) == nil && !r.Start.IsZero() {
			fmt.Printf("上一轮检测：%s，节点 %d，成功 %d，失败 %d，排除 %d\n",
				r.Start.Local().Format("2006-01-02 15:04:05"), r.Total, r.OK, r.Failed, r.Excluded)
		}
	}
	if v.ClashErr != "" {
		fmt.Println("Clash 环境：", v.ClashErr)
	}
	fmt.Println("实测分组：")
	if len(v.Groups) == 0 {
		fmt.Println("  （暂无）")
	}
	for _, g := range v.Groups {
		fmt.Printf("  %-20s %-9s %d 个节点\n", g.Name, g.Type, len(g.Members))
	}
	for _, n := range v.Notices {
		fmt.Println("提示：", n)
	}
	return nil
}

func cmdRefresh(o options) error {
	c, err := newAPI(o)
	if err != nil {
		return err
	}
	if _, running := c.health(); !running {
		return errors.New("geoclash 没有在运行（geoclash start 启动）")
	}
	if err := c.call(http.MethodPost, "/api/refresh", nil, nil); err != nil {
		return err
	}
	fmt.Println("已触发检测，稍后用 geoclash status 查看结果")
	return nil
}

func cmdResetSwitch(o options, groups []string) error {
	c, err := newAPI(o)
	if err != nil {
		return err
	}
	if _, running := c.health(); running {
		var resp struct {
			Error   string             `json:"error"`
			Results []app.SwitchResult `json:"results"`
		}
		if err := c.call(http.MethodPost, "/api/reset-switch", server.ResetSwitchRequest{Groups: groups, AllProfiles: o.allProfiles}, &resp); err != nil {
			return err
		}
		if len(resp.Results) == 0 && resp.Error == "" {
			fmt.Println("记录已清除；当前没有需要处理的已挂接分组")
		}
		for _, r := range resp.Results {
			fmt.Printf("  %-20s %s\n", r.Group, r.Text)
		}
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		return nil
	}
	// 没有运行：直接修改状态文件，下次启动时生效
	st, err := store.Open(statePath(o))
	if err != nil {
		return err
	}
	profile := "default"
	if env, _ := discover.Discover(); env != nil && env.ProfileName != "" {
		profile = env.ProfileName
	}
	cleared := st.ResetSwitch(profile, groups, o.allProfiles)
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("geoclash 没有在运行，已清除 %d 条记录，下次启动时生效\n", len(cleared))
	return nil
}

func cmdScript(o options) error {
	c, err := newAPI(o)
	if err != nil {
		return err
	}
	if _, running := c.health(); running {
		var s string
		if err := c.call(http.MethodGet, "/verge-script.js", nil, &s); err != nil {
			return err
		}
		fmt.Print(s)
		return nil
	}
	a, err := offlineApp(o)
	if err != nil {
		return err
	}
	fmt.Println(a.Snapshot().Block)
	return nil
}

func offlineApp(o options) (*app.App, error) {
	cfg, err := config.Read(o.config)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(statePath(o))
	if err != nil {
		return nil, err
	}
	return app.New(cfg, st, log.New(io.Discard, "", 0))
}

func cmdDiscover(o options) error {
	fmt.Println("配置文件：", o.config)
	env, err := discover.Discover()
	if env == nil {
		fmt.Println("Clash 环境：未找到 —", err)
		fmt.Println("候选目录：")
		for _, d := range discover.VergeDirs() {
			fmt.Println("  ", d)
		}
		for _, f := range discover.MihomoConfigs() {
			fmt.Println("  ", f)
		}
		return nil
	}
	fmt.Println("Clash 环境：", env.Kind)
	fmt.Println("  数据目录：  ", env.Home)
	fmt.Println("  当前订阅：  ", env.ProfileName, "—", env.ProfileFile)
	fmt.Println("  运行时配置：", env.RuntimeConfig)
	if env.Kernel.Home != "" {
		fmt.Println("  内核目录：  ", env.Kernel.Home, "（provider 缓存位于其下的 geoclash/）")
	}
	if env.GlobalScript != "" {
		fmt.Println("  全局脚本：  ", env.GlobalScript)
	}
	if err != nil {
		fmt.Println("  问题：      ", err)
	}
	ctlURL := env.Controller.URL
	if ctlURL == "" {
		ctlURL = "（未开启）"
	}
	fmt.Println("  controller：", ctlURL)
	if env.Controller.URL != "" {
		if cl, err := controller.New(env.Controller.URL, env.Controller.Secret); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if v, err := cl.Version(ctx); err == nil {
				fmt.Println("              可用，内核版本", v)
			} else {
				fmt.Println("              不可用：", err)
			}
			cancel()
		}
	}

	a, err := offlineApp(o)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(env.RuntimeConfig)
	if err != nil {
		fmt.Println("无法读取运行时配置，跳过挂接预览：", err)
		return nil
	}
	d := a.BlockData()
	results := map[string][]verge.AttachPreview{}
	for _, mode := range []string{config.AttachAuto, config.AttachAll} {
		d.Attach.Mode = mode
		p, err := verge.Preview(raw, d)
		if err != nil {
			return fmt.Errorf("挂接预览失败：%w", err)
		}
		results[mode] = p
	}
	cur := a.Config().AttachTo
	if cur.Mode == config.AttachList {
		d.Attach.Mode, d.Attach.Groups = config.AttachList, cur.Groups
		if p, err := verge.Preview(raw, d); err == nil {
			results[config.AttachList] = p
		}
	}
	fmt.Printf("\n挂接预览（当前配置 attach-to: %s）：\n", attachLabel(cur))
	fmt.Printf("  %-22s %-12s %-14s %-14s", "分组", "类型", "auto", "all")
	if _, ok := results[config.AttachList]; ok {
		fmt.Printf(" %-14s", "列表")
	}
	fmt.Println()
	describe := func(p verge.AttachPreview) string {
		switch {
		case !p.Attached:
			return "—"
		case p.First:
			return "挂接·第一位"
		default:
			return "挂接·末尾"
		}
	}
	for i, p := range results[config.AttachAuto] {
		fmt.Printf("  %-22s %-12s %-14s %-14s", p.Name, p.Type, describe(p), describe(results[config.AttachAll][i]))
		if l, ok := results[config.AttachList]; ok {
			fmt.Printf(" %-14s", describe(l[i]))
		}
		fmt.Println()
	}
	return nil
}

func attachLabel(a config.AttachTo) string {
	switch a.Mode {
	case config.AttachList:
		return "[" + strings.Join(a.Groups, ", ") + "]"
	case config.AttachNone:
		return "[]"
	}
	return a.Mode
}

// ---------- 工具函数 ----------

func samePath(a, b string) bool {
	ea, err1 := filepath.EvalSymlinks(a)
	eb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ea, eb)
	}
	return ea == eb
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
