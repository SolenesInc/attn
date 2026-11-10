package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/victorarias/attn/internal/codenav"
	"github.com/victorarias/attn/internal/diag"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/review"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/stepclock"
)

// CodeNavIdleStopTimer stops a language server no shown review needs.
const CodeNavIdleStopTimer stepclock.Name = "code-nav-idle-stop"

// codeNavIdleStopDefault keeps a server for five minutes after the last review
// that needs it closes, so a review closed and opened again answers at once. On
// attn a restart costs gopls 1.3 s to its first definition with a warm cache and
// 36 s with a cold one (ai-sandbox, 16 cores), 2 to 5 s on the Mac (F8), and it
// holds 1.0 to 1.5 GB while it runs.
const codeNavIdleStopDefault = 300

const codeNavIdleStopMaxSeconds = 86400

// codeNavRequestTripwire ends a question no server answers. The slowest first
// answers measured: gopls on attn with a cold cache in 36 s (ai-sandbox) and
// once 40.9 s on a loaded Mac (F8).
const codeNavRequestTripwire = 2 * time.Minute

func (d *Daemon) codeNavDir(elem ...string) string {
	return filepath.Join(append([]string{filepath.Dir(d.socketPath), "code-navigation"}, elem...)...)
}

func (d *Daemon) newCodeNavigation() *codenav.Manager {
	return codenav.NewManager(codenav.Config{
		BinDir:      d.codeNavDir("bin"),
		ReapDir:     d.codeNavDir("servers"),
		Definitions: d.codeNavDefinitions,
		Inputs: func(ctx context.Context, folder string, globs []string) ([]string, error) {
			return gitValue(ctx, d.gitExecution(), reviewTask, func(ctx context.Context, client *attngit.Client) ([]string, error) {
				return client.ListFilesMatching(ctx, folder, globs)
			})
		},
		IdleStop: func(fn func()) stepclock.Timer {
			return d.clock.AfterFunc(CodeNavIdleStopTimer, d.codeNavIdleStop(), func() {
				release, ok := d.life.Hold("codeNavIdleStop")
				if !ok {
					return
				}
				defer release()
				fn()
			})
		},
		Changed: func(_ codenav.Key, documents []string) {
			d.life.Go("codeNavChanged", func() { d.reportTileDocuments(d.tileDocuments.bump(documents)) })
		},
		Logf: func(format string, args ...any) { d.logf(format, args...) },
	})
}

func (d *Daemon) codeNavIdleStop() time.Duration {
	raw := strings.TrimSpace(d.setting(setting.CodeNavIdleStop))
	if raw == "" {
		return codeNavIdleStopDefault * time.Second
	}
	return time.Duration(resolveBoundedIntSetting(raw, codeNavIdleStopDefault, 0, codeNavIdleStopMaxSeconds)) * time.Second
}

