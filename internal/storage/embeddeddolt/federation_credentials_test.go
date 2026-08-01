//go:build cgo

package embeddeddolt

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

func newPeerAuthTestStore(t *testing.T) *EmbeddedDoltStore {
	t.Helper()
	ctx := t.Context()

	// Several cases here deliberately build the state that warns about a
	// suppressed ambient password; keep that off the test log unless a test
	// installs its own writer to assert on it.
	prevWarn := federationWarnWriter
	federationWarnWriter = io.Discard
	t.Cleanup(func() { federationWarnWriter = prevWarn })

	beadsDir := filepath.Join(t.TempDir(), ".beads")
	store, err := Open(ctx, beadsDir, "fedauth", "main")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// Regression test for GH#5080: credentials stored by add-peer must be the
// ones presented when syncing with that peer, overriding the environment
// pair as a unit; remotes without a stored peer keep the environment
// fallback.
func TestWithPeerAuth(t *testing.T) {
	cases := []struct {
		name       string
		peer       *storage.FederationPeer
		remote     string
		wantUser   string
		wantPwd    string
		wantPwdSet bool
		wantUsrEnv string
		wantUsrSet bool
	}{
		{
			name: "stored peer credentials win",
			peer: &storage.FederationPeer{
				Name:      "team",
				RemoteURL: "https://peer.example/peerdb",
				Username:  "peeruser",
				Password:  "peerpass",
			},
			remote:   "team",
			wantUser: "peeruser",
			wantPwd:  "peerpass", wantPwdSet: true,
			wantUsrEnv: "peeruser", wantUsrSet: true,
		},
		{
			name: "stored empty password does not inherit ambient password",
			peer: &storage.FederationPeer{
				Name:      "open-pwd",
				RemoteURL: "https://peer.example/peerdb",
				Username:  "peeruser",
			},
			remote:   "open-pwd",
			wantUser: "peeruser",
			wantPwd:  "", wantPwdSet: false,
			wantUsrEnv: "peeruser", wantUsrSet: true,
		},
		{
			name: "stored password with empty username does not inherit ambient user",
			peer: &storage.FederationPeer{
				Name:      "open-usr",
				RemoteURL: "https://peer.example/peerdb",
				Password:  "peerpass",
			},
			remote:   "open-usr",
			wantUser: "",
			wantPwd:  "peerpass", wantPwdSet: true,
			wantUsrEnv: "", wantUsrSet: false,
		},
		{
			name:     "unknown remote falls back to env",
			remote:   "not-a-peer",
			wantUser: "envuser",
			wantPwd:  "envpass", wantPwdSet: true,
			wantUsrEnv: "envuser", wantUsrSet: true,
		},
		{
			name: "credential-free peer falls back to env",
			peer: &storage.FederationPeer{
				Name:      "open-peer",
				RemoteURL: "https://peer.example/peerdb",
			},
			remote:   "open-peer",
			wantUser: "envuser",
			wantPwd:  "envpass", wantPwdSet: true,
			wantUsrEnv: "envuser", wantUsrSet: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := newPeerAuthTestStore(t)

			t.Setenv("DOLT_REMOTE_USER", "envuser")
			t.Setenv("DOLT_REMOTE_PASSWORD", "envpass")

			if tc.peer != nil {
				if err := store.AddFederationPeer(ctx, tc.peer); err != nil {
					t.Fatalf("AddFederationPeer: %v", err)
				}
			}

			var gotUser, gotPwd, gotUsrEnv string
			var gotPwdSet, gotUsrSet bool
			err := store.withPeerAuth(ctx, tc.remote, func(user string) error {
				gotUser = user
				gotPwd, gotPwdSet = os.LookupEnv("DOLT_REMOTE_PASSWORD")
				gotUsrEnv, gotUsrSet = os.LookupEnv("DOLT_REMOTE_USER")
				return nil
			})
			if err != nil {
				t.Fatalf("withPeerAuth: %v", err)
			}
			if gotUser != tc.wantUser {
				t.Errorf("user = %q, want %q", gotUser, tc.wantUser)
			}
			if gotPwdSet != tc.wantPwdSet || gotPwd != tc.wantPwd {
				t.Errorf("DOLT_REMOTE_PASSWORD during fn = %q (set=%v), want %q (set=%v)", gotPwd, gotPwdSet, tc.wantPwd, tc.wantPwdSet)
			}
			if gotUsrSet != tc.wantUsrSet || gotUsrEnv != tc.wantUsrEnv {
				t.Errorf("DOLT_REMOTE_USER during fn = %q (set=%v), want %q (set=%v)", gotUsrEnv, gotUsrSet, tc.wantUsrEnv, tc.wantUsrSet)
			}
			if got := os.Getenv("DOLT_REMOTE_PASSWORD"); got != "envpass" {
				t.Errorf("DOLT_REMOTE_PASSWORD after fn = %q, want restored %q", got, "envpass")
			}
			if got := os.Getenv("DOLT_REMOTE_USER"); got != "envuser" {
				t.Errorf("DOLT_REMOTE_USER after fn = %q, want restored %q", got, "envuser")
			}
		})
	}
}

