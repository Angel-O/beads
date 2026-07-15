package backendmigration

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/workspaceidentity"
)

func validProviderConfigurationRequest(workspace string) ProviderConfigurationRequest {
	return ProviderConfigurationRequest{
		Selection: validSelectionRequest(workspace),
		Target: PostgreSQLTargetRequest{
			Locator:       "postgresql://beads@db.example.test/beads?sslmode=verify-full",
			Schema:        "migration_target",
			LocatorOrigin: ValueOriginExplicitFlag,
			SchemaOrigin:  ValueOriginExplicitFlag,
		},
	}
}

func requireBindingRefusal(
	t *testing.T,
	binding *ProviderConfigurationBinding,
	err error,
	code RefusalCode,
	reason RefusalReason,
) *Refusal {
	t.Helper()
	if binding != nil {
		t.Fatalf("binding=%v, want nil", binding)
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("error=%v, want *Refusal", err)
	}
	wantRetryable := code == CodeWorkspaceChanged || code == CodeCredentialsRequired
	if refusal.Code != code || refusal.Reason != reason || refusal.Retryable != wantRetryable ||
		refusal.Effect != effectNone || err.Error() != string(code) {
		t.Fatalf("refusal=%#v error=%q, want %s/%s", refusal, err, code, reason)
	}
	return refusal
}

func requireErrorGraphOmits(t *testing.T, err error, sensitive string) {
	t.Helper()
	var walk func(error, int)
	walk = func(current error, depth int) {
		if current == nil || depth > 32 {
			return
		}
		for _, rendered := range []string{current.Error(), fmt.Sprint(current), fmt.Sprintf("%+v", current), fmt.Sprintf("%#v", current)} {
			if strings.Contains(rendered, sensitive) {
				t.Fatalf("error graph leaked rejected value: %q", rendered)
			}
		}
		if many, ok := current.(interface{ Unwrap() []error }); ok {
			for _, child := range many.Unwrap() {
				walk(child, depth+1)
			}
			return
		}
		walk(errors.Unwrap(current), depth+1)
	}
	walk(err, 0)
}

func TestBindProviderConfigurationUsesWitnessedBytesWithoutPathReload(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "witnessed_database",
	})

	reloaded, err := json.Marshal(configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "pathname_database",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.workspace, configfile.ConfigFileName), reloaded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "ambient_database")

	binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), fixture.dependencies())
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}
	defer func() { _ = binding.Close() }()

	configuration, err := binding.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if configuration.Source().Database() != "witnessed_database" {
		t.Fatal("bound source database was not taken from admitted witnessed bytes")
	}
}

