//go:build windows

package discover

import "testing"

func TestPickSameSuffix(t *testing.T) {
	got := pickSameSuffix(`\.\pipe\verge-mihomo-sidecar-release-abc`, []string{"verge-mihomo-production-xyz", "verge-mihomo-production-abc"}, `\.\pipe\`)
	if got != `\.\pipe\verge-mihomo-production-abc` {
		t.Errorf("got %q", got)
	}
	if got := pickSameSuffix("x-abc", nil, ""); got != "x-abc" {
		t.Errorf("没有候选时应返回原值：%q", got)
	}
}