// Diagnostic from the GH#5085 review: a peer stored with a username and no
// password suppresses an ambient DOLT_REMOTE_PASSWORD (the security default),
// which turns a setup that used to authenticate off the environment into an
// auth failure with no visible cause. One line names the cause and the fix.
func TestWithPeerAuth_WarnsWhenStoredPeerSuppressesAmbientPassword(t *testing.T) {
	// Pinned verbatim, not matched by substring: the peer name, the username,
	// and the Warning: prefix are the parts an operator reads, so a wording
	// change has to be made here deliberately.
	const wantOpenPwdWarning = `Warning: peer "open-pwd" stores a username with an empty password, ` +
		`which overrides the ambient DOLT_REMOTE_PASSWORD for this operation; ` +
		`store a password with 'bd federation add-peer open-pwd <url> ` +
		`--user peeruser --password <password>'.` + "\n"

	cases := []struct {
		name       string
		peer       *storage.FederationPeer
		remote     string
		ambient    string
		ambientSet bool
		// directPeer, when set, calls the warning helper itself instead of
		// going through withPeerAuth, pinning the helper's own boundary.
		directPeer *storage.FederationPeer
		wantLine   string
	}{
		{
			name: "username with empty stored password suppresses ambient password",
			peer: &storage.FederationPeer{
				Name:      "open-pwd",
				RemoteURL: "https://peer.example/peerdb",
				Username:  "peeruser",
			},
			remote: "open-pwd", ambient: "envpass", ambientSet: true,
			wantLine: wantOpenPwdWarning,
		},
		{
			name: "stored password suppresses nothing the operator can act on",
			peer: &storage.FederationPeer{
				Name:      "team",
				RemoteURL: "https://peer.example/peerdb",
				Username:  "peeruser",
				Password:  "peerpass",
			},
			remote: "team", ambient: "envpass", ambientSet: true,
		},
		{
			name: "no ambient password to suppress",
			peer: &storage.FederationPeer{
				Name:      "open-pwd",
				RemoteURL: "https://peer.example/peerdb",
				Username:  "peeruser",
			},
			remote: "open-pwd",
		},
		{
			name: "empty ambient password reads as no ambient password",
			peer: &storage.FederationPeer{
				Name:      "open-pwd",
				RemoteURL: "https://peer.example/peerdb",
				Username:  "peeruser",
			},
			remote: "open-pwd", ambient: "", ambientSet: true,
		},
		{
			name: "peer without a username keeps the password puzzle out of scope",
			peer: &storage.FederationPeer{
				Name:      "open-usr",
				RemoteURL: "https://peer.example/peerdb",
				Password:  "peerpass",
			},
			remote: "open-usr", ambient: "envpass", ambientSet: true,
		},
		{
			name:   "unknown remote keeps the environment fallback",
			remote: "not-a-peer", ambient: "envpass", ambientSet: true,
		},
		{
			// withPeerAuth returns before the helper when a peer stores
			// neither field, so a direct call is the only way to reach the
			// helper's defensive empty-username clause.
			name:       "helper called directly with an all-empty peer stays silent",
			directPeer: &storage.FederationPeer{},
			remote:     "empty-peer", ambient: "envpass", ambientSet: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			t.Setenv("DOLT_REMOTE_USER", "envuser")
			t.Setenv("DOLT_REMOTE_PASSWORD", tc.ambient)
			if !tc.ambientSet {
				_ = os.Unsetenv("DOLT_REMOTE_PASSWORD")
			}

			var warnings bytes.Buffer
			captureWarnings := func() {
				// Installed after any store: newPeerAuthTestStore points the
				// writer at io.Discard for the rest of the suite.
				prev := federationWarnWriter
				federationWarnWriter = &warnings
				t.Cleanup(func() { federationWarnWriter = prev })
			}

			if tc.directPeer != nil {
				captureWarnings()
				warnStoredPeerSuppressesAmbientPassword(tc.remote, tc.directPeer)
			} else {
				store := newPeerAuthTestStore(t)
				if tc.peer != nil {
					if err := store.AddFederationPeer(ctx, tc.peer); err != nil {
						t.Fatalf("AddFederationPeer: %v", err)
					}
				}
				captureWarnings()
				if err := store.withPeerAuth(ctx, tc.remote, func(string) error { return nil }); err != nil {
					t.Fatalf("withPeerAuth: %v", err)
				}
			}

			if got := warnings.String(); got != tc.wantLine {
				t.Errorf("warning = %q, want %q", got, tc.wantLine)
			}
		})
	}
}

