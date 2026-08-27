package auth

import "testing"

// The empty-expect case is the important one: "+ Connect Google account"
// places no constraint and must still be able to add any account.
func TestSubMismatch(t *testing.T) {
	cases := []struct {
		name   string
		expect string
		got    string
		want   bool
	}{
		{"no expectation — a plain connect", "", "any-sub", false},
		{"no expectation, empty result", "", "", false},
		{"reconnect, right account", "sub-123", "sub-123", false},
		{"reconnect, wrong account", "sub-123", "sub-456", true},
		{"reconnect, empty result is still wrong", "sub-123", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SubMismatch(tc.expect, tc.got); got != tc.want {
				t.Errorf("SubMismatch(%q, %q) = %v, want %v", tc.expect, tc.got, got, tc.want)
			}
		})
	}
}
