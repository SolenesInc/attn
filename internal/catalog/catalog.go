package catalog

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

//go:embed content
var content embed.FS

const (
	TypesFile  = "types.yaml"
	ServerFile = "server.yaml"

	MaxInstructionsChars = 4096
)

type Runs string

const (
	RunsHome Runs = "home"
	RunsHere Runs = "here"
)

var gates = []string{"any", "own", "full"}

var errorCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var grantNames = []string{"read_repo", "push_repo", "push_workflows", "push_default_branch", "use_host", "net_read", "net_write", "steer", "approve"}

type Catalog struct {
	Server   Server
	Types    map[string]Type
	Groups   []Group
	Commands []Command
}

type Server struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Intro       string `yaml:"intro"`
}

type Type struct {
	Schema      map[string]any `yaml:"json"`
	Doc         string         `yaml:"doc"`
	Placeholder string         `yaml:"placeholder"`
	Clears      bool           `yaml:"clears"`
}

type Group struct {
	Name      string     `yaml:"name"`
	Summary   string     `yaml:"summary"`
	Usage     string     `yaml:"usage"`
	Doc       string     `yaml:"doc"`
	Flags     []Flag     `yaml:"flags"`
	Sections  []Section  `yaml:"sections"`
	Reference *Reference `yaml:"reference"`
	Map       *FamilyMap `yaml:"map"`
	File      string     `yaml:"-"`
}

type FamilyMap struct {
	Rows  []MapRow `yaml:"rows"`
	Notes string   `yaml:"notes"`
}

type MapRow struct {
	Need  string   `yaml:"need"`
	Tools []string `yaml:"tools"`
}

type Flag struct {
	Name        string `yaml:"name"`
	Placeholder string `yaml:"placeholder"`
	Doc         string `yaml:"doc"`
}

type Section struct {
	Title    string   `yaml:"title"`
	Text     string   `yaml:"text"`
	Commands []string `yaml:"commands"`
}

type Reference struct {
	Name  string `yaml:"name"`
	Title string `yaml:"title"`
	Intro string `yaml:"intro"`
}

type Command struct {
	Name              string           `yaml:"name"`
	Summary           string           `yaml:"summary"`
	Core              bool             `yaml:"core"`
	Runs              Runs             `yaml:"runs"`
	Grant             string           `yaml:"grant"`
	MayNeed           []string         `yaml:"may_need"`
	Doc               string           `yaml:"doc"`
	Args              []Arg            `yaml:"args"`
	ReadOnly          bool             `yaml:"read_only"`
	Anyone            bool             `yaml:"anyone"`
	RunsWithoutDaemon bool             `yaml:"runs_without_daemon"`
	Result            string           `yaml:"result"`
	Errors            []Error          `yaml:"errors"`
	Examples          []map[string]any `yaml:"examples"`
	Offered           *bool            `yaml:"offered"`
	Reason            string           `yaml:"reason"`
	Usage             string           `yaml:"usage"`
	Hidden            bool             `yaml:"hidden"`
	Map               *FamilyMap       `yaml:"map"`
	File              string           `yaml:"-"`
}

type Error struct {
	Code  string `yaml:"code"`
	Fix   string `yaml:"fix"`
	Usage bool   `yaml:"usage"`
}

type Arg struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required"`
	List        bool     `yaml:"list"`
	CLI         string   `yaml:"cli"`
	Stdin       bool     `yaml:"stdin"`
	File        bool     `yaml:"file"`
	Local       Local    `yaml:"local"`
	LocalWhen   string   `yaml:"local_when"`
	CLIOnly     bool     `yaml:"cli_only"`
	Values      []string `yaml:"values"`
	OneOf       string   `yaml:"one_of"`
	Unless      string   `yaml:"unless"`
	MaxLength   int      `yaml:"maxLength"`
	Placeholder string   `yaml:"placeholder"`
	Doc         string   `yaml:"doc"`
}

const CLIInternal = "internal"

func (a Arg) Internal() bool { return a.CLI == CLIInternal }

type Local string

const (
	LocalPath        Local = "path"
	LocalDestination Local = "destination"
	LocalEvidence    Local = "evidence"
	LocalCommit      Local = "commit"
)

var locals = []Local{LocalPath, LocalDestination, LocalEvidence, LocalCommit}

type familyFile struct {
	Groups   []Group   `yaml:"groups"`
	Commands []Command `yaml:"commands"`
}

type typesFile struct {
	Types map[string]Type `yaml:"types"`
}

type serverFile struct {
	Server Server `yaml:"server"`
}