func TestBindProviderConfigurationTargetRefusalsPrecedeSourceAccess(t *testing.T) {
	base := validProviderConfigurationRequest(filepath.Join(string(filepath.Separator), "unused", ".beads"))
	secret := "w3_target_secret_7f45"
	tests := []struct {
		name   string
		mutate func(*ProviderConfigurationRequest)
		code   RefusalCode
		reason RefusalReason
	}{
		{name: "target backend", mutate: func(r *ProviderConfigurationRequest) { r.Selection.TargetBackend = configfile.BackendMySQL }, code: CodePairUnsupported, reason: ReasonTargetBackend},
		{name: "unknown locator origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.LocatorOrigin = ValueOriginUnknown }, code: CodePairUnsupported, reason: ReasonTargetLocatorSource},
		{name: "environment locator origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.LocatorOrigin = ValueOriginEnvironment }, code: CodePairUnsupported, reason: ReasonTargetLocatorSource},
		{name: "workspace locator origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.LocatorOrigin = ValueOriginWorkspace }, code: CodePairUnsupported, reason: ReasonTargetLocatorSource},
		{name: "other locator origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.LocatorOrigin = ValueOriginOther }, code: CodePairUnsupported, reason: ReasonTargetLocatorSource},
		{name: "unknown schema origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.SchemaOrigin = ValueOriginUnknown }, code: CodePairUnsupported, reason: ReasonTargetSchemaSource},
		{name: "environment schema origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.SchemaOrigin = ValueOriginEnvironment }, code: CodePairUnsupported, reason: ReasonTargetSchemaSource},
		{name: "workspace schema origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.SchemaOrigin = ValueOriginWorkspace }, code: CodePairUnsupported, reason: ReasonTargetSchemaSource},
		{name: "other schema origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.SchemaOrigin = ValueOriginOther }, code: CodePairUnsupported, reason: ReasonTargetSchemaSource},
		{name: "invalid locator origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.LocatorOrigin = ValueOrigin(255) }, code: CodePairUnsupported, reason: ReasonTargetLocatorSource},
		{name: "invalid schema origin", mutate: func(r *ProviderConfigurationRequest) { r.Target.SchemaOrigin = ValueOrigin(255) }, code: CodePairUnsupported, reason: ReasonTargetSchemaSource},
		{name: "empty locator", mutate: func(r *ProviderConfigurationRequest) { r.Target.Locator = "" }, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "oversize locator", mutate: func(r *ProviderConfigurationRequest) { r.Target.Locator = strings.Repeat("a", 4097) }, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "raw control", mutate: func(r *ProviderConfigurationRequest) { r.Target.Locator += "\x1b" }, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "invalid utf8", mutate: func(r *ProviderConfigurationRequest) { r.Target.Locator = string([]byte{'p', 0xff}) }, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "wrong scheme", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "mysql://beads@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "uppercase scheme", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "POSTGRESQL://beads@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "opaque url", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql:beads@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "empty fragment", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=require#"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "nonempty fragment", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=require#private"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "keyword dsn", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "host=db.example.test user=beads password=" + secret
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "userinfo password", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads:" + secret + "@db.example.test/beads?sslmode=require"
		}, code: CodeCredentialInLocator, reason: ReasonTargetCredential},
		{name: "empty userinfo password", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads:@db.example.test/beads?sslmode=require"
		}, code: CodeCredentialInLocator, reason: ReasonTargetCredential},
		{name: "encoded password key", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=require&pass%77ord=" + secret
		}, code: CodeCredentialInLocator, reason: ReasonTargetCredential},
		{name: "raw password key", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?password=" + secret + "&sslmode=require"
		}, code: CodeCredentialInLocator, reason: ReasonTargetCredential},
		{name: "empty password option", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?password=&sslmode=require"
		}, code: CodeCredentialInLocator, reason: ReasonTargetCredential},
		{name: "semicolon password option", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=require;sslpassword=" + secret
		}, code: CodeCredentialInLocator, reason: ReasonTargetCredential},
		{name: "case variant ssl password", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?SSLPassword=" + secret + "&sslmode=require"
		}, code: CodeCredentialInLocator, reason: ReasonTargetCredential},
		{name: "missing user", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "encoded nul user", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://user%00name@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "encoded newline user", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://user%0Aname@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "encoded space user", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://user%20name@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "invalid utf8 user", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://user%FF@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "overlong user", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://" + strings.Repeat("u", 64) + "@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "digit initial user", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://9user@db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "missing host", mutate: func(r *ProviderConfigurationRequest) { r.Target.Locator = "postgresql://beads@/beads?sslmode=require" }, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "trailing dot host", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test./beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "invalid numeric host", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@999.1.1.1/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "leading zero ipv4", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@010.0.0.1/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "short ipv4", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@127.1/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "hex ipv4", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@0x7f000001/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "ipv6 zone", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@[fe80::1%25eth0]/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "multiple hosts", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db1.example.test,db2.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "bracketed dns", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@[db.example.test]/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "empty dns label", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db..example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "overlong dns label", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@" + strings.Repeat("a", 64) + ".test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "overlong dns name", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62) + "/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "leading hyphen dns label", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@-db.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "trailing hyphen dns label", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db-.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "underscore dns label", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db_name.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "unicode dns label", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@déb.example.test/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "zero port", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test:0/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "overflow port", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test:65536/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "empty port", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test:/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetTransport},
		{name: "signed port", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test:+5432/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "negative port", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test:-1/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "nondecimal port", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test:port/beads?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "missing database", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "encoded control database", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/db%0Aname?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "encoded slash database", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/db%2Fname?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "invalid utf8 database", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/db%FF?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "encoded space database", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/db%20name?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "overlong database", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/" + strings.Repeat("d", 64) + "?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "second path authority", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test//other?sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetLocator},
		{name: "missing options", mutate: func(r *ProviderConfigurationRequest) { r.Target.Locator = "postgresql://beads@db.example.test/beads" }, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "unknown option", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=require&application_name=bd"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "duplicate option", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=require&sslmode=verify-full"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "uppercase option", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?SSLMODE=require"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "trailing empty pair", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=require&"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "leading empty pair", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?&sslmode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "semicolon option separator", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=require;application_name=bd"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "encoded option key", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?ssl%6dode=require"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "encoded option value", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=requ%69re"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "insecure ssl mode", mutate: func(r *ProviderConfigurationRequest) {
			r.Target.Locator = "postgresql://beads@db.example.test/beads?sslmode=disable"
		}, code: CodePairUnsupported, reason: ReasonTargetOptions},
		{name: "empty schema", mutate: func(r *ProviderConfigurationRequest) { r.Target.Schema = "" }, code: CodePairUnsupported, reason: ReasonTargetSchema},
		{name: "digit initial schema", mutate: func(r *ProviderConfigurationRequest) { r.Target.Schema = "9migration" }, code: CodePairUnsupported, reason: ReasonTargetSchema},
		{name: "hyphenated schema", mutate: func(r *ProviderConfigurationRequest) { r.Target.Schema = "migration-target" }, code: CodePairUnsupported, reason: ReasonTargetSchema},
		{name: "uppercase schema", mutate: func(r *ProviderConfigurationRequest) { r.Target.Schema = "Migration" }, code: CodePairUnsupported, reason: ReasonTargetSchema},
		{name: "public schema", mutate: func(r *ProviderConfigurationRequest) { r.Target.Schema = "public" }, code: CodePairUnsupported, reason: ReasonTargetSchema},
		{name: "information schema", mutate: func(r *ProviderConfigurationRequest) { r.Target.Schema = "information_schema" }, code: CodePairUnsupported, reason: ReasonTargetSchema},
		{name: "pg schema", mutate: func(r *ProviderConfigurationRequest) { r.Target.Schema = "pg_private" }, code: CodePairUnsupported, reason: ReasonTargetSchema},
		{name: "overlong schema", mutate: func(r *ProviderConfigurationRequest) { r.Target.Schema = "s" + strings.Repeat("x", 63) }, code: CodePairUnsupported, reason: ReasonTargetSchema},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.mutate(&request)
			sourceCalls := 0
			deps := selectionDependencies{
				platform: func() (bool, bool, error) { sourceCalls++; return true, false, nil },
				observe:  func(string) (shapeObservation, error) { sourceCalls++; return shapeObservation{}, nil },
				bind: func(string, int64) (sourceWitness, []byte, error) {
					sourceCalls++
					return nil, nil, errors.New("source must not be reached")
				},
			}
			binding, err := bindProviderConfigurationWith(request, deps)
			requireBindingRefusal(t, binding, err, test.code, test.reason)
			if sourceCalls != 0 {
				t.Fatalf("source callbacks=%d, want 0", sourceCalls)
			}
			requireErrorGraphOmits(t, err, secret)
		})
	}
}

func TestBindProviderConfigurationCanonicalizesTargetIdentity(t *testing.T) {
	tests := []struct {
		name    string
		locator string
		want    string
	}{
		{name: "dns case and default port", locator: "postgres://User.Name@DB.Example.TEST/beads?sslmode=require", want: "postgresql://User.Name@db.example.test:5432/beads?sslmode=require"},
		{name: "minimal decimal port", locator: "postgresql://beads@db.example.test:05432/beads?sslmode=verify-full", want: "postgresql://beads@db.example.test:5432/beads?sslmode=verify-full"},
		{name: "ipv4", locator: "postgresql://beads@192.0.2.10/beads?sslmode=require", want: "postgresql://beads@192.0.2.10:5432/beads?sslmode=require"},
		{name: "ipv6 compression", locator: "postgresql://beads@[2001:0db8:0:0:0:0:0:1]/beads?sslmode=require", want: "postgresql://beads@[2001:db8::1]:5432/beads?sslmode=require"},
		{name: "mapped ipv6 becomes ipv4", locator: "postgresql://beads@[::ffff:192.0.2.10]/beads?sslmode=require", want: "postgresql://beads@192.0.2.10:5432/beads?sslmode=require"},
		{name: "canonical escaped identity", locator: "postgresql://%62eads@db.example.test/%62eads%5Fdb?sslmode=require", want: "postgresql://beads@db.example.test:5432/beads_db?sslmode=require"},
		{name: "minimum identifiers", locator: "postgresql://_@db.example.test/_?sslmode=require", want: "postgresql://_@db.example.test:5432/_?sslmode=require"},
		{name: "minimum port", locator: "postgresql://beads@db.example.test:1/beads?sslmode=require", want: "postgresql://beads@db.example.test:1/beads?sslmode=require"},
		{name: "maximum port", locator: "postgresql://beads@db.example.test:65535/beads?sslmode=require", want: "postgresql://beads@db.example.test:65535/beads?sslmode=require"},
		{name: "maximum user and database", locator: "postgresql://" + "u" + strings.Repeat("x", 62) + "@db.example.test/" + "d" + strings.Repeat("y", 62) + "?sslmode=require", want: "postgresql://" + "u" + strings.Repeat("x", 62) + "@db.example.test:5432/" + "d" + strings.Repeat("y", 62) + "?sslmode=require"},
		{name: "maximum dns name", locator: "postgresql://beads@" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + "/beads?sslmode=require", want: "postgresql://beads@" + strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + ":5432/beads?sslmode=require"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
			request := validProviderConfigurationRequest(fixture.workspace)
			request.Target.Locator = test.locator
			binding, err := bindProviderConfigurationWith(request, fixture.dependencies())
			if err != nil {
				t.Fatalf("BindProviderConfiguration: %v", err)
			}
			defer func() { _ = binding.Close() }()
			configuration, err := binding.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if got := configuration.Target().BaseDSN(); got != test.want {
				t.Fatalf("BaseDSN()=%q, want %q", got, test.want)
			}
		})
	}
}

