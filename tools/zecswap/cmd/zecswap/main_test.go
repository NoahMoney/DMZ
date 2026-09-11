package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GAP-029: the maker's secret is read from a file or from stdin, never from the command line.
func TestSecretIsReadFromAFileOrStdin(t *testing.T) {
	secret := strings.Repeat("ab", 32)
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := readSecret(path, nil); err != nil || s != secret {
		t.Fatalf("from a file: %q %v", s, err)
	}
	if s, err := readSecret("-", strings.NewReader(secret+"\n")); err != nil || s != secret {
		t.Fatalf("from stdin: %q %v", s, err)
	}
	for _, bad := range []string{strings.ToUpper(secret), secret[:62], "not hex at all"} {
		if _, err := readSecret("-", strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
