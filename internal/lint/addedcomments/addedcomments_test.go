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

func TestARenamedFileKeepsItsComments(t *testing.T) {
	diff := `diff --git a/internal/a.go b/internal/a.go
deleted file mode 100644
--- a/internal/a.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package a
-// Old explains a.
-func Old() {}
diff --git a/internal/b.go b/internal/b.go
new file mode 100644
--- /dev/null
+++ b/internal/b.go
@@ -0,0 +1,3 @@
+package a
+// Old explains a.
+func Old() {}
`
	if got := FindInUnifiedDiff(diff); len(got) != 0 {
		t.Fatalf("findings = %#v, want none", got)
	}
}

func TestOneRemovedCommentExcusesOneAddedComment(t *testing.T) {
	diff := `--- a/internal/a.go
+++ b/internal/a.go
@@ -4 +4,2 @@
-// TODO
+// TODO
+// TODO
--- a/docs/notes.md
+++ b/docs/notes.md
@@ -1 +0,0 @@
-// TODO
`
	got := FindInUnifiedDiff(diff)
	if want := []Finding{{"internal/a.go", 5, "// TODO"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %#v, want %#v", got, want)
	}
}

func TestProseThatStartsLikeADirectiveIsStillProse(t *testing.T) {
	diff := `--- a/internal/a.go
+++ b/internal/a.go
@@ -1,0 +2,4 @@
+// system must restart here
+// line up the ducks
+// export the thing
+//export realDirective
`
	if got := FindInUnifiedDiff(diff); len(got) != 3 {
		t.Fatalf("findings = %#v, want the three prose lines", got)
	}
}

func TestContentThatLooksLikeDiffSyntaxDoesNotDerailTheParser(t *testing.T) {
	diff := "--- a/internal/sp ace.go\t\n+++ b/internal/sp ace.go\t\n@@ -3,2 +3,3 @@\n--- old banner\n-x := 1\n+++ counter\n+    * factor\n+// added after the odd lines\n"
	got := FindInUnifiedDiff(diff)
	if want := []Finding{{"internal/sp ace.go", 5, "// added after the odd lines"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %#v, want %#v", got, want)
	}
}

func TestBlockFormToolMarkersAreExempt(t *testing.T) {
	diff := `--- a/app/src/a.ts
+++ b/app/src/a.ts
@@ -1,0 +2,3 @@
+/* eslint-disable no-console */
+/* v8 ignore next */
+/** Explains the export. */
`
	got := FindInUnifiedDiff(diff)
	if want := []Finding{{"app/src/a.ts", 4, "/** Explains the export. */"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %#v, want %#v", got, want)
	}
}

func TestANewCgoPreambleIsNotProse(t *testing.T) {
	diff := `--- /dev/null
+++ b/internal/ghosttyvt/fresh.go
@@ -0,0 +1,7 @@
+package ghosttyvt
+
+/*
+#include <stdint.h>
+static int one(void) { return 1; }
+*/
+import "C"
`
	if got := FindInUnifiedDiff(diff); len(got) != 0 {
		t.Fatalf("findings = %#v, want none", got)
	}
}

func TestABareBlockOpenerIsOnlyExemptAsACgoPreamble(t *testing.T) {
	for name, diff := range map[string]string{
		"go block comment": `--- a/internal/store/sqlite.go
+++ b/internal/store/sqlite.go
@@ -10,0 +11,4 @@
+/*
+The image is built once.
+*/
+func migratedSchema() {}
`,
		"rust block comment": `--- a/app/src-tauri/src/main.rs
+++ b/app/src-tauri/src/main.rs
@@ -10,0 +11,4 @@
+/*
+#include <stdint.h>
+*/
+import "C"
`,
		"preamble cut off by the hunk": `--- a/internal/ghosttyvt/fresh.go
+++ b/internal/ghosttyvt/fresh.go
@@ -10,0 +11,2 @@
+/*
+#include <stdint.h>
`,
	} {
		got := FindInUnifiedDiff(diff)
		if len(got) != 1 || got[0].Line != 11 || got[0].Text != "/*" {
			t.Errorf("%s: findings = %#v, want the opener on line 11", name, got)
		}
	}
}

func TestADiffWithoutATrailingNewlineStillReportsItsLastLine(t *testing.T) {
	diff := "--- a/internal/store/sqlite.go\n+++ b/internal/store/sqlite.go\n@@ -10,0 +11 @@\n+// The image is built once."
	if got := FindInUnifiedDiff(diff); len(got) != 1 || got[0].Line != 11 {
		t.Fatalf("findings = %#v, want one on line 11", got)
	}
}
