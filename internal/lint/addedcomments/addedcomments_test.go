package addedcomments

import (
	"reflect"
	"testing"
)

func TestReportsTheCommentsReviewFlaggedOnMergedPullRequests(t *testing.T) {
	diff := `diff --git a/internal/daemon/reload.go b/internal/daemon/reload.go
--- a/internal/daemon/reload.go
+++ b/internal/daemon/reload.go
@@ -76,0 +77,3 @@ func (l *lease) release() {
+// sessionLifecycleLockFor serializes each session's close, reload and continuation
+// composites. Leases keep an entry alive until its final waiter releases it.
+func (d *Daemon) sessionLifecycleLockFor(id string) *lease {
diff --git a/app/src-tauri/core/src/native_input.rs b/app/src-tauri/core/src/native_input.rs
--- a/app/src-tauri/core/src/native_input.rs
+++ b/app/src-tauri/core/src/native_input.rs
@@ -456,0 +457,2 @@ fn uptime() -> f64 {
+    // A window that never became key still has itself as first responder, and
+    // NSResponder's keyDown goes nowhere from there.
diff --git a/app/scripts/real-app-harness/focusFreeSweep.test.mjs b/app/scripts/real-app-harness/focusFreeSweep.test.mjs
--- /dev/null
+++ b/app/scripts/real-app-harness/focusFreeSweep.test.mjs
@@ -0,0 +1,3 @@
+const DRIVER_CLASSES = new Set(['MacOSDriver', 'LinuxDriver']);
+/** Files that post input through macOS on purpose. */
+const TAKES_THE_FOREGROUND = new Set([]);
`
	got := FindInUnifiedDiff(diff)
	want := []Finding{
		{"internal/daemon/reload.go", 77, "// sessionLifecycleLockFor serializes each session's close, reload and continuation"},
		{"internal/daemon/reload.go", 78, "// composites. Leases keep an entry alive until its final waiter releases it."},
		{"app/src-tauri/core/src/native_input.rs", 457, "// A window that never became key still has itself as first responder, and"},
		{"app/src-tauri/core/src/native_input.rs", 458, "// NSResponder's keyDown goes nowhere from there."},
		{"app/scripts/real-app-harness/focusFreeSweep.test.mjs", 2, "/** Files that post input through macOS on purpose. */"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %#v, want %#v", got, want)
	}
}

func TestLeavesAloneWhatIsNotANewProseComment(t *testing.T) {
	diff := `--- a/internal/store/store.go
+++ b/internal/store/store.go
@@ -10,2 +10,0 @@
-	// Rows stay ordered by insertion.
-	legacy()
@@ -40,0 +39,6 @@
+	// Rows stay ordered by insertion.
+	//go:noinline
+	//nolint:errcheck
+	*total = *total * 2
+	url := "https://example.com" // trailing
+	rows.Close()
--- a/app/src/view.tsx
+++ b/app/src/view.tsx
@@ -1,0 +2,3 @@
+// @ts-expect-error upstream types lag the runtime
+// oxlint-disable-next-line no-console
+/// <reference types="vite/client" />
--- a/internal/protocol/generated.go
+++ b/internal/protocol/generated.go
@@ -1,0 +2 @@
+// Command is sent by the app.
--- a/internal/lint/commentblock/testdata/src/a/a.go
+++ b/internal/lint/commentblock/testdata/src/a/a.go
@@ -1,0 +2 @@
+// fixture comment
--- a/docs/glossary.md
+++ b/docs/glossary.md
@@ -1,0 +2 @@
+// not code
--- a/scripts/release.sh
+++ b/scripts/release.sh
@@ -1,0 +2 @@
+# shell is not checked
`
	if got := FindInUnifiedDiff(diff); len(got) != 0 {
		t.Fatalf("findings = %#v, want none", got)
	}
}
