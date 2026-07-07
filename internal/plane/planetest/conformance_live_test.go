package planetest

import (
	"os"
	"testing"
)

// TestLivePlaneConformance runs the same conformance battery against a real
// Plane deployment. It is gated on environment variables and skips when they
// are absent, so it never runs in CI without explicit opt-in.
//
// Point it at a DEDICATED THROWAWAY PROJECT — the suite creates issues,
// labels, and comments and does not clean them up:
//
//	export PLANE_CONFORMANCE_BASE_URL=https://plane.example.com
//	export PLANE_CONFORMANCE_API_KEY=plane_api_...
//	export PLANE_CONFORMANCE_WORKSPACE=myworkspace
//	export PLANE_CONFORMANCE_PROJECT_ID=<uuid of throwaway project>
//	go test ./internal/plane/planetest/ -run TestLivePlaneConformance -v
func TestLivePlaneConformance(t *testing.T) {
	baseURL := os.Getenv("PLANE_CONFORMANCE_BASE_URL")
	apiKey := os.Getenv("PLANE_CONFORMANCE_API_KEY")
	workspace := os.Getenv("PLANE_CONFORMANCE_WORKSPACE")
	projectID := os.Getenv("PLANE_CONFORMANCE_PROJECT_ID")

	if baseURL == "" || apiKey == "" || workspace == "" || projectID == "" {
		t.Skip("live conformance skipped: set PLANE_CONFORMANCE_BASE_URL, PLANE_CONFORMANCE_API_KEY, PLANE_CONFORMANCE_WORKSPACE, PLANE_CONFORMANCE_PROJECT_ID")
	}

	RunConformance(t, ConformanceTarget{
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Workspace: workspace,
		ProjectID: projectID,
		Live:      true,
	})
}
