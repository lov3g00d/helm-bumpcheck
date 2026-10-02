package report

import "testing"

func TestCleanEntry(t *testing.T) {
	tests := []struct {
		in, repo, want string
	}{
		{"[BUGFIX] Improved handling of complete blocks [#3805](https://github.com/grafana/tempo/pull/3805) (@mdisibio)", "grafana/tempo", "[BUGFIX] Improved handling of complete blocks grafana/tempo#3805 (@mdisibio)"},
		{"[velero] Add support for multiple locations by @bsteinm2 in https://github.com/vmware-tanzu/helm-charts/pull/413", "vmware-tanzu/helm-charts", "[velero] Add support for multiple locations"},
		{"Fix crash, see https://github.com/acme/app/issues/12 and https://example.com/docs", "grafana/tempo", "Fix crash, see acme/app#12 and"},
		{"Removed metrics-generator localblocks processor by @javiermolinar in #6555", "grafana/tempo", "Removed metrics-generator localblocks processor"},
		{"Bump chart by @renovate[bot] in https://github.com/x/y/pull/1", "x/y", "Bump chart"},
		{"Plain   text\twith   spaces", "grafana/tempo", "Plain text with spaces"},
		{"Fix crash when ingester restarts (#1234)", "grafana/tempo", "Fix crash when ingester restarts (grafana/tempo#1234)"},
		{"fixes #77, GH-55 and gh-56", "grafana/tempo", "fixes grafana/tempo#77, grafana/tempo#55 and grafana/tempo#56"},
		{"see grafana/loki#9 and issue#5, C# 12 and #v2", "grafana/tempo", "see grafana/loki#9 and issue#5, C# 12 and #v2"},
		{"Don&#39;t drop &#64;user", "grafana/tempo", "Don&#39;t drop &#64;user"},
		{"Fix crash (#1234) for GH-6 and #7", "", "Fix crash for and"},
	}
	for _, tt := range tests {
		if got := cleanEntry(tt.in, tt.repo); got != tt.want {
			t.Errorf("cleanEntry(%q, %q)\n got  %q\n want %q", tt.in, tt.repo, got, tt.want)
		}
	}
}
