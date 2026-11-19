package gerrit

import "testing"

func TestExcludeProjects(t *testing.T) {
	tests := []struct {
		hidden, visible []string
		want            string
	}{
		{nil, []string{"a"}, "is:open"},
		{[]string{"accounts/*"}, []string{"accounts/pkg", "accounts/billing"},
			`(is:open) -(project:"^accounts/.*" -project:"accounts/pkg" -project:"accounts/billing")`},
		{[]string{"accounts/*", "legacy", "a.b/v?"}, nil,
			`(is:open) -((project:"^accounts/.*" OR project:"legacy" OR project:"^a\.b/v."))`},
	}
	for _, tt := range tests {
		if got := ExcludeProjects("is:open", tt.hidden, tt.visible); got != tt.want {
			t.Errorf("ExcludeProjects(%q, %q):\n got %s\nwant %s", tt.hidden, tt.visible, got, tt.want)
		}
	}
}
