// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.
package appbuild

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/docstore"
)

const (
	APIVersion = 1

	ManifestName = "attn-app.toml"
)

// Manifest is `attn-app.toml`, parsed. The JSON tags are load-bearing: the
// marshalled form is the declaration snapshot frozen into the version row.
type Manifest struct {
	Name        string       `toml:"name" json:"name"`
	Description string       `toml:"description" json:"description,omitempty"`
	AttnAppAPI  int          `toml:"attn_app_api" json:"attn_app_api"`
	Entrypoint  string       `toml:"entrypoint" json:"entrypoint"`
	Reconcile   bool         `toml:"reconcile" json:"reconcile,omitempty"`
	Subscribe   []Subscribe  `toml:"subscribe" json:"subscribe,omitempty"`
	Collections []Collection `toml:"collections" json:"collections,omitempty"`
	Views       []View       `toml:"views" json:"views,omitempty"`
	Commands    []Command    `toml:"commands" json:"commands,omitempty"`
}

type Command struct {
	Name        string `toml:"name" json:"name"`
	Description string `toml:"description" json:"description,omitempty"`
}

type Subscribe struct {
	Events []string `toml:"events" json:"events"`
}

type Collection struct {
	Name   string   `toml:"name" json:"name"`
	Fields []string `toml:"fields" json:"fields,omitempty"`
}

type View struct {
	Name       string      `toml:"name" json:"name"`
	Kind       string      `toml:"kind" json:"kind"`
	Title      string      `toml:"title" json:"title"`
	Entrypoint string      `toml:"entrypoint" json:"entrypoint"`
	Params     *ViewParams `toml:"params" json:"params,omitempty"`
}

type ViewParams struct {
	Label       string `toml:"label" json:"label"`
	Placeholder string `toml:"placeholder" json:"placeholder,omitempty"`
}

const ViewKindTile = "tile"

var viewKinds = []string{ViewKindTile}

var knownTables = []string{"subscribe", "collections", "views", "commands"}