func TestBindProviderConfigurationAcceptsSchemaBoundaries(t *testing.T) {
	for _, schema := range []string{"_", "s" + strings.Repeat("x", 62)} {
		t.Run(schema, func(t *testing.T) {
			fixture := newSelectionFixture(t, configfile.Config{
				Backend:      configfile.BackendDolt,
				DoltMode:     configfile.DoltModeEmbedded,
				DoltDatabase: "s" + strings.Repeat("d", 62),
			})
			request := validProviderConfigurationRequest(fixture.workspace)
			request.Target.Schema = schema
			binding, err := bindProviderConfigurationWith(request, fixture.dependencies())
			if err != nil {
				t.Fatalf("BindProviderConfiguration: %v", err)
			}
			defer func() { _ = binding.Close() }()
			configuration, err := binding.Snapshot()
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if configuration.Target().Schema() != schema || configuration.Source().Database() != "s"+strings.Repeat("d", 62) {
				t.Fatalf("configuration=%#v, want maximum admitted source and schema", configuration)
			}
		})
	}
}

func TestBindProviderConfigurationOwnsWitnessAndDefaultsDatabaseWithoutAmbient(t *testing.T) {
	t.Setenv("BEADS_DOLT_SERVER_DATABASE", "ambient_must_not_win")
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), fixture.dependencies())
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}
	if fixture.witness.closes != 0 {
		t.Fatalf("witness closes after bind=%d, want 0", fixture.witness.closes)
	}
	configuration, err := binding.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if source := configuration.Source(); source.BeadsDir() != fixture.workspace || source.Database() != configfile.DefaultDoltDatabase || source.Branch() != "main" {
		t.Fatalf("source=%#v, want admitted workspace/default database/main", source)
	}
	if err := binding.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := binding.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if fixture.witness.closes != 1 {
		t.Fatalf("witness closes=%d, want 1", fixture.witness.closes)
	}
	closed, err := binding.Snapshot()
	if closed != (BoundProviderConfiguration{}) || !errors.Is(err, workspaceidentity.ErrClosed) {
		t.Fatalf("closed Snapshot=%#v, %v, want zero/ErrClosed", closed, err)
	}
}