func (d *Daemon) reapStrandedLanguageServers() {
	for _, result := range codenav.Reap(nil, d.codeNavDir("servers")) {
		d.logf("code navigation: reaped %s (pid %d) a stopped daemon left: %s", result.ID, result.PID, result.Outcome)
	}
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func codeNavSettingKey(origin, language string) string {
	return "code_navigation_" + origin + "_" + language
}

// codeNavDefinitions are the built-ins with the definitions set for the
// repository's origin in their place.
func (d *Daemon) codeNavDefinitions(repository string) codenav.Definitions {
	defs := codenav.Definitions{}
	for language, def := range codenav.Builtins() {
		defs[language] = def
	}
	for language, set := range d.codeNavSetDefinitions(d.repositoryOrigin(reviewTask, repository)) {
		if def, err := codenav.ParseDefinition(set); err == nil {
			defs[language] = def
		}
	}
	return defs
}

func (d *Daemon) codeNavSetDefinitions(origin string) map[string]string {
	set := map[string]string{}
	if origin == "" {
		return set
	}
	prefix := codeNavSettingKey(origin, "")
	for key, value := range d.storedSettingValues() {
		language, ok := strings.CutPrefix(key, prefix)
		if ok && language != "" && !strings.Contains(language, "_") && strings.TrimSpace(value) != "" {
			set[language] = value
		}
	}
	return set
}

// codeNavFiles are the files a review shows or changes, absolute: the files
// whose languages it needs a server for.
func codeNavFiles(bundle protocol.ReviewBundle) []string {
	var files []string
	for _, stop := range bundle.Stops {
		files = append(files, filepath.Join(bundle.Repository, filepath.FromSlash(stop.Path)))
	}
	for _, file := range bundle.Files {
		files = append(files, filepath.Join(bundle.Repository, filepath.FromSlash(file.Path)))
	}
	return files
}

// needCodeNavigation starts the servers a watched review needs and fills the
// bundle's navigation with their states.
func (d *Daemon) needCodeNavigation(document string, bundle *protocol.ReviewBundle) {
	if bundle.Repository == "" {
		return
	}
	nav := d.codeNav
	keys := nav.Need(document, bundle.Repository, codeNavFiles(*bundle))
	d.releaseUnlessShown(document)
	languages := map[string]protocol.ReviewNavigation{}
	for _, key := range keys {
		state, detail := nav.State(key)
		current, seen := languages[key.Language]
		if seen && navigationRank[current.State] <= navigationRank[state] {
			continue
		}
		languages[key.Language] = protocol.ReviewNavigation{Language: key.Language, State: state, Detail: optional(detail)}
	}
	bundle.Navigation = make([]protocol.ReviewNavigation, 0, len(languages))
	for _, navigation := range languages {
		bundle.Navigation = append(bundle.Navigation, navigation)
	}
	sort.Slice(bundle.Navigation, func(i, j int) bool { return bundle.Navigation[i].Language < bundle.Navigation[j].Language })
}

// navigationRank orders the states of one language's servers in several
// folders: the reader shows the one furthest from working.
var navigationRank = map[string]int{codenav.StateFailed: 0, codenav.StateMissing: 1, codenav.StateStarting: 2, codenav.StateReady: 3}

// releaseUnlessShown drops a need registered by a read or a question that
// raced with the document leaving the watch set: the watch releases a document
// after removing it, so a check after the need always ends released.
func (d *Daemon) releaseUnlessShown(document string) {
	if d.tileDocuments.version(document) == 0 {
		d.codeNav.Release(document)
	}
}

// releaseCodeNavigation drops a document's need of its servers, and forgets
// the outside paths its answers named.
func (d *Daemon) releaseCodeNavigation(document string) {
	d.codeNav.Release(document)
	d.codeNavOutside.forget(document)
}

// warmCodeNavigation starts the servers a review needs before anyone shows it,
// so the first question does not wait for the load.
func (d *Daemon) warmCodeNavigation(document string) {
	d.life.Go("warmCodeNavigation", func() {
		content, err := readMarkdownFile(document)
		if err != nil {
			return
		}
		bundle, err := d.resolveReview(d.life.Context(), document, content)
		if err != nil || bundle.Repository == "" {
			return
		}
		d.codeNav.Need("", bundle.Repository, codeNavFiles(bundle))
	})
}

// codeNavOutside records the paths outside a review's repository that a
// navigation answer named, the only outside files the file view may read.
type codeNavOutside struct {
	mu    sync.Mutex
	paths map[string]map[string]bool
}

func (o *codeNavOutside) allow(document, path string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.paths == nil {
		o.paths = map[string]map[string]bool{}
	}
	if o.paths[document] == nil {
		o.paths[document] = map[string]bool{}
	}
	o.paths[document][path] = true
}

func (o *codeNavOutside) allowed(document, path string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.paths[document][path]
}

func (o *codeNavOutside) forget(document string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.paths, document)
}

func (d *Daemon) answerCodeNav(ctx context.Context, msg protocol.CodeNavMessage) (protocol.CodeNavResultMessage, error) {
	content, err := d.reviewDocumentHere(msg.Document)
	if err != nil {
		return protocol.CodeNavResultMessage{}, err
	}
	document := filepath.Clean(msg.Document)
	repository, ok := review.Repository(content)
	if !ok {
		return protocol.CodeNavResultMessage{}, fmt.Errorf("the review names no absolute repository in its frontmatter. Run `attn review start <document>` from the repository under review")
	}
	path, err := d.codeNavPath(document, repository, msg.Path)
	if err != nil {
		return protocol.CodeNavResultMessage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, codeNavRequestTripwire)
	defer cancel()
	needer := ""
	if d.tileDocuments.version(document) > 0 {
		needer = document
	}
	answer, err := d.codeNav.Ask(ctx, needer, repository, path, int(msg.Line), int(msg.Character), msg.Kind)
	if needer != "" {
		d.releaseUnlessShown(needer)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%s did not answer within %s (codeNavRequestTripwire); `attn nav status` shows what it is doing", answer.Program, codeNavRequestTripwire)
	}
	if err != nil {
		return protocol.CodeNavResultMessage{}, err
	}
	result := protocol.CodeNavResultMessage{Event: protocol.EventCodeNavResult, Success: true, Language: protocol.Ptr(answer.Language), Text: optional(answer.Text)}
	if msg.Kind != codenav.KindHover {
		result.Locations = codeNavLocations(repository, answer.Locations)
		for _, location := range result.Locations {
			if location.Outside {
				d.codeNavOutside.allow(document, location.Path)
			}
		}
	}
	return result, nil
}

