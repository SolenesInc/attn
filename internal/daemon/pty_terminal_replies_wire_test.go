package daemon_test

import (
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/testworld"
)

const (
	cursorAndDeviceQueries = `printf '\033[5;7H\033[6n\033[0c'
IFS= read -rs -d R a; IFS= read -rs -d c b
printf '\033[3;4H\033[0c\033[6n'
IFS= read -rs -d c d; IFS= read -rs -d R e
printf '\033[20;1H'
`
	colorQueries = `printf '\033]11;?\007\033]11;?\007\033]10;?\007\033]12;?\007\033]11;#000000\033\\\033[0c'
IFS= read -rs -d c a
`
	colorSchemeQueries = `printf '\033[?996n\033[?996n'
IFS= read -rs -d n a; IFS= read -rs -d n b
[ "$1" = subscribed ] && printf '\033[?2031h'
printf 'armed-%s\n' "$1"
IFS= read -rs -d c d
`
)

func TestProgramsGetCursorAndDeviceRepliesInTheOrderTheyAsked(t *testing.T) {
	w := newWorld(t)
	shell := w.Spawn(w.App(), shellHarness, w.Path("shop"))

	got := runTerminalQueries(w, transportPeer(w), shell, cursorAndDeviceQueries, `"${a}R|${b}c|${d}c|${e}R"`)
	if want := "\x1b[5;7R|\x1b[?1;2c|\x1b[?1;2c|\x1b[3;4R"; got != want {
		t.Errorf("the program read %q, want the cursor at 5;7 then DA1, then DA1 then the cursor at 3;4: %q", got, want)
	}
}

func TestColorQueriesAreAnsweredOnceEachInOrderFromTheAppTheme(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	shell := w.Spawn(app, shellHarness, w.Path("shop"))
	program := transportPeer(w)

	defaults := runTerminalQueries(w, program, shell, colorQueries, `"$a"`)
	if want := "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\\x1b]10;rgb:d4d4/d4d4/d4d4\x1b\\\x1b]12;rgb:d4d4/d4d4/d4d4\x1b\\\x1b[?1;2"; defaults != want {
		t.Errorf("before the app set a theme the program read %q, want one default reply per query in order and nothing for the set: %q", defaults, want)
	}

	app.Send(protocol.SetTerminalThemeMessage{Cmd: protocol.CmdSetTerminalTheme, Foreground: "#405060", Background: "#102030", Cursor: "#708090"})
	themed := runTerminalQueries(w, program, shell, colorQueries, `"$a"`)
	if want := "\x1b]11;rgb:1010/2020/3030\x1b\\\x1b]11;rgb:1010/2020/3030\x1b\\\x1b]10;rgb:4040/5050/6060\x1b\\\x1b]12;rgb:7070/8080/9090\x1b\\\x1b[?1;2"; themed != want {
		t.Errorf("after the app set a theme the program read %q, want the theme's colors once per query: %q", themed, want)
	}
}

func TestOnlyProgramsSubscribedToMode2031HearTheColorSchemeChange(t *testing.T) {
	for _, mode := range []string{"subscribed", "unsubscribed"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			app := w.App()
			shell := w.Spawn(app, shellHarness, w.Path("shop"))
			program := transportPeer(w)
			program.TypeLine(shell, "bash "+writeTerminalQueryScript(w, colorSchemeQueries, `"${a}n|${b}n|$d"`)+" "+mode)
			transportAwaitOutput(program, shell, "armed-"+mode)
			for _, background := range []string{"#ffffff", "#fefefe"} {
				app.Send(protocol.SetTerminalThemeMessage{Cmd: protocol.CmdSetTerminalTheme, Background: background})
			}
			app.Send(protocol.PtyInputMessage{Cmd: protocol.CmdPtyInput, ID: shell, Data: "c"})

			want := "\x1b[?997;1n|\x1b[?997;1n|"
			if mode == "subscribed" {
				want += "\x1b[?997;2n"
			}
			if got := awaitTerminalQueryResult(program, shell); got != want {
				t.Errorf("the program read %q, want the dark scheme for each query and %q after two light themes", got, strings.TrimPrefix(want, "\x1b[?997;1n|\x1b[?997;1n|"))
			}
		})
	}
}

func runTerminalQueries(w *world, p *testworld.Peer, session, queries, result string) string {
	w.T.Helper()
	p.TypeLine(session, "bash "+writeTerminalQueryScript(w, queries, result))
	return awaitTerminalQueryResult(p, session)
}

func writeTerminalQueryScript(w *world, queries, result string) string {
	w.T.Helper()
	file, err := os.CreateTemp(w.Dir, "query-*.sh")
	if err != nil {
		w.T.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(queries + `printf 'replies=%s=\n' "$(printf '%s' ` + result + ` | od -An -tx1 | tr -d ' \n')"` + "\n"); err != nil {
		w.T.Fatal(err)
	}
	return file.Name()
}

var terminalQueryResult = regexp.MustCompile(`replies=([0-9a-f]*)=`)

func awaitTerminalQueryResult(p *testworld.Peer, session string) string {
	p.T.Helper()
	var seen []byte
	var match [][]byte
	testworld.Await(p, protocol.EventPtyOutput, func(e protocol.WebSocketEvent) bool {
		if protocol.Deref(e.ID) != session {
			return false
		}
		seen = append(seen, transportDecodeOutput(p.T, e)...)
		match = terminalQueryResult.FindSubmatch(seen)
		return match != nil
	})
	replies, err := hex.DecodeString(string(match[1]))
	if err != nil {
		p.T.Fatalf("the program printed replies %q that are not hex: %v", match[1], err)
	}
	return string(replies)
}
