//go:build !linux

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The helper exists only on Linux. Elsewhere the dispatch must leave the name alone, so
// the ordinary lookup answers "unknown command" as for any other unknown name.
func TestSecretHelperNotAvailable(t *testing.T) {
	var out, errOut bytes.Buffer
	code, ok := runSecretHelper([]string{"__secret-service"}, strings.NewReader("{}"), &out, &errOut)
	if ok {
		t.Error("the helper is handled on a platform without it")
	}
	if code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("wrote stdout %q, stderr %q", out.String(), errOut.String())
	}

	var stdout, stderr bytes.Buffer
	code = mainWith([]string{"__secret-service"}, Build{}, strings.NewReader("{}"), &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit code %d, want %d; stdout:\n%s\nstderr:\n%s", code, exitUsage, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "__secret-service"`) {
		t.Errorf("stderr does not call the name unknown:\n%s", stderr.String())
	}
}
