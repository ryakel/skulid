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
