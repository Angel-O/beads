package creds

import "testing"

func TestCanonicalHostGoldenVectors(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "GW.Example.COM.", want: "gw.example.com"},
		{in: "beads", want: "beads"},
		{in: "BÜCHER.example", want: "xn--bcher-kva.example"},
		{in: "127.0.0.1", want: "127.0.0.1"},
		{in: "127.0.0.1.evil.example", want: "127.0.0.1.evil.example"}, // a hostname, not loopback
		{in: "localhost.evil.example", want: "localhost.evil.example"},
		{in: "[::ffff:127.0.0.1]", want: "127.0.0.1"},
		{in: "::FFFF:127.0.0.1", want: "127.0.0.1"},
		{in: "2001:DB8::1", want: "2001:db8::1"},
		{in: "0.0.0.0", want: "0.0.0.0"},
		{in: "localhost", want: "localhost"},
		{in: "evil.example.", want: "evil.example"},
		{in: "", wantErr: true},
		{in: "exa mple.com", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := CanonicalHost(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("CanonicalHost(%q) = %q, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CanonicalHost(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("CanonicalHost(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