// codeNavPath resolves a path the reader asks about: a file of the review's
// repository, or an outside file an earlier answer named.
func (d *Daemon) codeNavPath(document, repository, path string) (string, error) {
	if filepath.IsAbs(path) {
		if !d.codeNavOutside.allowed(document, filepath.Clean(path)) {
			return "", fmt.Errorf("%s is outside the review's repository %s; the reader opens an outside file only after a definition or use named it", path, repository)
		}
		return filepath.Clean(path), nil
	}
	if !filepath.IsLocal(filepath.FromSlash(path)) {
		return "", fmt.Errorf("%s is not a path inside the review's repository %s", path, repository)
	}
	return filepath.Join(repository, filepath.FromSlash(path)), nil
}

// codeNavLocations are the answer's places, with paths inside the repository
// made relative and each line's text read for a list of uses.
func codeNavLocations(repository string, locations []codenav.Location) []protocol.CodeNavLocation {
	files := map[string][]string{}
	wire := make([]protocol.CodeNavLocation, 0, len(locations))
	for _, l := range locations {
		lines, read := files[l.Path]
		if !read {
			if content, err := os.ReadFile(l.Path); err == nil && len(content) <= review.MaxStopFileBytes {
				lines = strings.Split(string(content), "\n")
			}
			files[l.Path] = lines
		}
		location := protocol.CodeNavLocation{Path: l.Path, Line: l.Line, Character: l.Character, EndLine: l.EndLine, EndCharacter: l.EndChar}
		if rel, err := filepath.Rel(repository, l.Path); err == nil && filepath.IsLocal(rel) {
			location.Path = filepath.ToSlash(rel)
		} else {
			location.Outside = true
		}
		if l.Line >= 1 && l.Line <= len(lines) {
			location.Text = strings.TrimRight(lines[l.Line-1], "\r")
		}
		wire = append(wire, location)
	}
	return wire
}

func (d *Daemon) handleCodeNavWS(client *wsClient, msg *protocol.CodeNavMessage) {
	d.life.Go("handleCodeNavWS", func() {
		result, err := Call[protocol.CodeNavResultMessage](d.life.Context(), d.router(), *msg)
		if err != nil {
			result = protocol.CodeNavResultMessage{Event: protocol.EventCodeNavResult, Error: protocol.Ptr(d.tileDocumentError(protocol.Deref(msg.Host), err).Error())}
		}
		result.RequestID = msg.RequestID
		d.sendToClient(client, &result)
	})
}

func codeNavOff() string {
	return fmt.Sprintf("attn nav is off: code navigation is part of experimental code review. Turn it on with `attn settings set %s true`.", setting.CodeReviewEnabled.Key)
}

// repositoryRoot is the top of the repository that holds dir.
func (d *Daemon) repositoryRoot(ctx context.Context, dir string) (string, error) {
	return gitValue(ctx, d.gitExecution(), reviewTask, func(ctx context.Context, client *attngit.Client) (string, error) {
		return client.GetRepoRoot(ctx, dir)
	})
}

// identifiers are the names on a line, each with its UTF-16 offset.
func identifiers(line string) []struct {
	name      string
	character int
} {
	var found []struct {
		name      string
		character int
	}
	runes := []rune(line)
	units := 0
	for i := 0; i < len(runes); {
		r := runes[i]
		if unicode.IsLetter(r) || r == '_' {
			start, startUnits := i, units
			for i < len(runes) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i]) || runes[i] == '_') {
				units += len(utf16.Encode([]rune{runes[i]}))
				i++
			}
			found = append(found, struct {
				name      string
				character int
			}{string(runes[start:i]), startUnits})
			continue
		}
		units += len(utf16.Encode([]rune{r}))
		i++
	}
	return found
}

func (d *Daemon) handleCodeNavCheck(ctx context.Context, conn net.Conn, msg *protocol.CodeNavCheckMessage) {
	result, err := d.checkCodeNav(ctx, msg)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("nav check: %v", err))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, CodeNavCheckResult: &result})
}

