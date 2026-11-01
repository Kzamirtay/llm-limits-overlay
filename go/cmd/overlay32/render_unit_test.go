//go:build windows

package main

import "testing"

func TestFormatPercent(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.0, "0%"},
		{10.0, "10%"},
		{42.7, "42,7%"},
		{100.0, "100%"},
		{0.5, "0,5%"},
		{42.0, "42%"},
	}

	for _, c := range cases {
		got := formatPercent(c.in)
		if got != c.want {
			t.Errorf("formatPercent(%v) = %q; want %q", c.in, got, c.want)
		}
	}
}
