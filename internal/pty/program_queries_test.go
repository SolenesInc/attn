package pty

import (
	"context"
	"testing"
	"time"
)

func TestProgramQueriesGetAnAnswer(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"DA1", `\033[c`},
		{"DA2", `\033[>c`},
		{"extended CPR", `\033[?6n`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(nil)
			exited := make(chan struct{})
			m.SetExitHandler(func(ExitInfo) { close(exited) })
			script := `stty raw -echo; printf '` + tc.query + `'; head -c 3 > /dev/null`
			if err := m.Spawn(SpawnOptions{ID: "q", CWD: t.TempDir(), Agent: "codex", Cols: 80, Rows: 24, AgentArgv: []string{"sh", "-c", script}}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { m.Remove("q") })

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			select {
			case <-exited:
			case <-ctx.Done():
				t.Fatalf("the program never got an answer to its %s query", tc.name)
			}
		})
	}
}
