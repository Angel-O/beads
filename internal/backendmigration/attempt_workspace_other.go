//go:build !unix

package backendmigration

import "errors"

func createAttemptWorkspace(string, string) (string, error) {
	return "", errors.New("backend migration workspaces are unsupported on this platform")
}

func removeAttemptWorkspace(string, string, string) error {
	return errors.New("backend migration workspaces are unsupported on this platform")
}
