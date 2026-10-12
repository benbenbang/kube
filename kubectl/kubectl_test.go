package kubectl

import (
	"strings"
	"testing"
)

func TestRun_ClientVersion(t *testing.T) {
	// --client needs no cluster
	out, err := Run("version", "--client", "-o", "json")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "clientVersion") {
		t.Errorf("stdout = %q, want the client version", out)
	}
}

func TestRun_ReturnsError(t *testing.T) {
	_, err := Run("no-such-subcommand-xyz")
	if err == nil {
		t.Fatal("expected an error for an unknown subcommand")
	}
	if !strings.Contains(err.Error(), "no-such-subcommand-xyz") {
		t.Errorf("error = %v, want the command in it", err)
	}
}