func TestBindProviderConfigurationRejectsInvalidWitnessedDatabaseWithoutSanitizing(t *testing.T) {
	for _, database := range []string{"bad-name", "bad.name", "9bad", "bad\nname", strings.Repeat("d", 64)} {
		t.Run(fmt.Sprintf("%q", database), func(t *testing.T) {
			fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded, DoltDatabase: database})
			binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), fixture.dependencies())
			requireBindingRefusal(t, binding, err, CodeWorkspaceShapeUnsupported, ReasonMetadataValues)
			if fixture.witness.closes != 1 {
				t.Fatalf("witness closes=%d, want 1", fixture.witness.closes)
			}
		})
	}
}

func TestBindProviderConfigurationInvalidDatabaseCleanupWins(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "invalid-database",
	})
	fixture.witness.closeErr = errors.Join(workspaceidentity.ErrCleanup, workspaceidentity.ErrUnverifiable)
	binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), fixture.dependencies())
	refusal := requireBindingRefusal(t, binding, err, CodeWorkspaceUnverifiable, ReasonCleanup)
	if !errors.Is(refusal, workspaceidentity.ErrCleanup) || !errors.Is(refusal, workspaceidentity.ErrUnverifiable) || fixture.witness.closes != 1 {
		t.Fatalf("refusal=%#v closes=%d, want cleanup/unverifiable and one close", refusal, fixture.witness.closes)
	}
}

func TestProviderConfigurationNilZeroAndCopiedLifecycle(t *testing.T) {
	var nilBinding *ProviderConfigurationBinding
	if configuration, err := nilBinding.Snapshot(); configuration != (BoundProviderConfiguration{}) || !errors.Is(err, workspaceidentity.ErrClosed) {
		t.Fatalf("nil Snapshot=%#v, %v, want zero/ErrClosed", configuration, err)
	}
	if err := nilBinding.Close(); err != nil {
		t.Fatalf("nil Close: %v", err)
	}

	zero := &ProviderConfigurationBinding{}
	if configuration, err := zero.Snapshot(); configuration != (BoundProviderConfiguration{}) || !errors.Is(err, workspaceidentity.ErrClosed) {
		t.Fatalf("zero Snapshot=%#v, %v, want zero/ErrClosed", configuration, err)
	}
	if err := zero.Close(); err != nil {
		t.Fatalf("zero Close: %v", err)
	}

	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), fixture.dependencies())
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}
	copyForFormatting := *binding
	for _, rendered := range []string{fmt.Sprint(copyForFormatting), fmt.Sprintf("%+v", copyForFormatting), fmt.Sprintf("%#v", copyForFormatting)} {
		if rendered != safeConfigurationText {
			t.Fatalf("copied binding format=%q, want fixed safe text", rendered)
		}
	}
	if err := binding.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestProviderConfigurationCopiesCallerStringsAtBind(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{
		Backend:      configfile.BackendDolt,
		DoltMode:     configfile.DoltModeEmbedded,
		DoltDatabase: "source_database",
	})
	request := validProviderConfigurationRequest(fixture.workspace)
	binding, err := bindProviderConfigurationWith(request, fixture.dependencies())
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}
	defer func() { _ = binding.Close() }()

	request.Selection.Workspace = "/private/reassigned/.beads"
	request.Target.Locator = "postgresql://changed@changed.example/changed?sslmode=require"
	request.Target.Schema = "changed_schema"
	configuration, err := binding.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if source := configuration.Source(); source.BeadsDir() != fixture.workspace || source.Database() != "source_database" {
		t.Fatalf("source changed after caller reassignment: %#v", source)
	}
	if target := configuration.Target(); target.BaseDSN() != "postgresql://beads@db.example.test:5432/beads?sslmode=verify-full" || target.Schema() != "migration_target" {
		t.Fatalf("target changed after caller reassignment: %#v", target)
	}
}

