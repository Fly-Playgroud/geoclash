package app

import (
	"context"
	"testing"

	"geoclash/internal/config"

	"geoclash/internal/controller"
)

func TestDecide(t *testing.T) {
	const sel = "🌍 实测国家"
	proxies := map[string]controller.Proxy{
		sel:      {Name: sel, Type: "Selector", Now: "🇺🇸 美国-实测"},
		"美国-实测":  {Type: "URLTest", Now: "x"},
		"节点选择":   {Type: "Selector", Now: "日本06"},
		"日本06":   {Type: "Hysteria2"},
		"流媒体":    {Type: "Selector", Now: "节点选择"},
		"苹果服务":   {Type: "Selector", Now: "DIRECT"},
		"DIRECT": {Type: "Direct"},
		"已跟随":    {Type: "Selector", Now: sel},
		"间接跟随":   {Type: "Selector", Now: "已跟随"},
		"自动":     {Type: "URLTest", Now: "日本06"},
		"经过自动":   {Type: "Selector", Now: "自动"},
		"环A":     {Type: "Selector", Now: "环B"},
		"环B":     {Type: "Selector", Now: "环A"},
	}
	cases := map[string]string{
		"节点选择": OutcomeSwitched,
		"流媒体":  OutcomeSwitched, // 经由「节点选择」最终指向节点
		"苹果服务": OutcomeBuiltin,
		"已跟随":  OutcomeAlready,
		"间接跟随": OutcomeAlready,
		"经过自动": OutcomeSwitched,
		"环A":   OutcomeBuiltin,
	}
	for g, want := range cases {
		if got := decide(proxies, g, sel); got != want {
			t.Errorf("decide(%s) = %s，期望 %s", g, got, want)
		}
	}
}

func TestAutoSwitchSkipsGlobal(t *testing.T) {
	const sel = "🌍 实测国家"
	proxies := map[string]controller.Proxy{
		sel:      {Type: "Selector", Now: "x"},
		"GLOBAL": {Type: "Selector", Now: "日本06", All: []string{"DIRECT", sel, "日本06"}},
		"日本06":   {Type: "Hysteria2"},
	}
	a := &App{cfg: &config.Config{SelectorName: sel}}
	res := a.autoSwitch(context.Background(), nil, proxies, "p", []string{"GLOBAL"})
	if len(res) != 1 || res[0].Outcome != OutcomeNotAttach {
		t.Errorf("GLOBAL 不应被切换：%+v", res)
	}
}
