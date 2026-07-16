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
		// Trailing IDNA-mapped separators canonicalize to a dotless host so canon is
		// idempotent and byte-identical to the gasworks/eia-helper side.
		{in: "gw.example.com。", want: "gw.example.com"}, // U+3002 ideographic full stop
		{in: "gw.example.com．", want: "gw.example.com"}, // U+FF0E fullwidth full stop
		{in: "gw.example.com｡", want: "gw.example.com"}, // U+FF61 halfwidth ideographic full stop
		{in: "gw.example.com..", want: "gw.example.com"},
		{in: "", wantErr: true},
		{in: "exa mple.com", wantErr: true},
		// Inputs that collapse to nothing must error, never return the "" host.
		{in: "[]", wantErr: true},
		{in: ".", wantErr: true},
		{in: "[.]", wantErr: true},
		{in: "。", wantErr: true},
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

// TestCanonicalHostIdempotent guards report==dial: canonicalizing the canonical form
// again must be a no-op, so the value fed to exec-info, the DSN, and the cache key can
// never drift on a re-pass.
func TestCanonicalHostIdempotent(t *testing.T) {
	for _, in := range []string{"GW.Example.COM.", "2001:DB8::1", "::FFFF:127.0.0.1", "beads", "gw.example.com。", "gw.example.com..", "gw.example.com."} {
		once, err := CanonicalHost(in)
		if err != nil {
			t.Fatalf("CanonicalHost(%q): %v", in, err)
		}
		twice, err := CanonicalHost(once)
		if err != nil {
			t.Fatalf("CanonicalHost(%q): %v", once, err)
		}
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}