// checkCodeNav asks for the definition of each name on the line in turn and
// answers with the first that resolves.
func (d *Daemon) checkCodeNav(ctx context.Context, msg *protocol.CodeNavCheckMessage) (protocol.CodeNavCheckResult, error) {
	if !d.on(setting.CodeReviewEnabled) {
		return protocol.CodeNavCheckResult{}, errors.New(codeNavOff())
	}
	repository, err := d.repositoryRoot(ctx, msg.Repository)
	if err != nil {
		return protocol.CodeNavCheckResult{}, err
	}
	path := filepath.Join(repository, filepath.FromSlash(msg.Path))
	content, err := os.ReadFile(path)
	if err != nil {
		return protocol.CodeNavCheckResult{}, err
	}
	lines := strings.Split(string(content), "\n")
	if msg.Line < 1 || int(msg.Line) > len(lines) {
		return protocol.CodeNavCheckResult{}, fmt.Errorf("%s has %d lines; ask about a line from 1 to %d", msg.Path, len(lines), len(lines))
	}
	result := protocol.CodeNavCheckResult{Tried: []string{}}
	ctx, cancel := context.WithTimeout(ctx, codeNavRequestTripwire)
	defer cancel()
	started := time.Now()
	for _, name := range identifiers(lines[msg.Line-1]) {
		result.Tried = append(result.Tried, name.name)
		answer, err := d.codeNav.Ask(ctx, "", repository, path, int(msg.Line), name.character, codenav.KindDefinition)
		result.Language, result.Program = answer.Language, answer.Program
		if errors.Is(err, context.DeadlineExceeded) {
			return result, fmt.Errorf("%s did not answer within %s (codeNavRequestTripwire)", answer.Program, codeNavRequestTripwire)
		}
		var noLanguage codenav.NoLanguage
		var missing codenav.Missing
		if errors.As(err, &noLanguage) || errors.As(err, &missing) {
			return result, err
		}
		if err != nil {
			return result, fmt.Errorf("%s: %w", answer.Program, err)
		}
		if len(answer.Locations) > 0 {
			location := codeNavLocations(repository, answer.Locations[:1])[0]
			result.Name, result.Location = protocol.Ptr(name.name), &location
			break
		}
	}
	result.ElapsedMs = int(time.Since(started).Milliseconds())
	if result.Location == nil {
		return result, fmt.Errorf("no name on %s:%d has a definition the server could find (asked about %s)", msg.Path, msg.Line, strings.Join(result.Tried, ", "))
	}
	return result, nil
}

func (d *Daemon) handleCodeNavStatus(ctx context.Context, conn net.Conn, msg *protocol.CodeNavStatusMessage) {
	if !d.on(setting.CodeReviewEnabled) {
		d.sendError(conn, codeNavOff())
		return
	}
	result := protocol.CodeNavStatusResult{BinDir: d.codeNavDir("bin"), Languages: []protocol.CodeNavLanguage{}, Servers: []protocol.CodeNavServer{}}
	if repository, err := d.repositoryRoot(ctx, protocol.Deref(msg.Repository)); msg.Repository != nil && err == nil {
		origin := d.repositoryOrigin(reviewTask, repository)
		result.Origin = optional(origin)
		set := d.codeNavSetDefinitions(origin)
		for language, def := range d.codeNavDefinitions(repository) {
			entry := protocol.CodeNavLanguage{Language: language, Source: "built-in"}
			if value, ok := set[language]; ok {
				entry.Source, entry.SettingKey, entry.Definition = "setting", protocol.Ptr(codeNavSettingKey(origin, language)), value
			} else {
				encoded, _ := json.Marshal(def)
				entry.Definition = string(encoded)
			}
			if program, err := codenav.Locate(result.BinDir, def.Command[0]); err != nil {
				entry.Missing = protocol.Ptr(err.Error())
			} else {
				entry.Program = protocol.Ptr(program)
			}
			result.Languages = append(result.Languages, entry)
		}
		sort.Slice(result.Languages, func(i, j int) bool { return result.Languages[i].Language < result.Languages[j].Language })
	}
	for _, running := range d.codeNav.Running() {
		server := protocol.CodeNavServer{Language: running.Key.Language, Folder: running.Key.Folder, Program: running.Program, Pid: running.PID, State: running.State, Detail: optional(running.Detail), NeededBy: running.Needers}
		if running.PID > 0 {
			if process, ok := diag.ReadProcess(running.PID); ok {
				server.RssBytes = protocol.Ptr(int(process.ResidentBytes))
			}
		}
		result.Servers = append(result.Servers, server)
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, CodeNavStatusResult: &result})
}

