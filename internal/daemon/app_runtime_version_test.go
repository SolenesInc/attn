package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppRuntimeAPIVersionMatchesTheHost(t *testing.T) {
	hostPath := filepath.Join("..", "..", "apphost", "src", "index.ts")
	data, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("read the app runtime host: %v", err)
	}
	want := fmt.Sprintf("const APP_RUNTIME_API_VERSION = %d", appRuntimeAPIVersion)
	if !strings.Contains(string(data), want) {
		t.Fatalf("%s must contain %q — the daemon speaks version %d", hostPath, want, appRuntimeAPIVersion)
	}
}