func TestProviderConfigurationSnapshotStableCheckpoint(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "source_db"})
	binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), fixture.dependencies())
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}
	defer func() { _ = binding.Close() }()

	first, err := binding.Snapshot()
	if err != nil {
		t.Fatalf("first Snapshot: %v", err)
	}
	second, err := binding.Snapshot()
	if err != nil {
		t.Fatalf("second Snapshot: %v", err)
	}
	if first != second {
		t.Fatalf("snapshots differ: %#v %#v", first, second)
	}
	if fixture.observes != 7 || fixture.filesystems != 6 || fixture.witness.revalidateAt != 14 {
		t.Fatalf("observes=%d filesystems=%d revalidates=%d, want 7/6/14", fixture.observes, fixture.filesystems, fixture.witness.revalidateAt)
	}
}

func TestProviderConfigurationSnapshotFirstResultWins(t *testing.T) {
	t.Run("first shape mismatch stops later callbacks", func(t *testing.T) {
		fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
		deps := fixture.dependencies()
		baseObserve := deps.observe
		deps.observe = func(path string) (shapeObservation, error) {
			shape, err := baseObserve(path)
			if fixture.observes == 4 {
				shape.provider.canonical += "-changed"
			}
			return shape, err
		}
		baseInspect := deps.inspectFS
		deps.inspectFS = func(witness sourceWitness) (workspaceidentity.FilesystemSnapshot, error) {
			if fixture.filesystems >= 2 {
				return workspaceidentity.FilesystemSnapshot{}, errors.Join(workspaceidentity.ErrCleanup, errors.New("later filesystem error must not win"))
			}
			return baseInspect(witness)
		}
		binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), deps)
		if err != nil {
			t.Fatalf("BindProviderConfiguration: %v", err)
		}
		defer func() { _ = binding.Close() }()
		configuration, err := binding.Snapshot()
		if configuration != (BoundProviderConfiguration{}) {
			t.Fatalf("configuration=%#v, want zero", configuration)
		}
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != CodeWorkspaceChanged || refusal.Reason != ReasonWorkspaceObservation {
			t.Fatalf("Snapshot error=%v refusal=%#v", err, refusal)
		}
		if fixture.observes != 4 || fixture.filesystems != 2 || fixture.witness.revalidateAt != 6 {
			t.Fatalf("observes=%d filesystems=%d revalidates=%d, want fail-fast 4/2/6", fixture.observes, fixture.filesystems, fixture.witness.revalidateAt)
		}
	})

	t.Run("first filesystem mismatch stops later callbacks", func(t *testing.T) {
		fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
		deps := fixture.dependencies()
		equalities := 0
		deps.equalFS = func(workspaceidentity.FilesystemSnapshot, workspaceidentity.FilesystemSnapshot) bool {
			equalities++
			return equalities == 1
		}
		baseObserve := deps.observe
		deps.observe = func(path string) (shapeObservation, error) {
			observed, observeErr := baseObserve(path)
			if fixture.observes >= 5 {
				return shapeObservation{}, errors.Join(workspaceidentity.ErrCleanup, errors.New("later observation error must not win"))
			}
			return observed, observeErr
		}
		binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), deps)
		if err != nil {
			t.Fatalf("BindProviderConfiguration: %v", err)
		}
		defer func() { _ = binding.Close() }()
		_, err = binding.Snapshot()
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != CodeWorkspaceChanged {
			t.Fatalf("Snapshot error=%v refusal=%#v", err, refusal)
		}
		if fixture.observes != 4 || fixture.filesystems != 3 || fixture.witness.revalidateAt != 7 {
			t.Fatalf("observes=%d filesystems=%d revalidates=%d, want fail-fast 4/3/7", fixture.observes, fixture.filesystems, fixture.witness.revalidateAt)
		}
	})

	t.Run("filesystem cleanup supersedes drift", func(t *testing.T) {
		fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
		deps := fixture.dependencies()
		baseInspect := deps.inspectFS
		deps.inspectFS = func(witness sourceWitness) (workspaceidentity.FilesystemSnapshot, error) {
			if fixture.filesystems == 2 {
				fixture.filesystems++
				return workspaceidentity.FilesystemSnapshot{}, errors.Join(workspaceidentity.ErrChanged, workspaceidentity.ErrCleanup)
			}
			return baseInspect(witness)
		}
		binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), deps)
		if err != nil {
			t.Fatalf("BindProviderConfiguration: %v", err)
		}
		defer func() { _ = binding.Close() }()
		_, err = binding.Snapshot()
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != CodeWorkspaceUnverifiable || refusal.Reason != ReasonCleanup ||
			!errors.Is(err, workspaceidentity.ErrChanged) || !errors.Is(err, workspaceidentity.ErrCleanup) {
			t.Fatalf("Snapshot error=%v refusal=%#v", err, refusal)
		}
		if fixture.observes != 4 || fixture.filesystems != 3 || fixture.witness.revalidateAt != 6 {
			t.Fatalf("observes=%d filesystems=%d revalidates=%d, want fail-fast 4/3/6", fixture.observes, fixture.filesystems, fixture.witness.revalidateAt)
		}
	})
}

