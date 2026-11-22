// Package server 提供本地 HTTP 服务：provider、状态页、控制接口与脚本区块。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"geoclash/internal/app"
	"geoclash/internal/geo"
)

// ControlHeader 是控制接口要求的请求头。浏览器跨站请求无法在不经预检的情况下携带自定义请求头，
// 因此网页无法伪造 POST 请求来停止或操控 geoclash。
const ControlHeader = "X-Geoclash"

// Server 是 geoclash 的 HTTP 服务。
type Server struct {
	app     *app.App
	version string
	stop    func()
}

// New 创建服务；stop 在收到 /api/stop 请求时被调用。
func New(a *app.App, version string, stop func()) *Server {
	return &Server{app: a, version: version, stop: stop}
}

// Handler 返回路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("GET /provider/{id}", s.provider)
	mux.HandleFunc("GET /verge-script.js", s.script)
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("POST /api/refresh", s.control(s.refresh))
	mux.HandleFunc("POST /api/reset-switch", s.control(s.resetSwitch))
	mux.HandleFunc("POST /api/stop", s.control(s.stopHandler))
	return localOnly(mux)
}

// Listen 监听地址；端口被占用通常说明已有实例在运行。
func Listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// Serve 在 ln 上提供服务，直到 ctx 结束。
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// localOnly 拒绝 Host 不是本机地址的请求，防止 DNS rebinding 读取包含节点凭据的 provider。
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" {
			if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) control(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(ControlHeader) == "" {
			http.Error(w, "missing "+ControlHeader+" header", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (s *Server) provider(w http.ResponseWriter, r *http.Request) {
	snap := s.app.Snapshot()
	id := r.PathValue("id")
	if snap.Plan.Find(id) == nil {
		http.Error(w, "unknown group", http.StatusNotFound)
		return
	}
	body, ok := snap.Providers[id]
	if !ok {
		// 没有可用节点：返回 503，内核会继续使用上次缓存的 provider 文件
		http.Error(w, "no available node", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	_, _ = w.Write(body)
}

func (s *Server) script(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(s.app.Snapshot().Block + "\n"))
}

// Health 是 /api/health 的响应。
type Health struct {
	PID     int    `json:"pid"`
	Version string `json:"version"`
	Running bool   `json:"running"` // 正在检测
	Synced  bool   `json:"synced"`  // 实测分组已加载到 Clash 内核
	Groups  int    `json:"groups"`  // 当前实测分组数（来自本次或上次的检测结果）
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Health{
		PID: os.Getpid(), Version: s.version, Running: s.app.Running(),
		Synced: s.app.Synced(), Groups: len(s.app.Snapshot().Plan.Groups),
	})
}

func (s *Server) refresh(w http.ResponseWriter, _ *http.Request) {
	s.app.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "已触发检测"})
}

// ResetSwitchRequest 是 /api/reset-switch 的请求体。
type ResetSwitchRequest struct {
	Groups      []string `json:"groups"`
	AllProfiles bool     `json:"allProfiles"`
}

func (s *Server) resetSwitch(w http.ResponseWriter, r *http.Request) {
	var req ResetSwitchRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	res, err := s.app.ResetSwitch(r.Context(), req.Groups, req.AllProfiles)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "results": res})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

func (s *Server) stopHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"message": "正在停止"})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.stop()
	}()
}

// NodeView 是状态页中的一行。
type NodeView struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName,omitempty"`
	Source      string   `json:"source"`
	Country     string   `json:"country,omitempty"`
	CountryName string   `json:"countryName,omitempty"`
	City        string   `json:"city,omitempty"`
	IP          string   `json:"ip,omitempty"`
	Confident   bool     `json:"confident"`
	Effective   bool     `json:"effective"`
	FailCount   int      `json:"failCount"`
	Error       string   `json:"error,omitempty"`
	Answers     []string `json:"answers,omitempty"`
	CheckedAt   string   `json:"checkedAt,omitempty"`
}

// GroupView 是状态页中的一个分组。
type GroupView struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Members []string `json:"members"`
}

// StatusView 是 /status 的响应。
type StatusView struct {
	Version   string      `json:"version"`
	Running   bool        `json:"running"`
	Round     any         `json:"round"`
	Clash     any         `json:"clash,omitempty"`
	ClashErr  string      `json:"clashError,omitempty"`
	Notices   []string    `json:"notices,omitempty"`
	Groups    []GroupView `json:"groups"`
	Nodes     []NodeView  `json:"nodes"`
	BlockHash string      `json:"blockHash"`
}