func Builtin() (*Catalog, error) {
	sub, err := fs.Sub(content, "content")
	if err != nil {
		return nil, err
	}
	return Load(sub)
}

func Load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{}
	var server serverFile
	if err := decode(fsys, ServerFile, &server); err != nil {
		return nil, err
	}
	c.Server = server.Server
	var types typesFile
	if err := decode(fsys, TypesFile, &types); err != nil {
		return nil, err
	}
	c.Types = types.Types
	names, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if name == TypesFile || name == ServerFile {
			continue
		}
		var family familyFile
		if err := decode(fsys, name, &family); err != nil {
			return nil, err
		}
		for i := range family.Groups {
			family.Groups[i].File = name
		}
		for i := range family.Commands {
			family.Commands[i].File = name
		}
		c.Groups = append(c.Groups, family.Groups...)
		c.Commands = append(c.Commands, family.Commands...)
	}
	if problems := c.check(); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	return c, nil
}

func decode(fsys fs.FS, name string, into any) error {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (c *Catalog) Command(name string) (*Command, bool) {
	for i := range c.Commands {
		if c.Commands[i].Name == name {
			return &c.Commands[i], true
		}
	}
	return nil, false
}

func (c *Catalog) Group(name string) (*Group, bool) {
	for i := range c.Groups {
		if c.Groups[i].Name == name {
			return &c.Groups[i], true
		}
	}
	return nil, false
}

func (c *Catalog) LayerFamilies() []string {
	var out []string
	for _, top := range c.TopLevel() {
		if slices.ContainsFunc(c.FamilyCommands(top.Name), (*Command).ThroughLayer) {
			out = append(out, top.Name)
		}
	}
	return out
}

func (c *Catalog) FamilyCommands(family string) []*Command {
	var out []*Command
	if bare, ok := c.Command(family); ok {
		out = append(out, bare)
	}
	return append(out, c.GroupCommands(family)...)
}

func (c *Catalog) GroupCommands(group string) []*Command {
	var out []*Command
	for i := range c.Commands {
		if strings.HasPrefix(c.Commands[i].Name, group+" ") {
			out = append(out, &c.Commands[i])
		}
	}
	return out
}

func (c *Command) ToolName() string {
	return strings.NewReplacer(" ", "_", "-", "_").Replace(c.Name)
}

func (c *Command) IsOffered() bool { return c.Offered == nil || *c.Offered }

func (c *Command) ThroughLayer() bool { return c.Runs != "" }

func (c *Command) IsTool() bool { return c.ThroughLayer() && c.IsOffered() }

type TopLevel struct {
	Name    string
	Summary string
	Hidden  bool
	Group   *Group
	Command *Command
}

func (c *Catalog) TopLevel() []TopLevel {
	var out []TopLevel
	for i := range c.Groups {
		if g := &c.Groups[i]; !strings.Contains(g.Name, " ") {
			out = append(out, TopLevel{Name: g.Name, Summary: g.Summary, Group: g})
		}
	}
	for i := range c.Commands {
		cmd := &c.Commands[i]
		if strings.Contains(cmd.Name, " ") {
			continue
		}
		if _, grouped := c.Group(cmd.Name); grouped {
			continue
		}
		out = append(out, TopLevel{Name: cmd.Name, Summary: cmd.Summary, Hidden: cmd.Hidden, Command: cmd})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (c *Catalog) ArgType(a Arg) Type { return c.Types[a.Type] }

func (c *Catalog) check() []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if n := utf8.RuneCountInString(c.Instructions()); n > MaxInstructionsChars {
		add("%s: server instructions are %d characters, over the limit of %d that Claude Code keeps; cut the intent map (%s)", ServerFile, n, MaxInstructionsChars, c.mapShares())
	}
	if strings.TrimSpace(c.Server.Intro) == "" {
		add("%s: server intro is empty", ServerFile)
	}
	if strings.TrimSpace(c.Server.Description) == "" || strings.Contains(strings.TrimSpace(c.Server.Description), "\n") {
		add("%s: server description must be one line", ServerFile)
	}

	typeNames := make([]string, 0, len(c.Types))
	for name := range c.Types {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)
	for _, name := range typeNames {
		t := c.Types[name]
		if t.Schema == nil {
			add("%s: type %q has no json schema", TypesFile, name)
			continue
		}
		for _, problem := range schemaProblems(t.Schema, "") {
			add("%s: type %q, used by %s: %s", TypesFile, name, c.commandsUsing(name), problem)
		}
	}

	seen := map[string]bool{}
	for i := range c.Commands {
		cmd := &c.Commands[i]
		where := fmt.Sprintf("%s: %s", cmd.File, cmd.Name)
		if cmd.Name == "" {
			add("%s: a command has no name", cmd.File)
			continue
		}
		if seen[cmd.Name] {
			add("%s: defined twice", where)
		}
		seen[cmd.Name] = true
		top := !strings.Contains(cmd.Name, " ")
		if family, _, multiword := strings.Cut(cmd.Name, " "); multiword {
			if _, ok := c.Group(family); !ok {
				add("%s: no group is named %q; add the group for the family", where, family)
			}
		}
		switch cmd.Runs {
		case RunsHome, RunsHere:
		case "":
			if cmd.IsOffered() || !top {
				add("%s: runs is missing; name where the command runs: home or here", where)
			}
			if len(cmd.Args) > 0 || cmd.Result != "" || len(cmd.Errors) > 0 || cmd.Grant != "" {
				add("%s: args, result, errors and grant belong to a command the command layer runs; name where it runs", where)
			}
		default:
			add("%s: runs is %q; accepted values are home and here", where, cmd.Runs)
		}
		if cmd.Usage != "" && cmd.ThroughLayer() {
			add("%s: usage is generated from the args of a command the command layer runs; remove it", where)
		}
		if _, grouped := c.Group(cmd.Name); cmd.Map != nil && (!top || grouped) {
			add("%s: map belongs to the family: put it on the group, or on the command only when the command is the whole family", where)
		}
		if cmd.Hidden && cmd.IsOffered() {
			add("%s: hidden is for a command that is not offered", where)
		}
		switch {
		case !cmd.ThroughLayer():
		case cmd.Grant == "":
			add("%s: grant is missing; name the grant the command needs: %s", where, strings.Join(slices.Concat(gates, grantNames), ", "))
		case !slices.Contains(gates, cmd.Grant) && !slices.Contains(grantNames, cmd.Grant):
			add("%s: grant is %q; accepted values are %s", where, cmd.Grant, strings.Join(slices.Concat(gates, grantNames), ", "))
		}
		for _, need := range cmd.MayNeed {
			if !slices.Contains(grantNames, need) {
				add("%s: may_need names %q; accepted values are %s", where, need, strings.Join(grantNames, ", "))
			}
		}
		if cmd.RunsWithoutDaemon && cmd.Runs != RunsHere {
			add("%s: runs_without_daemon needs runs: here; a command that answers with no daemon runs on the caller's machine", where)
		}
		if !cmd.IsOffered() {
			if strings.TrimSpace(cmd.Reason) == "" {
				add("%s: offered is false without a reason", where)
			}
		}
		if strings.TrimSpace(cmd.Summary) == "" {
			add("%s: summary is missing", where)
		}
		if strings.Contains(strings.TrimSpace(cmd.Summary), "\n") {
			add("%s: summary is more than one line", where)
		}
		if strings.TrimSpace(cmd.Doc) == "" && cmd.ThroughLayer() {
			add("%s: doc is missing", where)
		}
		codes := map[string]bool{}
		for _, e := range cmd.Errors {
			switch {
			case !errorCodePattern.MatchString(e.Code):
				add("%s: error code %q is not a lower_snake_case name", where, e.Code)
			case codes[e.Code]:
				add("%s: error %q is listed twice", where, e.Code)
			case strings.TrimSpace(e.Fix) == "":
				add("%s: error %q has no fix", where, e.Code)
			}
			for _, problem := range c.MentionProblems(e.Fix) {
				add("%s: error %q: %s", where, e.Code, problem)
			}
			codes[e.Code] = true
		}
		args := map[string]bool{}
		for _, a := range cmd.Args {
			argWhere := fmt.Sprintf("%s: arg %q", where, a.Name)
			if args[a.Name] {
				add("%s: defined twice", argWhere)
			}
			args[a.Name] = true
			if _, ok := c.Types[a.Type]; !ok {
				add("%s: type %q is not in %s", argWhere, a.Type, TypesFile)
			}
			if a.CLI == "" {
				add("%s: cli is missing; name the mapping: pos, rest, words, a flag, --a|--b, -m+ or internal", argWhere)
			}
			if items, _ := c.Types[a.Type].Schema["items"].(map[string]any); a.CLI == "words" && (c.Types[a.Type].Schema["type"] != "array" || items["required"] == nil) {
				add("%s: cli words takes an array of objects whose required fields are the words of each item", argWhere)
			}
			if a.CLI == "pos" && a.OneOf != "" {
				if problem := c.sharedPositionalProblem(cmd, a); problem != "" {
					add("%s: %s", argWhere, problem)
				}
			}
			if a.Unless != "" && !slices.ContainsFunc(cmd.Args, func(other Arg) bool { return other.Name == a.Unless && other.Type == "bool" }) {
				add("%s: unless names %q, which is not a bool arg of this command", argWhere, a.Unless)
			}
			if strings.Contains(a.CLI, "|") && len(a.Values) == 0 {
				add("%s: cli %q maps values to flags, but the arg has no values", argWhere, a.CLI)
			}
			if a.Local != "" && !slices.Contains(locals, a.Local) {
				add("%s: local is %q; accepted values are path, destination, evidence and commit", argWhere, a.Local)
			}
			if a.LocalWhen != "" && (a.Local == "" || !slices.ContainsFunc(cmd.Args, func(other Arg) bool { return other.Name == a.LocalWhen })) {
				add("%s: local_when names %q; it needs local and another arg of this command", argWhere, a.LocalWhen)
			}
			if a.Internal() && (a.Required || a.Local != "" || a.CLIOnly) {
				add("%s: cli internal on an arg that is required, local or cli_only; the CLI fills it alone, and a tool call leaves it out", argWhere)
			}
			if a.CLIOnly && (a.Required || a.Local != "") {
				add("%s: cli_only on an arg that is required or local; a tool must be able to make the call without it", argWhere)
			}
		}
		for _, example := range cmd.Examples {
			for key := range example {
				if !args[key] {
					add("%s: example names %q, which is not an arg", where, key)
				}
			}
		}
	}

	for gi := range c.Groups {
		g := &c.Groups[gi]
		switch summary := strings.TrimSpace(g.Summary); {
		case summary == "":
			add("%s: group %q: summary is missing", g.File, g.Name)
		case strings.Contains(summary, "\n"):
			add("%s: group %q: summary is more than one line; put the paragraph in doc", g.File, g.Name)
		}
		if g.Usage != "" && strings.Contains(g.Name, " ") {
			add("%s: group %q: usage is for a top-level group", g.File, g.Name)
		}
		listed := map[string]bool{}
		for _, section := range g.Sections {
			for _, name := range section.Commands {
				if _, ok := c.Command(name); !ok {
					add("%s: group %q: section %q names %q, which is not a command", g.File, g.Name, section.Title, name)
				}
				if listed[name] {
					add("%s: group %q: %q is in two sections", g.File, g.Name, name)
				}
				listed[name] = true
			}
		}
		if len(g.Sections) == 0 {
			continue
		}
		for _, cmd := range c.GroupCommands(g.Name) {
			if !listed[cmd.Name] {
				add("%s: group %q: %q is in no section", g.File, g.Name, cmd.Name)
			}
		}
	}
	return append(problems, c.mapProblems()...)
}

func (c *Catalog) Tools(family string) []*Command {
	var tools []*Command
	for _, cmd := range c.FamilyCommands(family) {
		if cmd.IsTool() {
			tools = append(tools, cmd)
		}
	}
	return tools
}

func (c *Catalog) Offers(family string) bool { return len(c.Tools(family)) > 0 }

func (t TopLevel) Map() *FamilyMap {
	if t.Group != nil {
		return t.Group.Map
	}
	return t.Command.Map
}

func (t TopLevel) File() string {
	if t.Group != nil {
		return t.Group.File
	}
	return t.Command.File
}

func (c *Catalog) Instructions() string {
	var rows, notes []string
	for _, top := range c.TopLevel() {
		m := top.Map()
		if m == nil {
			continue
		}
		for _, row := range m.Rows {
			rows = append(rows, fmt.Sprintf("| %s | %s |", strings.TrimSpace(row.Need), strings.Join(row.Tools, ", ")))
		}
		if text := strings.TrimSpace(m.Notes); text != "" {
			notes = append(notes, text)
		}
	}
	parts := []string{strings.TrimSpace(c.Server.Intro)}
	if len(rows) > 0 {
		parts = append(parts, "| When you need to | Tools |\n|---|---|\n"+strings.Join(rows, "\n"))
	}
	return strings.Join(append(parts, notes...), "\n\n")
}

func (c *Catalog) mapShares() string {
	shares := []string{fmt.Sprintf("%s %d", ServerFile, utf8.RuneCountInString(c.Server.Intro))}
	for _, top := range c.TopLevel() {
		m := top.Map()
		if m == nil {
			continue
		}
		n := utf8.RuneCountInString(m.Notes)
		for _, row := range m.Rows {
			n += utf8.RuneCountInString(row.Need + strings.Join(row.Tools, ", "))
		}
		shares = append(shares, fmt.Sprintf("%s %d", top.File(), n))
	}
	return strings.Join(shares, ", ")
}

func (c *Catalog) mapProblems() []string {
	var problems []string
	for _, top := range c.TopLevel() {
		add := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("%s: family %q: ", top.File(), top.Name)+fmt.Sprintf(format, args...))
		}
		tools := map[string]bool{}
		for _, cmd := range c.Tools(top.Name) {
			tools[cmd.ToolName()] = true
		}
		m := top.Map()
		if m == nil {
			if len(tools) > 0 {
				add("it offers tools but has no map; add map rows that say when to reach for them, because the intent map is how an agent finds a tool")
			}
			continue
		}
		if len(m.Rows) == 0 {
			add("map has no rows")
		}
		mapped := map[string]bool{}
		for _, row := range m.Rows {
			if strings.TrimSpace(row.Need) == "" || strings.Contains(row.Need, "|") || strings.Contains(strings.TrimSpace(row.Need), "\n") {
				add("a map row's need must be one line without |")
			}
			if len(row.Tools) == 0 {
				add("map row %q names no tool", row.Need)
			}
			for _, name := range row.Tools {
				if !tools[name] {
					add("map row %q names %q, which is not a tool of this family", row.Need, name)
				}
				mapped[name] = true
			}
		}
		for _, cmd := range c.Tools(top.Name) {
			if cmd.Core && !mapped[cmd.ToolName()] {
				add("core tool %q is in no map row; a core tool is always loaded, so the map must say when to call it", cmd.ToolName())
			}
		}
	}
	return problems
}