func TestProviderConfigurationSnapshotWinnerMatrix(t *testing.T) {
	witnessFailure := func(frontier int, operationErr error) func(*selectionFixture, *selectionDependencies) {
		return func(fixture *selectionFixture, _ *selectionDependencies) {
			fixture.witness.revalidations = make([]error, 9)
			fixture.witness.revalidations[4+frontier] = operationErr
		}
	}
	observationFailure := func(observation int, operationErr error) func(*selectionFixture, *selectionDependencies) {
		return func(fixture *selectionFixture, deps *selectionDependencies) {
			baseObserve := deps.observe
			deps.observe = func(path string) (shapeObservation, error) {
				observed, err := baseObserve(path)
				if fixture.observes == observation {
					return shapeObservation{}, operationErr
				}
				return observed, err
			}
		}
	}
	filesystemFailure := func(filesystem int, operationErr error) func(*selectionFixture, *selectionDependencies) {
		return func(fixture *selectionFixture, deps *selectionDependencies) {
			baseInspect := deps.inspectFS
			deps.inspectFS = func(witness sourceWitness) (workspaceidentity.FilesystemSnapshot, error) {
				if fixture.filesystems+1 == filesystem {
					fixture.filesystems++
					return workspaceidentity.FilesystemSnapshot{}, operationErr
				}
				return baseInspect(witness)
			}
		}
	}
	shapeMismatch := func(observation int) func(*selectionFixture, *selectionDependencies) {
		return func(fixture *selectionFixture, deps *selectionDependencies) {
			baseObserve := deps.observe
			deps.observe = func(path string) (shapeObservation, error) {
				observed, err := baseObserve(path)
				if fixture.observes == observation {
					observed.provider.canonical += "-changed"
				}
				return observed, err
			}
		}
	}
	filesystemMismatch := func(equality int) func(*selectionFixture, *selectionDependencies) {
		return func(_ *selectionFixture, deps *selectionDependencies) {
			equalities := 0
			deps.equalFS = func(workspaceidentity.FilesystemSnapshot, workspaceidentity.FilesystemSnapshot) bool {
				equalities++
				return equalities != equality
			}
		}
	}

	tests := []struct {
		name          string
		configure     func(*selectionFixture, *selectionDependencies)
		code          RefusalCode
		reason        RefusalReason
		causes        []error
		observes      int
		filesystems   int
		revalidations int
	}{
		{name: "witness first frontier", configure: witnessFailure(0, workspaceidentity.ErrChanged), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 3, filesystems: 2, revalidations: 5},
		{name: "witness after first shape", configure: witnessFailure(1, workspaceidentity.ErrChanged), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 4, filesystems: 2, revalidations: 6},
		{name: "witness after first filesystem", configure: witnessFailure(2, workspaceidentity.ErrChanged), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 4, filesystems: 3, revalidations: 7},
		{name: "witness after second shape", configure: witnessFailure(3, workspaceidentity.ErrChanged), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 5, filesystems: 3, revalidations: 8},
		{name: "witness final frontier", configure: witnessFailure(4, workspaceidentity.ErrChanged), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 5, filesystems: 4, revalidations: 9},
		{name: "witness unsupported", configure: witnessFailure(0, workspaceidentity.ErrUnsupported), code: CodePlatformUnsupported, reason: ReasonFilesystem, causes: []error{workspaceidentity.ErrUnsupported}, observes: 3, filesystems: 2, revalidations: 5},
		{name: "witness cleanup overrides changed", configure: witnessFailure(0, errors.Join(workspaceidentity.ErrChanged, workspaceidentity.ErrCleanup)), code: CodeWorkspaceUnverifiable, reason: ReasonCleanup, causes: []error{workspaceidentity.ErrChanged, workspaceidentity.ErrCleanup}, observes: 3, filesystems: 2, revalidations: 5},
		{name: "first observation error", configure: observationFailure(4, &shapeObservationError{reason: ReasonProvider, cause: os.ErrPermission}), code: CodeWorkspaceUnverifiable, reason: ReasonProvider, causes: []error{os.ErrPermission}, observes: 4, filesystems: 2, revalidations: 5},
		{name: "second observation error", configure: observationFailure(5, &shapeObservationError{reason: ReasonProvider, cause: os.ErrPermission}), code: CodeWorkspaceUnverifiable, reason: ReasonProvider, causes: []error{os.ErrPermission}, observes: 5, filesystems: 3, revalidations: 7},
		{name: "observation cleanup overrides error", configure: observationFailure(4, errors.Join(&shapeObservationError{reason: ReasonProvider, cause: os.ErrPermission}, workspaceidentity.ErrCleanup)), code: CodeWorkspaceUnverifiable, reason: ReasonCleanup, causes: []error{os.ErrPermission, workspaceidentity.ErrCleanup}, observes: 4, filesystems: 2, revalidations: 5},
		{name: "first shape mismatch", configure: shapeMismatch(4), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 4, filesystems: 2, revalidations: 6},
		{name: "second shape mismatch", configure: shapeMismatch(5), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 5, filesystems: 3, revalidations: 8},
		{name: "first filesystem probe error", configure: filesystemFailure(3, &fakeFilesystemProbeFailure{cause: os.ErrPermission}), code: CodeWorkspaceUnverifiable, reason: ReasonFilesystemProbe, causes: []error{os.ErrPermission}, observes: 4, filesystems: 3, revalidations: 6},
		{name: "second filesystem probe error", configure: filesystemFailure(4, &fakeFilesystemProbeFailure{cause: os.ErrPermission}), code: CodeWorkspaceUnverifiable, reason: ReasonFilesystemProbe, causes: []error{os.ErrPermission}, observes: 5, filesystems: 4, revalidations: 8},
		{name: "filesystem cleanup overrides error", configure: filesystemFailure(3, errors.Join(&fakeFilesystemProbeFailure{cause: os.ErrPermission}, workspaceidentity.ErrCleanup)), code: CodeWorkspaceUnverifiable, reason: ReasonCleanup, causes: []error{os.ErrPermission, workspaceidentity.ErrCleanup}, observes: 4, filesystems: 3, revalidations: 6},
		{name: "first filesystem mismatch", configure: filesystemMismatch(2), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 4, filesystems: 3, revalidations: 7},
		{name: "second filesystem mismatch", configure: filesystemMismatch(3), code: CodeWorkspaceChanged, reason: ReasonWorkspaceObservation, causes: []error{workspaceidentity.ErrChanged}, observes: 5, filesystems: 4, revalidations: 9},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
			deps := fixture.dependencies()
			test.configure(fixture, &deps)
			binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), deps)
			if err != nil {
				t.Fatalf("BindProviderConfiguration: %v", err)
			}
			defer func() { _ = binding.Close() }()

			configuration, err := binding.Snapshot()
			if configuration != (BoundProviderConfiguration{}) {
				t.Fatalf("configuration=%#v, want zero", configuration)
			}
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != test.code || refusal.Reason != test.reason || refusal.Effect != effectNone {
				t.Fatalf("Snapshot error=%v refusal=%#v, want %s/%s", err, refusal, test.code, test.reason)
			}
			for _, cause := range test.causes {
				if !errors.Is(err, cause) {
					t.Errorf("Snapshot error does not preserve %v", cause)
				}
			}
			if fixture.observes != test.observes || fixture.filesystems != test.filesystems || fixture.witness.revalidateAt != test.revalidations {
				t.Fatalf("observes=%d filesystems=%d revalidates=%d, want fail-fast %d/%d/%d",
					fixture.observes, fixture.filesystems, fixture.witness.revalidateAt,
					test.observes, test.filesystems, test.revalidations)
			}
		})
	}
}

