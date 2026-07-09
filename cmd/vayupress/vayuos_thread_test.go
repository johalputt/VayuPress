package main

import "testing"

func TestNormSubject(t *testing.T) {
	cases := map[string]string{
		"Hello":                "hello",
		"Re: Hello":            "hello",
		"RE: re: Hello":        "hello",
		"Fwd: Re: Hello World": "hello world",
		"FW: budget":           "budget",
		"  Re:   spaced  ":     "spaced",
		"":                     "",
		"Re:":                  "",
		"Recreate the plan":    "recreate the plan", // "Re" must need the colon
	}
	for in, want := range cases {
		if got := normSubject(in); got != want {
			t.Errorf("normSubject(%q) = %q, want %q", in, got, want)
		}
	}
}
