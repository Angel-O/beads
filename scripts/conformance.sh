#!/usr/bin/env bash
#
# Single conformance entrypoint. CI runs this verbatim; run it locally the same way:
#
#   ./scripts/conformance.sh
#
# Two tiers, both reading the backend registry (test/conformance/profiles.go for the
# E2E tier; the per-backend conformance_test.go files for the in-process tier). Add a
# backend = add a profile + a factory; both tiers pick it up.
#
# Backends that need an external service auto-skip when their env is unset:
#   BEADS_PG_TEST_URL   postgres://user:pass@host:port/db   (enables the postgres backend)
#   BEADS_PG_PASSWORD   optional, if the password is not in the URL
#
# Optional deep gate (bts-rs 523-scenario differential oracle; needs the bts-rs
# checkout + ~50 min, so it is off by default and not part of the per-PR loop):
#   CONFORMANCE_DEEP=1  BTS_RS_DIR=/path/to/bts-rs  ./scripts/conformance.sh
#
set -euo pipefail
cd "$(dirname "$0")/.."

TAGS="gms_pure_go"

echo "==> Tier 1: in-process store conformance + wedge gates"
# Dolt runs the full backend-agnostic suite (conformance.RunAll).
CGO_ENABLED=1 go test -tags "$TAGS" ./internal/storage/embeddeddolt/ -run TestConformance
# The postgres wedge's behavioral parity is covered differentially in Tier 2 and by
# the deep oracle; its full in-process RunAll still fails the genuinely-Dolt-only methods
# (version-control/remote/sync/slots — audited in completeness_test.go). Tier 1 here runs
# the wedge's green gates: live smoke, the interface-completeness audit (the shell must
# equal the deferral allowlist — no SILENT unsupported) plus its behavioral complement
# (every allowlisted method returns typed ErrUnsupported), the seed-once regression, the
# portable non-VC reads+writes (statistics/external-ref/stale + molecule/repo-mtime/
# streams/counts/comment/rekey/promote/purge/batch), and the dialect corpus-PREPARE +
# password-redaction gates. All self-skip without BEADS_PG_TEST_URL.
CGO_ENABLED=1 go test -tags "$TAGS" ./internal/storage/postgres/ \
  -run 'TestPGSmoke|TestInterfaceCompleteness|TestUnsupportedContract|TestSeedOnlyOnFirstProvision|TestDeferredReads|TestPortableMethods'
CGO_ENABLED=1 go test -tags "$TAGS" ./internal/storage/pgdialect/
# MySQL wedge gates (self-skip without BEADS_MYSQL_TEST_URL); the dialect rewrite test
# (the is_blocked 1093 workaround) always runs.
CGO_ENABLED=1 go test -tags "$TAGS" ./internal/storage/mysql/ \
  -run 'TestInterfaceCompleteness|TestUnsupportedContract|TestSeedOnlyOnFirstProvision|TestDeferredReads|TestPortableMethods'
CGO_ENABLED=1 go test -tags "$TAGS" ./internal/storage/mysqldialect/
# SQLite is embedded (pure-Go), always runs.
CGO_ENABLED=1 go test -tags "$TAGS" ./internal/storage/sqlite/ \
  -run 'TestInterfaceCompleteness|TestUnsupportedContract|TestSeedOnlyOnFirstProvision|TestDeferredReads|TestPortableMethods'
CGO_ENABLED=1 go test -tags "$TAGS" ./internal/storage/sqlitedialect/

echo "==> Tier 2: end-to-end 'bd init' + CLI conformance (differential vs Dolt)"
CGO_ENABLED=1 go test -tags "$TAGS e2e" ./test/conformance/

if [[ "${CONFORMANCE_DEEP:-0}" == "1" ]]; then
  echo "==> Deep: bts-rs 523-scenario differential oracle"
  ./scripts/run-oracle-p.sh
fi

echo "==> conformance OK"
