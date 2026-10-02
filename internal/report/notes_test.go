package report

import "testing"

func TestCleanEntry(t *testing.T) {
	tests := map[string]string{
		"[BUGFIX] Improved handling of complete blocks [#3805](https://github.com/grafana/tempo/pull/3805) (@mdisibio)":    "[BUGFIX] Improved handling of complete blocks grafana/tempo#3805 (@mdisibio)",
		"[velero] Add support for multiple locations by @bsteinm2 in https://github.com/vmware-tanzu/helm-charts/pull/413": "[velero] Add support for multiple locations",
		"Fix crash, see https://github.com/acme/app/issues/12 and https://example.com/docs":                                "Fix crash, see acme/app#12 and",
		"Removed metrics-generator localblocks processor by @javiermolinar in #6555":                                       "Removed metrics-generator localblocks processor",
		"Bump chart by @renovate[bot] in https://github.com/x/y/pull/1":                                                    "Bump chart",
		"Plain   text\twith   spaces": "Plain text with spaces",
	}
	for in, want := range tests {
		if got := cleanEntry(in); got != want {
			t.Errorf("cleanEntry(%q)\n got  %q\n want %q", in, got, want)
		}
	}
}
