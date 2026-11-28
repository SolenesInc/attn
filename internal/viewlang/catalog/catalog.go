package catalog

const Language = 1

type ArgKind int

const (
	ItemsArg ArgKind = iota + 1
	FieldArg
	NameArg
	CommandArg
	FlagArg
	TextArg
	ChoiceArg
	DurationArg
	KeyedFieldArg
	FileArg
)

func (k ArgKind) Positional() bool {
	return k == ItemsArg || k == FieldArg || k == NameArg || k == CommandArg
}

type Arg struct {
	Name        string
	Kind        ArgKind
	Required    bool
	Source      bool
	Choices     []string
	Placeholder string
	Doc         string
}

func (a Arg) Form() string {
	placeholder := a.Placeholder
	if placeholder == "" {
		placeholder = a.Name
	}
	switch {
	case a.Kind == FlagArg:
		return a.Name
	case a.Kind.Positional():
		return "<" + placeholder + ">"
	}
	return a.Name + "=<" + placeholder + ">"
}

type Body int

const (
	NoBody Body = iota
	ProseBody
	MediaBody
	TasksBody
	QuestionBody
	TextQuestionBody
	MediaPairBody
	FieldsBody
	FactsBody
	SlotsBody
	TableBody
	BlocksBody
	TextBody
	ControlsBody
	CommandsBody
)

type SlotKind int

const (
	TemplateSlot SlotKind = iota + 1
	CommandSlot
	CommandsSlot
	TextSlot
)

type Slot struct {
	Name string
	Kind SlotKind
	Doc  string
}

type Part struct {
	Name    string
	Colons  int
	Written string
	Summary string
	Args    []Arg
	Body    Body
	Slots   []Slot
	Keys    string
	Reports string
	TopOnly bool
	Example string
}

func (p Part) Container() bool { return p.Colons == 4 }

func (p Part) Opener() string {
	switch p.Colons {
	case 3:
		return ":::" + p.Name
	case 4:
		return "::::" + p.Name
	}
	return ""
}

type Named struct {
	Name string
	Doc  string
}

const ReservedPart = "script"

var answerName = Arg{Name: "name", Kind: NameArg, Required: true, Doc: "the name the answer is saved under"}

var itemsArg = Arg{Name: "items", Kind: ItemsArg, Required: true, Doc: "the field that holds the items, or a .json or .jsonl file in the view's folder"}

var rowSlots = []Slot{
	{Name: "lead", Kind: TemplateSlot, Doc: "the mark at the start of the row"},
	{Name: "title", Kind: TemplateSlot, Doc: "the row's main text"},
	{Name: "sub", Kind: TemplateSlot, Doc: "a second, quieter line"},
	{Name: "aside", Kind: TemplateSlot, Doc: "text at the end of the row"},
	{Name: "press", Kind: CommandSlot, Doc: "the command ↩ runs on the row"},
	{Name: "controls", Kind: CommandsSlot, Doc: "the commands the row offers, separated by commas"},
	{Name: "empty", Kind: TextSlot, Doc: "what the list says when it has no items"},
	{Name: "group-aside", Kind: TemplateSlot, Doc: "text at the end of the heading above the list"},
	{Name: "group-controls", Kind: CommandsSlot, Doc: "the commands the heading above the list offers, separated by commas"},
}