func (d *Daemon) handleCodeNavLink(conn net.Conn, msg *protocol.CodeNavLinkMessage) {
	if !d.on(setting.CodeReviewEnabled) {
		d.sendError(conn, codeNavOff())
		return
	}
	target := filepath.Clean(msg.Target)
	info, err := os.Stat(target)
	switch {
	case !filepath.IsAbs(target):
		err = fmt.Errorf("%s is not an absolute path", msg.Target)
	case err == nil && (info.IsDir() || info.Mode().Perm()&0o111 == 0):
		err = fmt.Errorf("%s is not an executable file", target)
	}
	if err != nil {
		d.sendError(conn, fmt.Sprintf("nav link: %v", err))
		return
	}
	name := protocol.Deref(msg.Name)
	if name == "" {
		name = filepath.Base(target)
	}
	if !filepath.IsLocal(name) || strings.ContainsRune(name, filepath.Separator) {
		d.sendError(conn, fmt.Sprintf("nav link: %q is not a program name", name))
		return
	}
	link := d.codeNavDir("bin", name)
	err = os.MkdirAll(filepath.Dir(link), 0o755)
	if err == nil {
		_ = os.Remove(link)
		err = os.Symlink(target, link)
	}
	if err != nil {
		d.sendError(conn, fmt.Sprintf("nav link: %v", err))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, CodeNavLinkResult: &protocol.CodeNavLinkResult{Name: name, Path: link, Target: protocol.Ptr(target)}})
}

func (d *Daemon) handleCodeNavUnlink(conn net.Conn, msg *protocol.CodeNavUnlinkMessage) {
	if !d.on(setting.CodeReviewEnabled) {
		d.sendError(conn, codeNavOff())
		return
	}
	if !filepath.IsLocal(msg.Name) || strings.ContainsRune(msg.Name, filepath.Separator) {
		d.sendError(conn, fmt.Sprintf("nav unlink: %q is not a program name", msg.Name))
		return
	}
	link := d.codeNavDir("bin", msg.Name)
	if err := os.Remove(link); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = fmt.Errorf("nothing is linked as %s in %s; `attn nav status` lists what is", msg.Name, d.codeNavDir("bin"))
		}
		d.sendError(conn, fmt.Sprintf("nav unlink: %v", err))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, CodeNavLinkResult: &protocol.CodeNavLinkResult{Name: msg.Name, Path: link}})
}

func (d *Daemon) handleCodeNavDefine(ctx context.Context, conn net.Conn, msg *protocol.CodeNavDefineMessage) {
	result, err := d.defineCodeNav(ctx, msg)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("nav set: %v", err))
		return
	}
	_ = json.NewEncoder(conn).Encode(protocol.Response{Ok: true, CodeNavDefineResult: &result})
}

func (d *Daemon) defineCodeNav(ctx context.Context, msg *protocol.CodeNavDefineMessage) (protocol.CodeNavDefineResult, error) {
	if !d.on(setting.CodeReviewEnabled) {
		return protocol.CodeNavDefineResult{}, errors.New(codeNavOff())
	}
	language := strings.TrimSpace(msg.Language)
	if language == "" || strings.ContainsFunc(language, func(r rune) bool { return !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '-' }) {
		return protocol.CodeNavDefineResult{}, fmt.Errorf("language %q must be lowercase letters, digits and dashes, such as java", msg.Language)
	}
	repository, err := d.repositoryRoot(ctx, msg.Repository)
	if err != nil {
		return protocol.CodeNavDefineResult{}, err
	}
	origin := d.repositoryOrigin(reviewTask, repository)
	if origin == "" {
		return protocol.CodeNavDefineResult{}, fmt.Errorf("%s has no origin remote; a server definition is keyed by the origin, so outposts with a clone find it", repository)
	}
	key := codeNavSettingKey(origin, language)
	value := strings.TrimSpace(protocol.Deref(msg.Definition))
	if value != "" {
		def, err := codenav.ParseDefinition(value)
		if err != nil {
			return protocol.CodeNavDefineResult{}, err
		}
		encoded, _ := json.Marshal(def)
		value = string(encoded)
	} else if _, ok := d.codeNavSetDefinitions(origin)[language]; !ok {
		return protocol.CodeNavDefineResult{}, fmt.Errorf("no definition of %s is set for %s; `attn nav status` lists them", language, origin)
	}
	if _, err := d.setSetting(&protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: key, Value: value}); err != nil {
		return protocol.CodeNavDefineResult{}, err
	}
	return protocol.CodeNavDefineResult{Key: key, Origin: origin, Definition: optional(value)}, nil
}

func validateCodeNavDefinition(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	_, err := codenav.ParseDefinition(value)
	return err
}

func validateCodeNavIdleStop(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return validateBoundedIntSetting("code navigation idle stop", value, 0, codeNavIdleStopMaxSeconds)
}