func TestProviderConfigurationSnapshotFailureDoesNotRefreshBaseline(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	deps := fixture.dependencies()
	baseObserve := deps.observe
	deps.observe = func(path string) (shapeObservation, error) {
		observed, err := baseObserve(path)
		if fixture.observes == 4 {
			observed.provider.canonical += "-transient"
		}
		return observed, err
	}
	binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), deps)
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}
	defer func() { _ = binding.Close() }()

	if configuration, err := binding.Snapshot(); configuration != (BoundProviderConfiguration{}) || !errors.Is(err, workspaceidentity.ErrChanged) {
		t.Fatalf("first Snapshot=%#v, %v, want zero/changed", configuration, err)
	}
	if configuration, err := binding.Snapshot(); configuration == (BoundProviderConfiguration{}) || err != nil {
		t.Fatalf("second stable Snapshot=%#v, %v", configuration, err)
	}
}

func TestProviderConfigurationCloseFailureIsStable(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	fixture.witness.closeErr = errors.Join(workspaceidentity.ErrCleanup, workspaceidentity.ErrUnverifiable)
	binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), fixture.dependencies())
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}
	first := binding.Close()
	second := binding.Close()
	for _, closeErr := range []error{first, second} {
		var refusal *Refusal
		if !errors.As(closeErr, &refusal) || refusal.Code != CodeWorkspaceUnverifiable || refusal.Reason != ReasonCleanup ||
			!errors.Is(closeErr, workspaceidentity.ErrCleanup) {
			t.Fatalf("Close error=%v refusal=%#v", closeErr, refusal)
		}
	}
	if first != second || fixture.witness.closes != 1 {
		t.Fatalf("close errors same=%v closes=%d, want true/1", first == second, fixture.witness.closes)
	}
}

func TestProviderConfigurationConcurrentSnapshotAndClose(t *testing.T) {
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded})
	binding, err := bindProviderConfigurationWith(validProviderConfigurationRequest(fixture.workspace), fixture.dependencies())
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, snapshotErr := binding.Snapshot()
		if snapshotErr != nil && !errors.Is(snapshotErr, workspaceidentity.ErrClosed) {
			errs <- snapshotErr
		}
	}()
	go func() {
		defer wg.Done()
		if closeErr := binding.Close(); closeErr != nil {
			errs <- closeErr
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent lifecycle: %v", err)
	}
	if fixture.witness.closes != 1 {
		t.Fatalf("witness closes=%d, want 1", fixture.witness.closes)
	}
}