var Parts = []Part{
	{Name: "markdown", Written: "prose", Summary: "Prose, headings, links and code blocks, drawn as markdown.", Body: ProseBody, Example: "markdown.md"},
	{Name: "media", Written: "![label](file)", Summary: "A picture or an audio clip, chosen by the file's type. It stands on a line of its own.", Body: MediaBody, Keys: "Space plays", Example: "media.md"},
	{Name: "tasks", Written: "- [ ] text", Summary: "A task list drawn as checkboxes.", Body: TasksBody, Keys: "x checks", Reports: "which tasks are checked", Example: "tasks.md"},
	{Name: "question", Colons: 3, Summary: "A question with choices, written as in an ask: the question on the first line, then one `- [ ] choice` per line, and `Recommended:` if one choice is best. With no choices it takes text.", Args: []Arg{answerName}, Body: QuestionBody, Keys: "digits pick", Reports: "the choice", Example: "question.md"},
	{Name: "question-multi", Colons: 3, Summary: "A question where the user picks any number of choices.", Args: []Arg{answerName}, Body: QuestionBody, Keys: "digits pick and unpick", Reports: "the choices", Example: "question-multi.md"},
	{Name: "question-text", Colons: 3, Summary: "A question answered with text. `Value:` gives the text it starts with.", Args: []Arg{answerName}, Body: TextQuestionBody, Keys: "e or ↩ writes, Escape leaves the text", Reports: "the text, when it is not the `Value:` text", Example: "question-text.md"},
	{Name: "question-file", Colons: 3, Summary: "A question answered with a file.", Args: []Arg{answerName}, Body: QuestionBody, Keys: "↩ picks a file", Reports: "the file's path", Example: "question-file.md"},
	{Name: "compare", Colons: 3, Summary: "Two media lines side by side, or one flipped over the other.", Body: MediaPairBody, Keys: "f flips", Example: "compare.md"},
	{Name: "data", Colons: 3, Summary: "Names one source of the view's values. It draws nothing.", Args: []Arg{
		{Name: "attn", Kind: ChoiceArg, Source: true, Choices: dataSetNames(), Placeholder: "data set", Doc: "one of attn's own data sets"},
		{Name: "run", Kind: TextArg, Source: true, Placeholder: "program and arguments", Doc: "a program and its arguments that prints one JSON object. attn runs it in the view's folder, never through a shell"},
		{Name: "every", Kind: DurationArg, Placeholder: "duration", Doc: "how often attn reruns `run`, such as 30s or 10m"},
		{Name: "file", Kind: FileArg, Source: true, Doc: "a JSON or JSON Lines file in the view's folder"},
		{Name: "store", Kind: TextArg, Source: true, Doc: "the name of a store: values a kept view owns. Its body lists the store's fields, one `name: type` per line, and its values appear under the store's name. `store=\"view/set\"` shows another kept view's store and takes no body"},
		{Name: "as", Kind: TextArg, Placeholder: "field", Doc: "the field the values appear under"},
	}, Body: FieldsBody, Keys: "r reruns", TopOnly: true, Example: "data.md"},
	{Name: "status", Colons: 3, Summary: "A state mark with its word.", Args: []Arg{{Name: "field", Kind: FieldArg, Required: true, Doc: "the field that holds the state"}}, Example: "status.md"},
	{Name: "facts", Colons: 3, Summary: "Lines of label and value, one `label | value` per line.", Body: FactsBody, Example: "facts.md"},
	{Name: "list", Colons: 3, Summary: "One row per item, written as slots, one `slot: value` per line.", Args: []Arg{
		itemsArg,
		{Name: "foldable", Kind: FlagArg, Doc: "the heading above the list folds it"},
		{Name: "count", Kind: FlagArg, Doc: "the heading above the list shows how many items it has"},
	}, Body: SlotsBody, Slots: rowSlots, Keys: "j and k move, ↩ runs press", Reports: "the row's answers", Example: "list.md"},
	{Name: "table", Colons: 3, Summary: "One table line per item. The body is a markdown table whose one row is the template for each item.", Args: []Arg{itemsArg}, Body: TableBody, Keys: "j and k move", Example: "table.md"},
	{Name: "chart", Colons: 3, Summary: "A line chart of the items.", Args: []Arg{
		itemsArg,
		{Name: "x", Kind: KeyedFieldArg, Required: true, Placeholder: "field", Doc: "the item field along the bottom"},
		{Name: "y", Kind: KeyedFieldArg, Required: true, Placeholder: "field", Doc: "the item field up the side"},
	}, Example: "chart.md"},
	{Name: "each", Colons: 4, Summary: "Its content once per item: prose, headings, media, questions and parts.", Args: []Arg{itemsArg}, Body: BlocksBody, Reports: "each item's answers", Example: "each.md"},
	{Name: "one-by-one", Colons: 4, Summary: "One item at a time, with progress and a summary before sending.", Args: []Arg{itemsArg}, Body: BlocksBody, Keys: "↩ keeps and goes on, s skips, ← and → step; e writes and digits pick in the item's questions", Reports: "each item's answers, and whether the user kept, changed or skipped it", Example: "one-by-one.md"},
	{Name: "entry", Colons: 3, Summary: "A line to type in. ↩ runs the command with the text. The body is the hint shown while the line is empty.", Args: []Arg{{Name: "command", Kind: CommandArg, Required: true, Doc: "the command and its arguments"}}, Body: TextBody, Keys: "↩ runs", Example: "entry.md"},
	{Name: "controls", Colons: 3, Summary: "The view's own buttons, in its header, one command per line.", Body: ControlsBody, Keys: "each control's letter", TopOnly: true, Example: "controls.md"},
	{Name: "commands", Colons: 3, Summary: "Defines the view's own commands, one `name: program arguments` per line. It draws nothing.", Body: CommandsBody, TopOnly: true, Example: "commands.md"},
}

