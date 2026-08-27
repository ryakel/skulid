package config

import "testing"

func TestIsTruthy(t *testing.T) {
	yes := []string{"1", "true", "TRUE", "True", " yes ", "on", "y", "YES"}
	no := []string{"", "0", "false", "no", "off", "nope", "  "}
	for _, v := range yes {
		if !isTruthy(v) {
			t.Errorf("isTruthy(%q): want true", v)
		}
	}
	for _, v := range no {
		if isTruthy(v) {
			t.Errorf("isTruthy(%q): want false", v)
		}
	}
}

func TestEnvInt(t *testing.T) {
	const key = "SKULID_TEST_ENV_INT"

	cases := []struct {
		name string
		set  bool
		val  string
		def  int
		want int
	}{
		{"unset falls back", false, "", 90, 90},
		{"empty falls back", true, "", 90, 90},
		{"whitespace falls back", true, "   ", 90, 90},
		{"plain value", true, "30", 90, 30},
		{"surrounding space", true, " 45 ", 90, 45},
		// A typo in a tuning knob must not stop the daemon booting.
		{"unparseable falls back", true, "ninety", 90, 90},
		{"negative falls back", true, "-1", 90, 90},
		// Zero is meaningful: it disables the prune.
		{"zero is honoured", true, "0", 90, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.val)
			} else {
				t.Setenv(key, "")
			}
			if got := envInt(key, tc.def); got != tc.want {
				t.Errorf("envInt(%q=%q, %d) = %d, want %d", key, tc.val, tc.def, got, tc.want)
			}
		})
	}
}
