package creds

import (
	"encoding/json"
	"fmt"
)

// ExecInfoEnvVar is the environment variable through which bd tells a credential
// helper the destination it is about to dial. It follows the kubectl
// KUBERNETES_EXEC_INFO idiom: the payload is passed via the environment, never on
// argv, so it is invisible in `ps` and cannot be spoofed through the freeform
// command string. A helper reads it to decide whether the destination is trusted
// before minting a credential.
const ExecInfoEnvVar = "BEADS_EXEC_INFO"

// execInfoAPIVersion is the versioned schema tag on every payload. A helper must
// require this exact value (tolerating only future minor bumps of the same line).
const execInfoAPIVersion = "beads.dev/credential-exec/v1"

// execInfoOriginBD marks a payload as bd-injected. A helper that receives an
// origin==bd payload but cannot resolve a trusted destination must fail closed; a
// direct human invocation (no exec-info, no marker) may keep minting.
const execInfoOriginBD = "bd"

// execInfoSpec carries the dial destination. dialHost is always present and
// canonical (see CanonicalHost); dialPort and database are included only when
// known so a helper can mint a project-pinned token without re-parsing the command.
type execInfoSpec struct {
	DialHost string `json:"dialHost"`
	DialPort int    `json:"dialPort,omitempty"`
	Database string `json:"database,omitempty"`
}

// execInfoPayload is the single-line JSON document written to BEADS_EXEC_INFO.
type execInfoPayload struct {
	APIVersion string       `json:"apiVersion"`
	Origin     string       `json:"origin"`
	Spec       execInfoSpec `json:"spec"`
}

// buildExecInfo renders the BEADS_EXEC_INFO value for a dial to canonHost. The
// host must already be canonical. Returns single-line JSON.
func buildExecInfo(canonHost string, port int, database string) (string, error) {
	p := execInfoPayload{
		APIVersion: execInfoAPIVersion,
		Origin:     execInfoOriginBD,
		Spec: execInfoSpec{
			DialHost: canonHost,
			DialPort: port,
			Database: database,
		},
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("building %s payload: %w", ExecInfoEnvVar, err)
	}
	return string(b), nil
}