func TestProviderConfigurationFormattingAndJSONAreSafe(t *testing.T) {
	type privateConfigurationEnvelope struct {
		binding       ProviderConfigurationBinding
		configuration BoundProviderConfiguration
		source        EmbeddedDoltReadOnlyConfiguration
		target        PostgreSQLTargetConfiguration
	}
	fixture := newSelectionFixture(t, configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "private_source_database"})
	request := validProviderConfigurationRequest(fixture.workspace)
	request.Target.Locator = "postgresql://private_user@private.example.test/private_database?sslmode=require"
	request.Target.Schema = "private_schema"
	binding, err := bindProviderConfigurationWith(request, fixture.dependencies())
	if err != nil {
		t.Fatalf("BindProviderConfiguration: %v", err)
	}
	defer func() { _ = binding.Close() }()
	configuration, err := binding.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	values := []any{
		binding,
		*binding,
		configuration,
		configuration.Source(),
		configuration.Target(),
		privateConfigurationEnvelope{
			binding:       *binding,
			configuration: configuration,
			source:        configuration.Source(),
			target:        configuration.Target(),
		},
	}
	for _, value := range values {
		for _, rendered := range []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			for _, private := range []string{fixture.workspace, "private_source_database", "private_user", "private.example.test", "private_database", "private_schema"} {
				if strings.Contains(rendered, private) {
					t.Fatalf("format %q leaked %q", rendered, private)
				}
			}
		}
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatalf("json.Marshal(%T): %v", value, marshalErr)
		}
		if string(encoded) != "{}" {
			t.Fatalf("json.Marshal(%T)=%s, want {}", value, encoded)
		}
	}
}

func TestBindingProductionSourceFence(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "binding.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse binding.go: %v", err)
	}
	allowedImports := map[string]string{
		"net":          "net",
		"net/url":      "url",
		"strconv":      "strconv",
		"strings":      "strings",
		"sync":         "sync",
		"unicode":      "unicode",
		"unicode/utf8": "utf8",
		"github.com/steveyegge/beads/internal/configfile":        "configfile",
		"github.com/steveyegge/beads/internal/workspaceidentity": "workspaceidentity",
	}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		expectedName, ok := allowedImports[path]
		if !ok {
			t.Errorf("binding.go imports non-allowlisted package %q", path)
			continue
		}
		if spec.Name != nil {
			t.Errorf("binding.go aliases or dot-imports %q as %q", path, spec.Name.Name)
			continue
		}
		if expectedName == "" {
			t.Errorf("binding.go import %q has no expected package name", path)
		}
	}
	allowedSelectors := map[string]map[string]bool{
		"net":               {"JoinHostPort": true, "ParseIP": true},
		"url":               {"Parse": true, "QueryUnescape": true, "URL": true, "User": true},
		"strconv":           {"FormatUint": true, "ParseUint": true},
		"strings":           {"Contains": true, "Count": true, "Cut": true, "EqualFold": true, "FieldsFunc": true, "HasPrefix": true, "HasSuffix": true, "LastIndexByte": true, "Split": true, "ToLower": true, "TrimPrefix": true},
		"sync":              {"Mutex": true},
		"unicode":           {"IsControl": true, "IsSpace": true, "MaxASCII": true},
		"utf8":              {"ValidString": true},
		"configfile":        {"BackendPostgres": true, "DefaultDoltDatabase": true},
		"workspaceidentity": {"ErrChanged": true, "ErrClosed": true, "FilesystemSnapshot": true},
	}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		selectors, importedPackage := allowedSelectors[packageName.Name]
		if importedPackage && !selectors[selector.Sel.Name] {
			t.Errorf("binding.go uses non-allowlisted package selector %s.%s", packageName.Name, selector.Sel.Name)
		}
		return true
	})

	productionSymbols := map[string]bool{
		"BindProviderConfiguration":         true,
		"ProviderConfigurationRequest":      true,
		"PostgreSQLTargetRequest":           true,
		"ProviderConfigurationBinding":      true,
		"BoundProviderConfiguration":        true,
		"PostgreSQLTargetConfiguration":     true,
		"EmbeddedDoltReadOnlyConfiguration": true,
	}
	allowedProductionConsumers := map[string]map[string]bool{
		filepath.Join("internal", "backendmigration", "binding.go"): productionSymbols,
		filepath.Join("internal", "backendmigration", "lifecycle.go"): {
			"BindProviderConfiguration":    true,
			"ProviderConfigurationRequest": true,
			"BoundProviderConfiguration":   true,
		},
	}
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	err = filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".beads", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("parse production source %s: %w", path, parseErr)
		}
		relative, relErr := filepath.Rel(repositoryRoot, path)
		if relErr != nil {
			return relErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "BaseDSN" {
					t.Errorf("production source %s calls identity-only BaseDSN", relative)
				}
			}
			if identifier, ok := node.(*ast.Ident); ok && productionSymbols[identifier.Name] {
				if !allowedProductionConsumers[relative][identifier.Name] {
					t.Errorf("production source %s consumes W3 binding symbol %s", relative, identifier.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk production source: %v", err)
	}
}
