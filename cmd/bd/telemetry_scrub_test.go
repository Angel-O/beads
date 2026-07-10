package main

import (
	"strings"
	"testing"
)

// TestScrubArgsForTelemetry locks the invariant that no password embedded in a
// --pg-url/--mysql-url value survives into the bd.args telemetry span, across every
// DSN password form pgx/go-sql-driver accept and both the --flag=value and
// --flag value spellings. The pre-fix scrubber handled only URL userinfo, so
// query-param and libpq keyword/value passwords (the confirmed red-team vectors)
// leaked verbatim.
func TestScrubArgsForTelemetry(t *testing.T) {
	const secret = "s3cr3t-pw"
	cases := []struct {
		name string
		argv []string
		keep []string // non-secret structure that must survive redaction
	}{
		{
			name: "pg url userinfo =form",
			argv: []string{"init", "--backend=postgres", "--pg-url=postgres://bts:" + secret + "@127.0.0.1:5432/db"},
			keep: []string{"--pg-url=postgres://bts:", "127.0.0.1:5432/db", "--backend=postgres"},
		},
		{
			name: "pg url query param space form",
			argv: []string{"init", "--pg-url", "postgres://bts@127.0.0.1:5432/db?password=" + secret},
			keep: []string{"127.0.0.1:5432/db", "password="},
		},
		{
			name: "pg url sslpassword =form",
			argv: []string{"init", "--pg-url=postgres://bts@h:5432/db?sslpassword=" + secret + "&sslmode=require"},
			keep: []string{"sslmode=require"},
		},
		{
			name: "pg libpq keyword/value =form single token",
			argv: []string{"init", "--pg-url=host=127.0.0.1 user=bts password=" + secret + " dbname=db"},
			keep: []string{"host=127.0.0.1", "user=bts", "dbname=db"},
		},
		{
			name: "mysql userinfo space form",
			argv: []string{"init", "--mysql-url", "bts:" + secret + "@tcp(127.0.0.1:3306)/db"},
			keep: []string{"tcp(127.0.0.1:3306)/db", "bts:"},
		},
		{
			name: "mysql userinfo =form",
			argv: []string{"init", "--mysql-url=bts:" + secret + "@tcp(127.0.0.1:3306)/db"},
			keep: []string{"--mysql-url=bts:", "tcp(127.0.0.1:3306)/db"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := scrubArgsForTelemetry(tc.argv)
			if strings.Contains(out, secret) {
				t.Fatalf("PASSWORD LEAK: scrubArgsForTelemetry(%v) = %q still contains %q", tc.argv, out, secret)
			}
			if !strings.Contains(out, "xxxxx") {
				t.Fatalf("expected redaction marker xxxxx in %q", out)
			}
			for _, k := range tc.keep {
				if !strings.Contains(out, k) {
					t.Errorf("expected %q to survive redaction in %q", k, out)
				}
			}
		})
	}
}

// TestScrubArgsForTelemetryLeavesOrdinaryArgs proves the scrubber does not mangle
// non-DSN arguments: an ordinary token that merely contains "password=" is not a
// credential-flag value and passes through untouched (no over-redaction), while a
// bare user:pass@host userinfo anywhere is still redacted as defense in depth.
func TestScrubArgsForTelemetryLeavesOrdinaryArgs(t *testing.T) {
	argv := []string{"create", "--title", "document the password= knob"}
	if out := scrubArgsForTelemetry(argv); out != "create --title document the password= knob" {
		t.Fatalf("over-redacted ordinary args: got %q", out)
	}

	argv = []string{"weird", "postgres://u:leak@h:5432/db"}
	if out := scrubArgsForTelemetry(argv); strings.Contains(out, "leak") {
		t.Fatalf("userinfo password not scrubbed as defense in depth: %q", out)
	}
}
