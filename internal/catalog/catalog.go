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
	Name         string `yaml:"name"`
	Instructions string `yaml:"instructions"`
}

type Type struct {
	Schema      map[string]any `yaml:"json"`
	Doc         string         `yaml:"doc"`
	Placeholder string         `yaml:"placeholder"`
}

type Group struct {
	Name      string     `yaml:"name"`
	Summary   string     `yaml:"summary"`
	Doc       string     `yaml:"doc"`
	Flags     []Flag     `yaml:"flags"`
	Sections  []Section  `yaml:"sections"`
	Reference *Reference `yaml:"reference"`
	File      string     `yaml:"-"`
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
	Name      string           `yaml:"name"`
	Summary   string           `yaml:"summary"`
	Core      bool             `yaml:"core"`
	Runs      Runs             `yaml:"runs"`
	Grant     string           `yaml:"grant"`
	MayNeed   []string         `yaml:"may_need"`
	Doc       string           `yaml:"doc"`
	Args      []Arg            `yaml:"args"`
	ReadOnly  bool             `yaml:"read_only"`
	Result    string           `yaml:"result"`
	Errors    []Error          `yaml:"errors"`
	Examples  []map[string]any `yaml:"examples"`
	WrongArgs string           `yaml:"wrong_args"`
	NoArgs    string           `yaml:"no_args"`
	Offered   *bool            `yaml:"offered"`
	Reason    string           `yaml:"reason"`
	File      string           `yaml:"-"`
}

type Error struct {
	Code string `yaml:"code"`
	Fix  string `yaml:"fix"`
}

type Arg struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required"`
	List        bool     `yaml:"list"`
	CLI         string   `yaml:"cli"`
	Stdin       bool     `yaml:"stdin"`
	File        bool     `yaml:"file"`
	Values      []string `yaml:"values"`
	OneOf       string   `yaml:"one_of"`
	Unless      string   `yaml:"unless"`
	MaxLength   int      `yaml:"maxLength"`
	Placeholder string   `yaml:"placeholder"`
	Doc         string   `yaml:"doc"`
}

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

func (c *Catalog) ArgType(a Arg) Type { return c.Types[a.Type] }

func (c *Catalog) check() []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if n := utf8.RuneCountInString(c.Server.Instructions); n > MaxInstructionsChars {
		add("%s: server instructions are %d characters, over the limit of %d that Claude Code keeps; cut the intent map", ServerFile, n, MaxInstructionsChars)
	}
	if strings.TrimSpace(c.Server.Instructions) == "" {
		add("%s: server instructions are empty", ServerFile)
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
		switch cmd.Runs {
		case RunsHome, RunsHere:
		case "":
			add("%s: runs is missing; name where the command runs: home or here", where)
		default:
			add("%s: runs is %q; accepted values are home and here", where, cmd.Runs)
		}
		switch {
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
		if strings.TrimSpace(cmd.Doc) == "" {
			add("%s: doc is missing", where)
		}
		if cmd.WrongArgs == "" && slices.ContainsFunc(cmd.Args, func(a Arg) bool { return a.CLI == "pos" || a.CLI == "rest" }) {
			add("%s: wrong_args is missing; say what the CLI prints when the positionals do not fit", where)
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
				add("%s: cli is missing; name the mapping: pos, rest, a flag, --a|--b, -m+ or stamp", argWhere)
			}
			if a.Unless != "" && !slices.ContainsFunc(cmd.Args, func(other Arg) bool { return other.Name == a.Unless && other.Type == "bool" }) {
				add("%s: unless names %q, which is not a bool arg of this command", argWhere, a.Unless)
			}
			if strings.Contains(a.CLI, "|") && len(a.Values) == 0 {
				add("%s: cli %q maps values to flags, but the arg has no values", argWhere, a.CLI)
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
