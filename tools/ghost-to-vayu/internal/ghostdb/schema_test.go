package ghostdb

import (
	"testing"
	"time"
)

func TestEnsureMySQLParseTime(t *testing.T) {
	cases := []struct{ in, want string }{
		{"u:p@tcp(h:3306)/db", "u:p@tcp(h:3306)/db?parseTime=true"},
		{"u:p@tcp(h:3306)/db?charset=utf8mb4", "u:p@tcp(h:3306)/db?charset=utf8mb4&parseTime=true"},
		{"u:p@tcp(h:3306)/db?parseTime=true", "u:p@tcp(h:3306)/db?parseTime=true"},
		{"u:p@tcp(h:3306)/db?parseTime=false", "u:p@tcp(h:3306)/db?parseTime=false"},
	}
	for _, c := range cases {
		if got := ensureMySQLParseTime(c.in); got != c.want {
			t.Errorf("ensureMySQLParseTime(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToTime(t *testing.T) {
	ref := time.Date(2024, 1, 2, 10, 30, 0, 0, time.UTC)

	if got, ok := toTime(ref); !ok || !got.Equal(ref) {
		t.Errorf("time.Time passthrough failed: %v %v", got, ok)
	}
	if got, ok := toTime("2024-01-02 10:30:00"); !ok || got.Year() != 2024 {
		t.Errorf("string parse failed: %v %v", got, ok)
	}
	if got, ok := toTime([]byte("2024-01-02 10:30:00")); !ok || got.Year() != 2024 {
		t.Errorf("[]byte parse failed: %v %v", got, ok)
	}
	if _, ok := toTime(nil); ok {
		t.Error("nil should be invalid")
	}
	if _, ok := toTime("0000-00-00 00:00:00"); ok {
		t.Error("zero MySQL datetime should be invalid")
	}
	if _, ok := toTime(time.Time{}); ok {
		t.Error("zero time.Time should be invalid")
	}
}
