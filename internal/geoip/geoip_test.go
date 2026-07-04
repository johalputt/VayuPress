package geoip

import "testing"

func TestCountry(t *testing.T) {
	t.Setenv("ANALYTICS_GEOIP", "") // ensure enabled default
	cases := []struct {
		ip   string
		want string
	}{
		{"1.1.1.1", "AU"},
		{"8.8.8.8", "US"},
		{"2606:4700:4700::1111", "US"},
		{"127.0.0.1", ""},     // loopback
		{"192.168.1.5", ""},   // private
		{"10.0.0.1", ""},      // private
		{"::1", ""},           // loopback v6
		{"0.0.0.0", ""},       // unspecified
		{"not-an-ip", ""},     // unparseable
		{"", ""},              // empty
		{"169.254.10.10", ""}, // link-local
	}
	for _, c := range cases {
		if got := Country(c.ip); got != c.want {
			t.Errorf("Country(%q) = %q, want %q", c.ip, got, c.want)
		}
	}
}

func TestDisabled(t *testing.T) {
	t.Setenv("ANALYTICS_GEOIP", "off")
	if Enabled() {
		t.Fatal("Enabled() = true with ANALYTICS_GEOIP=off")
	}
	if got := Country("8.8.8.8"); got != "" {
		t.Errorf("Country with lookup disabled = %q, want empty", got)
	}
}
