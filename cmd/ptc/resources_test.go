package main

import "testing"

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		517:      "517 B",
		1023:     "1023 B",
		1024:     "1.0 KiB",
		33554432: "32 MiB",
		33555529: "32 MiB",
	}
	for n, want := range cases {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d)=%q want %q", n, got, want)
		}
	}
}
