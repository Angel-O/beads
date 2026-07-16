package creds

import (
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

// CanonicalHost normalizes a host into the single byte-exact form used everywhere a
// dial destination is compared or reported: the value injected into a credential
// helper's environment, the credential cache key, and (structurally) the host bd
// dials. Matching on the result is only ever byte-exact equality — never a
// suffix, substring, CIDR-string, or port-insensitive comparison — so a
// lookalike such as "127.0.0.1.evil.example" can never be mistaken for loopback.
//
// The algorithm is:
//  1. Reject empty input or input containing whitespace.
//  2. Strip a single surrounding pair of brackets. If the result parses as an IP,
//     return the Go-normalized form (a v4-mapped IPv6 collapses to a dotted quad,
//     an IPv6 address is lowercased and compressed).
//  3. Otherwise treat it as a hostname: strip exactly one trailing dot and convert
//     to ASCII via IDNA (the Lookup profile, which case-folds), rejecting any
//     input IDNA cannot encode.
func CanonicalHost(host string) (string, error) {
	if host == "" {
		return "", fmt.Errorf("canonical host: empty host")
	}
	if strings.ContainsAny(host, " \t\r\n\v\f") {
		return "", fmt.Errorf("canonical host: %q contains whitespace", host)
	}

	h := host
	if len(h) >= 2 && h[0] == '[' && h[len(h)-1] == ']' {
		h = h[1 : len(h)-1]
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.String(), nil
	}

	h = strings.TrimSuffix(h, ".")
	ascii, err := idna.Lookup.ToASCII(h)
	if err != nil {
		return "", fmt.Errorf("canonical host: %q is not a valid hostname: %w", host, err)
	}
	return ascii, nil
}