var Formats = []Named{
	{Name: "duration", Doc: "a number of seconds, drawn as a duration such as 1h 4m"},
	{Name: "ago", Doc: "an instant, drawn as how long ago it was, such as 4 min ago"},
}

var DataSets = []Named{
	{Name: "sessions", Doc: "the profile's sessions"},
	{Name: "pull-requests", Doc: "the pull requests attn follows"},
	{Name: "asks", Doc: "the open asks, and those answered in the last day"},
	{Name: "seeds", Doc: "the open seeds"},
	{Name: "app", Doc: "attn itself: its version and what is new"},
}

var StoreTypes = []Named{
	{Name: "text", Doc: "text"},
	{Name: "number", Doc: "a number"},
	{Name: "yes-no", Doc: "yes or no"},
}

var Commands = []Named{
	{Name: "open", Doc: "opens the row's session, pull request or file"},
	{Name: "new-session", Doc: "starts a session"},
	{Name: "choose-harness", Doc: "picks the harness of a new session"},
	{Name: "unsnooze", Doc: "brings a snoozed session back"},
	{Name: "approve", Doc: "approves a pull request"},
	{Name: "merge", Doc: "merges a pull request"},
	{Name: "mute", Doc: "mutes a pull request"},
	{Name: "mute-author", Doc: "mutes a pull request's author"},
	{Name: "mute-repo", Doc: "mutes a repository"},
	{Name: "refresh", Doc: "fetches the data again"},
	{Name: "follow", Doc: "follows the next turn; a switch that shows on or off"},
	{Name: "settings", Doc: "opens Settings"},
	{Name: "whats-new", Doc: "shows what is new in attn"},
}

type StoreCommand struct {
	Name  string
	Field bool
	Value bool
	Type  string
	Doc   string
}

var StoreCommands = []StoreCommand{
	{Name: "add", Field: true, Doc: "adds an item with the typed text in the field"},
	{Name: "set", Field: true, Value: true, Doc: "sets the row's field to the value"},
	{Name: "toggle", Field: true, Type: "yes-no", Doc: "switches the row's yes-no field"},
	{Name: "remove", Doc: "removes the row"},
}

func dataSetNames() []string {
	names := make([]string, 0, len(DataSets))
	for _, set := range DataSets {
		names = append(names, set.Name)
	}
	return names
}

func FindPart(name string) (Part, bool) {
	for _, part := range Parts {
		if part.Name == name && part.Colons > 0 {
			return part, true
		}
	}
	return Part{}, false
}

func Form(name string) Part {
	for _, part := range Parts {
		if part.Name == name {
			return part
		}
	}
	panic("catalog: no form " + name)
}