func TestWithPeerAuth_RestoresEnvWhenCallbackFails(t *testing.T) {
	ctx := t.Context()
	store := newPeerAuthTestStore(t)

	t.Setenv("DOLT_REMOTE_PASSWORD", "envpass")
	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name: "team", RemoteURL: "https://peer.example/peerdb",
		Username: "peeruser", Password: "peerpass",
	}); err != nil {
		t.Fatalf("AddFederationPeer: %v", err)
	}

	sentinel := errors.New("boom")
	err := store.withPeerAuth(ctx, "team", func(string) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("withPeerAuth error = %v, want sentinel", err)
	}
	if got := os.Getenv("DOLT_REMOTE_PASSWORD"); got != "envpass" {
		t.Errorf("DOLT_REMOTE_PASSWORD after failed fn = %q, want restored %q", got, "envpass")
	}
}

func TestWithPeerAuth_RestoresAbsentEnv(t *testing.T) {
	ctx := t.Context()
	store := newPeerAuthTestStore(t)

	t.Setenv("DOLT_REMOTE_PASSWORD", "scratch")
	_ = os.Unsetenv("DOLT_REMOTE_PASSWORD")
	t.Setenv("DOLT_REMOTE_USER", "scratch")
	_ = os.Unsetenv("DOLT_REMOTE_USER")

	if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
		Name: "team", RemoteURL: "https://peer.example/peerdb",
		Username: "peeruser", Password: "peerpass",
	}); err != nil {
		t.Fatalf("AddFederationPeer: %v", err)
	}

	if err := store.withPeerAuth(ctx, "team", func(string) error { return nil }); err != nil {
		t.Fatalf("withPeerAuth: %v", err)
	}
	if v, set := os.LookupEnv("DOLT_REMOTE_PASSWORD"); set {
		t.Errorf("DOLT_REMOTE_PASSWORD after fn = %q, want unset", v)
	}
	if v, set := os.LookupEnv("DOLT_REMOTE_USER"); set {
		t.Errorf("DOLT_REMOTE_USER after fn = %q, want unset", v)
	}
}

func TestWithPeerAuth_ConcurrentPeersDoNotMixCredentials(t *testing.T) {
	ctx := t.Context()
	store := newPeerAuthTestStore(t)

	peers := map[string]string{"alpha": "alphapass", "beta": "betapass"}
	for name, pwd := range peers {
		if err := store.AddFederationPeer(ctx, &storage.FederationPeer{
			Name: name, RemoteURL: "https://peer.example/" + name,
			Username: name + "-user", Password: pwd,
		}); err != nil {
			t.Fatalf("AddFederationPeer(%s): %v", name, err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for range 10 {
		for name, pwd := range peers {
			wg.Add(1)
			go func(name, pwd string) {
				defer wg.Done()
				errs <- store.withPeerAuth(ctx, name, func(user string) error {
					if got := os.Getenv("DOLT_REMOTE_PASSWORD"); got != pwd {
						t.Errorf("peer %s observed DOLT_REMOTE_PASSWORD %q, want %q", name, got, pwd)
					}
					if want := name + "-user"; user != want {
						t.Errorf("peer %s got user %q, want %q", name, user, want)
					}
					return nil
				})
			}(name, pwd)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("withPeerAuth: %v", err)
		}
	}
}
