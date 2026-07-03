package repl

import "testing"

func TestParseHarmVerdict(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text    string
		harmful bool
		reason  string
		wantErr bool
	}{
		{`{"harmful": false, "reason": "常规列目录"}`, false, "常规列目录", false},
		{`{"harmful": true, "reason": "递归删除目录"}`, true, "递归删除目录", false},
		// Surrounding prose / reasoning leftovers are tolerated.
		{"Here is my verdict:\n{\"harmful\": true, \"reason\": \"rm -rf\"}\n", true, "rm -rf", false},
		{`no json here`, false, "", true},
		{`{not valid json}`, false, "", true},
	}
	for _, c := range cases {
		harmful, reason, err := parseHarmVerdict(c.text)
		if (err != nil) != c.wantErr {
			t.Fatalf("parseHarmVerdict(%q) err = %v, wantErr %v", c.text, err, c.wantErr)
		}
		if c.wantErr {
			continue
		}
		if harmful != c.harmful || reason != c.reason {
			t.Fatalf("parseHarmVerdict(%q) = (%v, %q), want (%v, %q)", c.text, harmful, reason, c.harmful, c.reason)
		}
	}
}
