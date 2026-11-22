// Package discover 自动发现本机的 Clash 环境：当前订阅文件、运行时配置、controller 地址、全局扩展脚本。
package discover

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	KindVerge  = "clash-verge-rev"
	KindMihomo = "mihomo"

	vergeID = "io.github.clash-verge-rev.clash-verge-rev"
)

// Controller 是 mihomo REST API 的地址：http://host:port、pipe:\\.\pipe\name 或 unix:/path。
type Controller struct {
	URL    string
	Secret string
}

// Env 描述发现到的 Clash 环境。
type Env struct {
	Kind          string
	Home          string // Verge 数据目录或 mihomo 配置目录
	ProfileUID    string
	ProfileName   string
	ProfileFile   string // 节点来源文件
	RuntimeConfig string // 内核实际加载的配置
	GlobalScript  string // Verge 全局扩展脚本；非 Verge 环境为空
	Controller    Controller
}

// ErrNotFound 表示本机没有找到受支持的 Clash 环境。
var ErrNotFound = errors.New("未找到 Clash Verge Rev 或 mihomo 的配置")

// Discover 按设计文档 3.2 节的顺序探测，返回第一个找到的环境。
func Discover() (*Env, error) {
	for _, dir := range VergeDirs() {
		if fileExists(filepath.Join(dir, "profiles.yaml")) {
			return loadVerge(dir)
		}
	}
	for _, f := range MihomoConfigs() {
		if fileExists(f) {
			return loadMihomo(f)
		}
	}
	return nil, ErrNotFound
}

// VergeDirs 返回各平台上 Clash Verge Rev 数据目录的候选路径。
func VergeDirs() []string {
	var dirs []string
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if v := os.Getenv("APPDATA"); v != "" {
			dirs = append(dirs, filepath.Join(v, vergeID))
		}
		// 便携版：数据位于程序目录下的 .config
		for _, base := range []string{os.Getenv("ProgramFiles"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs")} {
			if base != "" {
				dirs = append(dirs, filepath.Join(base, "Clash Verge", ".config", vergeID))
			}
		}
	case "darwin":
		dirs = append(dirs, filepath.Join(home, "Library", "Application Support", vergeID))
	default:
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		dirs = append(dirs, filepath.Join(data, vergeID))
	}
	return dirs
}

// MihomoConfigs 返回裸跑 mihomo 配置文件的候选路径。
func MihomoConfigs() []string {
	home, _ := os.UserHomeDir()
	list := []string{filepath.Join(home, ".config", "mihomo", "config.yaml")}
	if runtime.GOOS != "windows" {
		list = append(list, "/etc/mihomo/config.yaml")
	}
	return list
}

type vergeProfiles struct {
	Current string `yaml:"current"`
	Items   []struct {
		UID  string `yaml:"uid"`
		Type string `yaml:"type"`
		Name string `yaml:"name"`
		File string `yaml:"file"`
	} `yaml:"items"`
}

func loadVerge(dir string) (*Env, error) {
	data, err := os.ReadFile(filepath.Join(dir, "profiles.yaml"))
	if err != nil {
		return nil, err
	}
	var p vergeProfiles
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("解析 profiles.yaml 失败：%w", err)
	}
	env := &Env{Kind: KindVerge, Home: dir, RuntimeConfig: filepath.Join(dir, "clash-verge.yaml")}
	for _, it := range p.Items {
		if it.UID == "Script" && it.File != "" {
			env.GlobalScript = filepath.Join(dir, "profiles", it.File)
		}
		if it.UID == p.Current && p.Current != "" {
			env.ProfileUID = it.UID
			env.ProfileName = it.Name
			if it.File != "" {
				env.ProfileFile = filepath.Join(dir, "profiles", it.File)
			}
		}
	}
	if env.ProfileName == "" {
		env.ProfileName = env.ProfileUID
	}
	if env.ProfileFile == "" {
		return env, fmt.Errorf("Clash Verge Rev（%s）没有激活的订阅", dir)
	}
	env.Controller = ControllerFromConfig(env.RuntimeConfig)
	return env, nil
}

func loadMihomo(path string) (*Env, error) {
	env := &Env{
		Kind:          KindMihomo,
		Home:          filepath.Dir(path),
		ProfileName:   "mihomo",
		ProfileFile:   path,
		RuntimeConfig: path,
	}
	env.Controller = ControllerFromConfig(path)
	return env, nil
}

// ControllerFromConfig 从内核配置中读取 controller 地址，优先级：TCP → 命名管道 → unix socket。
// 读取失败或均未开启时返回零值。
func ControllerFromConfig(path string) Controller {
	data, err := os.ReadFile(path)
	if err != nil {
		return Controller{}
	}
	var raw struct {
		TCP    string `yaml:"external-controller"`
		Pipe   string `yaml:"external-controller-pipe"`
		Unix   string `yaml:"external-controller-unix"`
		Secret string `yaml:"secret"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Controller{}
	}
	c := Controller{Secret: raw.Secret}
	switch {
	case strings.TrimSpace(raw.TCP) != "":
		c.URL = "http://" + loopbackAddr(strings.TrimSpace(raw.TCP))
	case raw.Pipe != "" && runtime.GOOS == "windows":
		c.URL = "pipe:" + resolvePipe(raw.Pipe)
	case raw.Unix != "":
		sock := raw.Unix
		if !filepath.IsAbs(sock) {
			sock = filepath.Join(filepath.Dir(path), sock)
		}
		c.URL = "unix:" + resolveSocket(sock)
	}
	return c
}

// loopbackAddr 把 ":9090"、"0.0.0.0:9090" 这类监听地址转换为可连接的本机地址。
func loopbackAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
