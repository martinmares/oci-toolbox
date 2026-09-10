package cmd

import "testing"

func TestBundleImageProgressLabelIncludesImageID(t *testing.T) {
	for _, test := range []struct {
		kind           string
		current, total int
		want           string
	}{
		{kind: "bundle", current: 15, total: 17, want: "bundle 15/17 (tsm-catalog)"},
		{kind: "selected image", current: 1, total: 1, want: "selected image 1/1 (tsm-catalog)"},
	} {
		if got := bundleImageProgressLabel(test.kind, test.current, test.total, "tsm-catalog"); got != test.want {
			t.Fatalf("label = %q, want %q", got, test.want)
		}
	}
}
