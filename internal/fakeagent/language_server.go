package fakeagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/textproto"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"unicode"
)

// LanguageServerName is the name the fake language server answers to: the
// program of attn's built-in Go definition, so a world links it with
// `attn nav link` and reviews of .go files use it.
const LanguageServerName = "gopls"

// LanguageServerLoading is the progress title the fake reports while it loads.
const LanguageServerLoading = "Loading packages"

// InstallLanguageServer links the fake language server into dir and returns
// its path.
func InstallLanguageServer(t testing.TB, binary, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, LanguageServerName)
	if err := os.Symlink(binary, path); err != nil {
		t.Fatal(err)
	}
	return path
}

// runLanguageServer speaks LSP over stdio for Go-like files: a definition is
// the `func Name` or `type Name` line under the workspace or a folder its
// settings name in "search", uses are the other lines naming it, and the type
// is the definition's line. Like a server stuck in its own work, it keeps
// running when its stdin closes, until it is asked to exit or is signalled.
func runLanguageServer() int {
	reader := bufio.NewReader(os.Stdin)
	ls := &fakeLanguageServer{w: os.Stdout}
	for {
		msg, err := readLSP(reader)
		if err != nil {
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
			<-stop
			return 0
		}
		if err := ls.handle(msg); err != nil {
			fmt.Fprintf(os.Stderr, "fake %s: %v\n", LanguageServerName, err)
			return 1
		}
	}
}

type lspMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type fakeLanguageServer struct {
	w     io.Writer
	roots []string
}

type lspPos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspPlace struct {
	URI   string `json:"uri"`
	Range struct {
		Start lspPos `json:"start"`
		End   lspPos `json:"end"`
	} `json:"range"`
}

func (ls *fakeLanguageServer) handle(msg lspMessage) error {
	switch msg.Method {
	case "initialize":
		var params struct {
			RootURI  string `json:"rootUri"`
			Settings struct {
				Search []string `json:"search"`
			} `json:"initializationOptions"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		ls.roots = append([]string{pathOfURI(params.RootURI)}, params.Settings.Search...)
		return ls.reply(msg.ID, map[string]any{"capabilities": map[string]any{"definitionProvider": true, "referencesProvider": true, "hoverProvider": true}})
	case "initialized":
		token := "load"
		if err := ls.send(map[string]any{"jsonrpc": "2.0", "id": 900, "method": "window/workDoneProgress/create", "params": map[string]any{"token": token}}); err != nil {
			return err
		}
		if err := ls.notify("$/progress", map[string]any{"token": token, "value": map[string]any{"kind": "begin", "title": LanguageServerLoading}}); err != nil {
			return err
		}
		return ls.notify("$/progress", map[string]any{"token": token, "value": map[string]any{"kind": "end"}})
	case "textDocument/definition", "textDocument/references", "textDocument/hover":
		var params struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Position lspPos `json:"position"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		name := nameAt(pathOfURI(params.TextDocument.URI), params.Position)
		definition, uses := ls.find(name)
		switch msg.Method {
		case "textDocument/definition":
			if definition == nil {
				return ls.reply(msg.ID, []lspPlace{})
			}
			return ls.reply(msg.ID, []lspPlace{*definition})
		case "textDocument/references":
			return ls.reply(msg.ID, uses)
		default:
			if definition == nil {
				return ls.reply(msg.ID, nil)
			}
			return ls.reply(msg.ID, map[string]any{"contents": map[string]string{"kind": "plaintext", "value": strings.TrimSpace(lineOf(pathOfURI(definition.URI), definition.Range.Start.Line))}})
		}
	case "shutdown":
		return ls.reply(msg.ID, nil)
	case "exit":
		os.Exit(0)
	}
	if len(msg.ID) > 0 && msg.Method != "" {
		return ls.reply(msg.ID, nil)
	}
	return nil
}

func (ls *fakeLanguageServer) find(name string) (*lspPlace, []lspPlace) {
	if name == "" {
		return nil, []lspPlace{}
	}
	declares := regexp.MustCompile(`^\s*(func(\s*\([^)]*\))?|type)\s+` + regexp.QuoteMeta(name) + `\b`)
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	var definition *lspPlace
	uses := []lspPlace{}
	for _, root := range ls.roots {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() && entry.Name() == ".git" {
				return fs.SkipDir
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for i, line := range strings.Split(string(content), "\n") {
				at := word.FindStringIndex(line)
				if at == nil {
					continue
				}
				place := lspPlace{URI: (&url.URL{Scheme: "file", Path: path}).String()}
				place.Range.Start = lspPos{Line: i, Character: at[0]}
				place.Range.End = lspPos{Line: i, Character: at[1]}
				if definition == nil && declares.MatchString(line) {
					definition = &place
					continue
				}
				uses = append(uses, place)
			}
			return nil
		})
	}
	return definition, uses
}

func nameAt(path string, at lspPos) string {
	runes := []rune(lineOf(path, at.Line))
	if at.Character >= len(runes) {
		return ""
	}
	isName := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	start, end := at.Character, at.Character
	for start > 0 && isName(runes[start-1]) {
		start--
	}
	for end < len(runes) && isName(runes[end]) {
		end++
	}
	return string(runes[start:end])
}

func lineOf(path string, line int) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(content), "\n")
	if line < 0 || line >= len(lines) {
		return ""
	}
	return lines[line]
}

func pathOfURI(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return filepath.FromSlash(parsed.Path)
}

func (ls *fakeLanguageServer) reply(id json.RawMessage, result any) error {
	return ls.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (ls *fakeLanguageServer) notify(method string, params any) error {
	return ls.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (ls *fakeLanguageServer) send(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(ls.w, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = ls.w.Write(body)
	return err
}

func readLSP(r *bufio.Reader) (lspMessage, error) {
	header, err := textproto.NewReader(r).ReadMIMEHeader()
	if err != nil {
		return lspMessage{}, err
	}
	length, err := strconv.Atoi(header.Get("Content-Length"))
	if err != nil {
		return lspMessage{}, err
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return lspMessage{}, err
	}
	var msg lspMessage
	return msg, json.Unmarshal(body, &msg)
}