func LoadManifest(dir string) (Manifest, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, fmt.Errorf("%s has no %s, so it is not an app directory; `attn app new %s` scaffolds one", dir, ManifestName, dir)
		}
		return Manifest{}, fmt.Errorf("reading %s: %w", path, err)
	}
	m, err := ParseManifest(string(data))
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.checkEntrypoint(dir); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func ParseManifest(text string) (Manifest, error) {
	var m Manifest
	md, err := toml.Decode(text, &m)
	if err != nil {
		return Manifest{}, fmt.Errorf("not valid TOML: %w", err)
	}
	if err := refuseUnknownKeys(md); err != nil {
		return Manifest{}, err
	}

	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	m.Entrypoint = strings.TrimSpace(m.Entrypoint)

	// The api gate runs before the rest: newer syntax reads as an error here, and
	// "unknown table [tiles]" is a worse answer than "this app wants app api 2".
	if err := m.checkAPIVersion(); err != nil {
		return Manifest{}, err
	}
	if err := apps.ValidateName(m.Name); err != nil {
		return Manifest{}, err
	}
	if m.Entrypoint == "" {
		return Manifest{}, fmt.Errorf("entrypoint is required, as a path relative to the app directory (for example entrypoint = \"src/index.ts\")")
	}
	if filepath.IsAbs(m.Entrypoint) {
		return Manifest{}, fmt.Errorf("entrypoint %q must be relative to the app directory", m.Entrypoint)
	}
	if err := m.checkSubscriptions(); err != nil {
		return Manifest{}, err
	}
	if err := m.checkViews(); err != nil {
		return Manifest{}, err
	}
	if err := m.checkCommands(); err != nil {
		return Manifest{}, err
	}
	if err := m.checkSomethingRuns(); err != nil {
		return Manifest{}, err
	}
	if err := m.checkCollections(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func refuseUnknownKeys(md toml.MetaData) error {
	undecoded := md.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var keys []string
	for _, key := range undecoded {
		full := key.String()
		if seen[full] {
			continue
		}
		seen[full] = true
		keys = append(keys, full)
	}
	sort.Strings(keys)
	subject, verb := "this", "it"
	if len(keys) > 1 {
		subject, verb = "these", "they"
	}
	return fmt.Errorf("declares %s, which this attn does not understand (attn_app_api %d supports the tables %s, and the top-level keys name, description, attn_app_api, entrypoint, reconcile). "+
		"An app must not half-load: %s ignored, %s would leave the app declaring behavior nothing provides",
		strings.Join(quoteAll(keys), ", "), APIVersion, strings.Join(knownTables, ", "), subject, verb)
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

func (m Manifest) checkAPIVersion() error {
	switch {
	case m.AttnAppAPI == 0:
		return fmt.Errorf("attn_app_api is required; this attn speaks app api %d, so set attn_app_api = %d", APIVersion, APIVersion)
	case m.AttnAppAPI < 0:
		return fmt.Errorf("attn_app_api %d is not a version; this attn speaks app api %d", m.AttnAppAPI, APIVersion)
	case m.AttnAppAPI > APIVersion:
		return fmt.Errorf("needs app api %d but this attn speaks app api %d, so it would run against a runtime missing what it declares; upgrade attn, or lower attn_app_api to %d and drop what it added",
			m.AttnAppAPI, APIVersion, APIVersion)
	}
	return nil
}

// checkSubscriptions validates the event patterns and refuses a duplicate: every pattern is
// one key of the generated `Handlers` type, so two blocks would collapse into one slot.
func (m Manifest) checkSubscriptions() error {
	seen := map[string]bool{}
	for _, block := range m.Subscribe {
		for _, raw := range block.Events {
			pattern := strings.TrimSpace(raw)
			if err := validateEventPattern(pattern); err != nil {
				return err
			}
			if seen[pattern] {
				return fmt.Errorf("subscribes to %q twice; each event pattern binds one handler, so list it once", pattern)
			}
			seen[pattern] = true
		}
	}
	return nil
}

func (m Manifest) checkSomethingRuns() error {
	if len(m.EventPatterns()) > 0 || len(m.Views) > 0 {
		return nil
	}
	if len(m.Commands) > 0 {
		return fmt.Errorf("declares commands but no view, and a command is invoked from one of this app's own views; add a [[views]] block naming the component that calls it, or a [[subscribe]] block if the app is meant to run on events instead")
	}
	return fmt.Errorf("declares neither a subscription nor a view, so nothing would ever run it; add a [[subscribe]] block with events = [\"session.state.changed\"] (patterns end in .* to match a family), or a [[views]] block naming a component to render")
}

func validateEventPattern(pattern string) error {
	switch {
	case pattern == "":
		return fmt.Errorf("subscribes to an empty event pattern; use a fact name (session.state.changed) or a family (session.*)")
	case pattern == "*":
		return fmt.Errorf("subscribes to %q, which is every fact attn publishes; name the families you handle instead (for example session.*, ticket.*)", pattern)
	}
	body := strings.TrimSuffix(pattern, ".*")
	if body == "" {
		return fmt.Errorf("subscribes to %q, which has no family before the wildcard", pattern)
	}
	for _, segment := range strings.Split(body, ".") {
		if segment == "" {
			return fmt.Errorf("subscribes to %q, which has an empty segment; patterns are dotted names like session.state.changed or session.*", pattern)
		}
		if strings.Contains(segment, "*") {
			return fmt.Errorf("subscribes to %q; a wildcard is only valid as a trailing .* (session.* matches session.state.changed)", pattern)
		}
	}
	return nil
}

func (m *Manifest) checkViews() error {
	seen := map[string]bool{}
	for i := range m.Views {
		v := &m.Views[i]
		v.Name = strings.TrimSpace(v.Name)
		v.Kind = strings.TrimSpace(v.Kind)
		v.Title = strings.TrimSpace(v.Title)
		v.Entrypoint = strings.TrimSpace(v.Entrypoint)

		if err := apps.ValidateViewName(v.Name); err != nil {
			return err
		}
		if seen[v.Name] {
			return fmt.Errorf("declares view %q twice; a view name addresses one component, so name each one once", v.Name)
		}
		seen[v.Name] = true
		if v.Kind == "" {
			v.Kind = ViewKindTile
		}
		if v.Kind != ViewKindTile {
			return fmt.Errorf("view %q is of kind %q, which this attn cannot mount anywhere; it mounts %s",
				v.Name, v.Kind, strings.Join(quoteAll(viewKinds), ", "))
		}
		if v.Title == "" {
			return fmt.Errorf("view %q has no title, and the title is what the tile header and the dock picker show; add title = \"Pending approvals\"", v.Name)
		}
		if v.Entrypoint == "" {
			return fmt.Errorf("view %q has no entrypoint; add one as a path relative to the app directory (for example entrypoint = \"src/views/%s.tsx\")", v.Name, v.Name)
		}
		if filepath.IsAbs(v.Entrypoint) {
			return fmt.Errorf("view %q has entrypoint %q, which must be relative to the app directory", v.Name, v.Entrypoint)
		}
		if v.Params != nil {
			v.Params.Label = strings.TrimSpace(v.Params.Label)
			v.Params.Placeholder = strings.TrimSpace(v.Params.Placeholder)
			if v.Params.Label == "" {
				return fmt.Errorf("view %q declares params with no label, and the label is what the dock picker puts on the field it asks for; add label = \"Repository\", or drop params to take none", v.Name)
			}
		}
	}
	return nil
}

func (m *Manifest) checkCommands() error {
	seen := map[string]bool{}
	for i := range m.Commands {
		c := &m.Commands[i]
		c.Name = strings.TrimSpace(c.Name)
		c.Description = strings.TrimSpace(c.Description)
		if err := apps.ValidateCommandName(c.Name); err != nil {
			return err
		}
		if seen[c.Name] {
			return fmt.Errorf("declares command %q twice; a command name binds one handler, so name each one once", c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

func (m Manifest) checkCollections() error {
	seen := map[string]bool{}
	for _, c := range m.Collections {
		name := strings.TrimSpace(c.Name)
		if seen[name] {
			return fmt.Errorf("declares collection %q twice", name)
		}
		seen[name] = true
		schema := docstore.CollectionSchema{
			Namespace:  apps.Namespace(m.Name),
			Collection: name,
			Fields:     make([]docstore.FieldSpec, 0, len(c.Fields)),
		}
		for _, f := range c.Fields {
			schema.Fields = append(schema.Fields, docstore.FieldSpec{
				Name: strings.TrimSpace(f), Type: docstore.FieldString,
			})
		}
		if err := schema.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (m Manifest) checkEntrypoint(dir string) error {
	if err := checkEntrypointFile(dir, m.Entrypoint, "entrypoint"); err != nil {
		return err
	}
	for _, v := range m.Views {
		if err := checkEntrypointFile(dir, v.Entrypoint, fmt.Sprintf("view %q's entrypoint", v.Name)); err != nil {
			return err
		}
	}
	return nil
}

func checkEntrypointFile(dir, entrypoint, what string) error {
	abs := filepath.Clean(filepath.Join(dir, entrypoint))
	root := filepath.Clean(dir)
	if abs != root && !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
		return fmt.Errorf("%s %q points outside the app directory", what, entrypoint)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s %q does not exist (looked for %s)", what, entrypoint, abs)
		}
		return fmt.Errorf("%s %q: %w", what, entrypoint, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s %q is not a regular file", what, entrypoint)
	}
	return nil
}

func (m Manifest) EventPatterns() []string {
	var out []string
	for _, block := range m.Subscribe {
		for _, e := range block.Events {
			out = append(out, strings.TrimSpace(e))
		}
	}
	return out
}

func (m Manifest) ViewNames() []string {
	out := make([]string, 0, len(m.Views))
	for _, v := range m.Views {
		out = append(out, v.Name)
	}
	return out
}

func (m Manifest) CommandNames() []string {
	out := make([]string, 0, len(m.Commands))
	for _, c := range m.Commands {
		out = append(out, c.Name)
	}
	return out
}

// DeclaredCommands reads the command names back out of a frozen declaration — the serving
// version's contract. Every name is validated: it arrived over the wire and becomes a key.
func DeclaredCommands(declaration string) ([]string, error) {
	var snapshot struct {
		Commands []Command `json:"commands"`
	}
	if err := json.Unmarshal([]byte(declaration), &snapshot); err != nil {
		return nil, fmt.Errorf("reading the commands of a declaration snapshot: %w", err)
	}
	out := make([]string, 0, len(snapshot.Commands))
	for _, c := range snapshot.Commands {
		if err := apps.ValidateCommandName(c.Name); err != nil {
			return nil, err
		}
		out = append(out, c.Name)
	}
	return out, nil
}

// DeclaredViews reads the views back out of a frozen declaration. Trust boundary: a view
// name becomes a path segment of the bundle URL and of the `app:<app>/<view>` tile kind.
func DeclaredViews(declaration string) ([]View, error) {
	var snapshot struct {
		Views []View `json:"views"`
	}
	if err := json.Unmarshal([]byte(declaration), &snapshot); err != nil {
		return nil, fmt.Errorf("reading the views of a declaration snapshot: %w", err)
	}
	for _, v := range snapshot.Views {
		if err := apps.ValidateViewName(v.Name); err != nil {
			return nil, err
		}
	}
	return snapshot.Views, nil
}

func DeclaredViewNames(declaration string) ([]string, error) {
	views, err := DeclaredViews(declaration)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.Name)
	}
	return out, nil
}

func (m Manifest) Declaration() (string, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encoding the declaration of app %q: %w", m.Name, err)
	}
	return string(data), nil
}
