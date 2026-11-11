package version

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0-p2", "0.3.0", -1},
		{"v0.3.0", "0.3.0", 0},
		{"0.3.0", "0.3.0-rc1", 1},
		{"0.3.1", "0.3.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"0.2.0-p2", "0.2.0-p3", -1},
		{"0.3.0", "0.3.0-dev", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
	if !SameMajor("0.2.0-p2", "0.3.0") || SameMajor("0.3.0", "1.0.0") || SameMajor("x", "0.1.0") {
		t.Fatal("SameMajor")
	}
}