func (c *Catalog) commandsUsing(typeName string) string {
	var names []string
	for _, cmd := range c.Commands {
		if slices.ContainsFunc(cmd.Args, func(a Arg) bool { return a.Type == typeName }) {
			names = append(names, cmd.Name)
		}
	}
	if len(names) == 0 {
		return "no command"
	}
	return strings.Join(names, ", ")
}

func schemaProblems(schema map[string]any, at string) []string {
	var problems []string
	keys := make([]string, 0, len(schema))
	for key := range schema {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := schema[key]
		here := path.Join(at, key)
		switch v := value.(type) {
		case nil:
			problems = append(problems, fmt.Sprintf("schema keyword %q is null; a null keyword makes Claude Code drop every tool of the server, so give it a value or remove it", here))
		case map[string]any:
			problems = append(problems, schemaProblems(v, here)...)
		case []any:
			for i, item := range v {
				nested, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if (key == "anyOf" || key == "oneOf") && len(nested) == 0 {
					problems = append(problems, fmt.Sprintf("schema keyword %q holds an empty object at %d; an empty-object union makes a harness drop the server", here, i))
				}
				problems = append(problems, schemaProblems(nested, fmt.Sprintf("%s/%d", here, i))...)
			}
		}
	}
	return problems
}

// SharedPositional reports, for a positional arg that shares its one_of with
// another positional, the partner it takes the word from and the literal
// prefix of its type's pattern that a word must start with to be its.
func (c *Catalog) SharedPositional(cmd *Command, a Arg) (partner, prefix string, ok bool) {
	if a.CLI != "pos" || a.OneOf == "" {
		return "", "", false
	}
	prefix, ok = literalPrefix(c.Types[a.Type].Schema)
	if !ok {
		return "", "", false
	}
	for _, other := range cmd.Args {
		if other.Name != a.Name && other.OneOf == a.OneOf && other.CLI == "pos" {
			return other.Name, prefix, true
		}
	}
	return "", "", false
}

func literalPrefix(schema map[string]any) (string, bool) {
	pattern, _ := schema["pattern"].(string)
	literal, anchored := strings.CutPrefix(pattern, "^")
	if !anchored || literal == "" || strings.ContainsAny(literal, `\.+*?()[]{}|$^`) {
		return "", false
	}
	return literal, true
}

func (c *Catalog) sharedPositionalProblem(cmd *Command, a Arg) string {
	var members []Arg
	positionals := 0
	for _, other := range cmd.Args {
		if other.OneOf == a.OneOf {
			members = append(members, other)
			if other.CLI == "pos" {
				positionals++
			}
		}
	}
	if positionals < 2 {
		return ""
	}
	shared := 0
	for _, member := range members {
		if _, _, ok := c.SharedPositional(cmd, member); ok {
			shared++
		}
	}
	if len(members) != 2 || members[0].CLI != "pos" || members[1].CLI != "pos" || shared != 1 || a.Required {
		return "a positional in a one_of shares the word with one other positional: two optional pos args, and exactly one has a type whose pattern is a literal prefix such as ^s-, which decides whose the word is"
	}
	return ""
}