func (s *Server) view() StatusView {
	snap := s.app.Snapshot()
	stale := s.app.Config().Probe.StaleRounds
	v := StatusView{
		Version:   s.version,
		Running:   s.app.Running(),
		Round:     snap.Round,
		ClashErr:  snap.EnvErr,
		Notices:   snap.Notices,
		BlockHash: snap.BlockHash,
	}
	if snap.Env != nil {
		v.Clash = map[string]string{
			"kind":       snap.Env.Kind,
			"profile":    snap.Env.ProfileName,
			"controller": snap.Env.Controller.URL,
			"script":     snap.Env.GlobalScript,
		}
	}
	for _, g := range snap.Plan.Groups {
		gv := GroupView{ID: g.ID, Name: g.Name, Type: g.Type, Members: []string{}}
		for _, m := range g.Members {
			gv.Members = append(gv.Members, m.DisplayName)
		}
		v.Groups = append(v.Groups, gv)
	}
	for _, n := range snap.Nodes {
		nv := NodeView{
			Name:        n.Name,
			DisplayName: snap.Plan.Names[n.Key],
			Source:      n.Source,
			Country:     n.Country,
			City:        n.City,
			IP:          n.IP,
			Confident:   n.Confident,
			Effective:   n.Effective(stale),
			FailCount:   n.FailCount,
			Error:       n.LastError,
		}
		if n.Country != "" {
			nv.CountryName = geo.Flag(n.Country) + " " + geo.Name(n.Country)
		}
		if !n.CheckedAt.IsZero() {
			nv.CheckedAt = n.CheckedAt.Local().Format("01-02 15:04")
		}
		for _, a := range n.Answers {
			if a.Err != "" {
				nv.Answers = append(nv.Answers, a.Provider+": ✗ "+a.Err)
			} else {
				nv.Answers = append(nv.Answers, strings.TrimSpace(a.Provider+": "+a.Country+" "+a.City))
			}
		}
		v.Nodes = append(v.Nodes, nv)
	}
	// 有效节点在前，按国家、名称排序
	sort.SliceStable(v.Nodes, func(i, j int) bool {
		a, b := v.Nodes[i], v.Nodes[j]
		if a.Effective != b.Effective {
			return a.Effective
		}
		return a.Country < b.Country
	})
	return v
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.view())
}

var page = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>geoclash</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
body{font:14px/1.5 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif;margin:24px;color:#1f2328;background:#fff}
h1{font-size:20px;margin:0 0 4px} h2{font-size:16px;margin:24px 0 8px}
.muted{color:#656d76} .warn{background:#fff8c5;border:1px solid #d4a72c;padding:8px 12px;border-radius:6px;margin:8px 0}
table{border-collapse:collapse;width:100%} th,td{border-bottom:1px solid #d0d7de;padding:6px 8px;text-align:left;vertical-align:top}
th{background:#f6f8fa;font-weight:600} tr.off td{color:#8c959f}
.tag{display:inline-block;padding:0 6px;border-radius:10px;font-size:12px;background:#ddf4ff;color:#0969da}
.low{background:#fff1e5;color:#bc4c00} .ans{font-size:12px;color:#656d76}
button{font:inherit;padding:4px 12px;cursor:pointer}
@media (prefers-color-scheme:dark){body{background:#0d1117;color:#e6edf3}th{background:#161b22}th,td{border-color:#30363d}
.warn{background:#3b2e00;border-color:#9e6a03}.tag{background:#0c2d6b;color:#79c0ff}.low{background:#3d1d00;color:#ffa657}}
</style></head><body>
<h1>geoclash</h1>
<div class="muted">版本 {{.Version}} · {{if .Running}}正在检测…{{else}}空闲{{end}}
 · <button onclick="fetch('/api/refresh',{method:'POST',headers:{'X-Geoclash':'1'}}).then(()=>setTimeout(()=>location.reload(),1000))">立即检测</button></div>
{{with .ClashErr}}<div class="warn">Clash 环境：{{.}}</div>{{end}}
{{range .Notices}}<div class="warn">{{.}}</div>{{end}}
<h2>实测分组</h2>
<table><tr><th>分组</th><th>类型</th><th>节点</th></tr>
{{range .Groups}}<tr><td>{{.Name}}</td><td>{{.Type}}</td><td>{{len .Members}}</td></tr>{{else}}<tr><td colspan="3" class="muted">暂无</td></tr>{{end}}
</table>
<h2>节点</h2>
<table><tr><th>原名</th><th>实测名称</th><th>实测国家</th><th>城市</th><th>出口 IP</th><th>各接口结果</th><th>检测时间</th></tr>
{{range .Nodes}}<tr class="{{if not .Effective}}off{{end}}">
<td>{{.Name}}<div class="ans">{{.Source}}</div></td>
<td>{{.DisplayName}}</td>
<td>{{.CountryName}} {{if and .Country (not .Confident)}}<span class="tag low">低置信</span>{{end}}{{if gt .FailCount 0}}<span class="tag low">失败 {{.FailCount}} 轮</span>{{end}}</td>
<td>{{.City}}</td><td>{{.IP}}</td>
<td class="ans">{{range .Answers}}{{.}}<br>{{end}}{{with .Error}}{{.}}{{end}}</td>
<td>{{.CheckedAt}}</td></tr>{{end}}
</table>
</body></html>`))

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, s.view())
}
