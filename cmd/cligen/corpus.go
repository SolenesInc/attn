package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

const generatedCasesPerTable = 300

func writeCorpus(directory string) error {
	random := rand.New(rand.NewPCG(20261108, 1))
	spec, err := json.MarshalIndent(flagSpecs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "flag_spec.json"), append(spec, '\n'), 0o644); err != nil {
		return err
	}
	for _, table := range []struct {
		file  string
		cases []any
	}{
		{"flag.jsonl", flagCases(random)},
		{"tabwriter.jsonl", tabwriterCases(random)},
		{"quote.jsonl", quoteCases(random)},
		{"parse_duration.jsonl", parseDurationCases(random)},
		{"duration_string.jsonl", durationStringCases(random)},
		{"local_time.jsonl", localTimeCases(random)},
		{"json_indent.jsonl", jsonIndentCases(random)},
	} {
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		for _, c := range table.cases {
			if err := encoder.Encode(c); err != nil {
				return fmt.Errorf("%s: %w", table.file, err)
			}
		}
		if err := os.WriteFile(filepath.Join(directory, table.file), out.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func pick[T any](random *rand.Rand, choices []T) T { return choices[random.IntN(len(choices))] }

type flagSpec struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Default string `json:"default"`
	Usage   string `json:"usage"`
}

type flagCase struct {
	Args   []string          `json:"args"`
	Values map[string]string `json:"values"`
	Set    []string          `json:"set"`
	Rest   []string          `json:"rest"`
	Error  string            `json:"error"`
	Output string            `json:"output"`
}

var flagSpecs = []flagSpec{
	{"all", "bool", "false", "show every seed"},
	{"json", "bool", "false", "print JSON"},
	{"on", "bool", "true", "starts enabled"},
	{"m", "string", "", "the message"},
	{"plot", "string", "fallback", "the `name` of the plot"},
	{"limit", "int", "20", "how many"},
	{"since", "int64", "0", "a bookmark"},
	{"timeout", "duration", "1m30s", "how long to wait"},
}

var flagArgTables = [][]string{
	{},
	{"-all"},
	{"--all"},
	{"-all=true"},
	{"-all=false"},
	{"-all=0"},
	{"-all=maybe"},
	{"-all", "false"},
	{"-on=false", "rest"},
	{"-m", "hello"},
	{"-m=hello"},
	{"--m", "-all"},
	{"-m"},
	{"-m="},
	{"-m", ""},
	{"-plot=a=b"},
	{"-limit", "5"},
	{"-limit", "0x10"},
	{"-limit", "0b11"},
	{"-limit", "1_000"},
	{"-limit", "abc"},
	{"-limit", "99999999999999999999"},
	{"-limit="},
	{"-since", "-3"},
	{"-since", "9223372036854775808"},
	{"-timeout", "90s"},
	{"-timeout", "soon"},
	{"-timeout", "5"},
	{"--"},
	{"--", "-all"},
	{"-", "-all"},
	{"rest", "-all"},
	{"---all"},
	{"-=x"},
	{"--=x"},
	{"-nope"},
	{"--nope=1"},
	{"-h"},
	{"-help"},
	{"--help"},
	{"-all", "-all=false", "-all"},
	{"-m", "first", "-m", "second"},
	{"-é"},
}

var flagArgTokens = []string{"-all", "--json", "-on=false", "-m", "-m=x", "-plot", "-limit", "-limit=7", "-limit=x", "-since", "-timeout", "-timeout=2h", "-timeout=x", "--", "-", "word", "", "7", "-nope", "-h", "two words", "é"}

func flagCases(random *rand.Rand) []any {
	var cases []any
	for _, args := range flagArgTables {
		cases = append(cases, runFlags(flagSpecs, args))
	}
	for range generatedCasesPerTable {
		args := make([]string, random.IntN(6))
		for i := range args {
			args[i] = pick(random, flagArgTokens)
		}
		cases = append(cases, runFlags(flagSpecs, args))
	}
	return cases
}

func runFlags(specs []flagSpec, args []string) flagCase {
	var output bytes.Buffer
	fs := flag.NewFlagSet("cmd", flag.ContinueOnError)
	fs.SetOutput(&output)
	for _, spec := range specs {
		switch spec.Kind {
		case "bool":
			fs.Bool(spec.Name, spec.Default == "true", spec.Usage)
		case "string":
			fs.String(spec.Name, spec.Default, spec.Usage)
		case "int":
			value, _ := strconv.Atoi(spec.Default)
			fs.Int(spec.Name, value, spec.Usage)
		case "int64":
			value, _ := strconv.ParseInt(spec.Default, 10, 64)
			fs.Int64(spec.Name, value, spec.Usage)
		case "duration":
			value, _ := time.ParseDuration(spec.Default)
			fs.Duration(spec.Name, value, spec.Usage)
		}
	}
	result := flagCase{Args: args, Values: map[string]string{}, Set: []string{}}
	if err := fs.Parse(args); err != nil {
		result.Error = err.Error()
	}
	fs.VisitAll(func(f *flag.Flag) { result.Values[f.Name] = f.Value.String() })
	fs.Visit(func(f *flag.Flag) { result.Set = append(result.Set, f.Name) })
	result.Rest = append([]string{}, fs.Args()...)
	result.Output = output.String()
	return result
}

type textCase struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

var tabwriterTables = []string{
	"",
	"no tabs\n",
	"no newline at the end",
	"a\tb\n",
	"a\tb",
	"a\tb\tc\nlonger\tx\ty\n",
	"a\t\tc\n\t\t\nlonger\tx\ty\n",
	"trailing\t\nother\t\n",
	"one\ttwo\n\nthree\tfour\n",
	"block\tone\nno cell here\nlonger block\ttwo\n",
	"é\tb\nééééé\tb\n",
	"日本語\tb\nabc\tb\n",
	"\x1b[1mbold\x1b[0m\tb\nplain\tb\n",
	"a\vb\tc\n",
	"a\fb\tc\nlonger\td\n",
	"\ttabbed\n\ttabbed too\n",
}

var tabwriterCells = []string{"", "a", "seed", "s-7k3f9m", "a much longer cell than the others", "é", "日本", "two words", " padded "}

func tabwriterCases(random *rand.Rand) []any {
	var cases []any
	for _, input := range tabwriterTables {
		cases = append(cases, runTabwriter(input))
	}
	for range generatedCasesPerTable {
		var input strings.Builder
		for range random.IntN(8) {
			for range random.IntN(5) {
				input.WriteString(pick(random, tabwriterCells))
				input.WriteByte('\t')
			}
			input.WriteString(pick(random, tabwriterCells))
			input.WriteByte('\n')
		}
		cases = append(cases, runTabwriter(input.String()))
	}
	return cases
}

func runTabwriter(input string) textCase {
	var output bytes.Buffer
	writer := tabwriter.NewWriter(&output, 0, 0, 2, ' ', 0)
	_, _ = writer.Write([]byte(input))
	_ = writer.Flush()
	return textCase{input, output.String()}
}

type quoteCase struct {
	InputHex string `json:"input_hex"`
	Output   string `json:"output"`
}

var quoteTables = []string{
	"", "plain", `with "quotes"`, `back\slash`, "tab\there", "new\nline", "\r\a\b\f\v", "\x00\x01\x7f", "é", "日本語",
	"\u00a0", "\u00ad", "\u200b", "\u2028", "\ufeff", "\U0001f600", "\U000e0001", "\U0010ffff", "\xff", "a\xc0b", "\xed\xa0\x80", "\xf4\x90\x80\x80", "'",
}

func quoteCases(random *rand.Rand) []any {
	var cases []any
	for _, input := range quoteTables {
		cases = append(cases, quoteCase{hex.EncodeToString([]byte(input)), strconv.Quote(input)})
	}
	for range generatedCasesPerTable {
		var input []byte
		for range random.IntN(12) {
			if random.IntN(3) == 0 {
				input = append(input, byte(random.IntN(256)))
				continue
			}
			input = append(input, string(rune(random.IntN(0x110000)))...)
		}
		cases = append(cases, quoteCase{hex.EncodeToString(input), strconv.Quote(string(input))})
	}
	return cases
}

type parseDurationCase struct {
	Input string `json:"input"`
	Nanos int64  `json:"nanos"`
	Error string `json:"error"`
}

var durationTables = []string{
	"", "0", "+0", "-0", "1", "s", "1s", "+1s", "-1s", "1.5h", ".5s", "5.s", ".", ".s", "1h2m3s", "1h 2m", "1ns", "1us", "1µs", "1μs", "1ms", "1m", "1h", "1d", "1H",
	"2562047h47m16.854775807s", "-2562047h47m16.854775808s", "2562047h47m16.854775808s", "9223372036854775807ns", "9223372036854775808ns", "99999999999h",
	"0.000000001s", "0.0000000001s", "1.00000000000000000000001s", "1s1s", "1h1h", " 1s", "1s ", "--1s", "1e3s", "\"1s", "é",
}

var durationUnits = []string{"ns", "us", "µs", "μs", "ms", "s", "m", "h", "d", ""}

func parseDurationCases(random *rand.Rand) []any {
	var cases []any
	for _, input := range durationTables {
		cases = append(cases, runParseDuration(input))
	}
	for range generatedCasesPerTable {
		var input strings.Builder
		input.WriteString(pick(random, []string{"", "", "-", "+"}))
		for range 1 + random.IntN(4) {
			fmt.Fprintf(&input, "%s%s", pick(random, []string{strconv.Itoa(random.IntN(100000)), fmt.Sprintf("%d.%d", random.IntN(100), random.IntN(100000)), fmt.Sprintf(".%d", random.IntN(1000))}), pick(random, durationUnits))
		}
		cases = append(cases, runParseDuration(input.String()))
	}
	return cases
}

func runParseDuration(input string) parseDurationCase {
	value, err := time.ParseDuration(input)
	if err != nil {
		return parseDurationCase{Input: input, Error: err.Error()}
	}
	return parseDurationCase{Input: input, Nanos: int64(value)}
}

type durationStringCase struct {
	Nanos  int64  `json:"nanos"`
	Output string `json:"output"`
}

var durationStringTables = []int64{0, 1, -1, 999, 1000, 1001, 999999, 1000000, 1000001, 999999999, 1000000000, 59999999999, 60000000000, 3599999999999, 3600000000000, 3661000000000, 90000000000, 1500000000, 9223372036854775807, -9223372036854775808}

func durationStringCases(random *rand.Rand) []any {
	var cases []any
	for _, nanos := range durationStringTables {
		cases = append(cases, durationStringCase{nanos, time.Duration(nanos).String()})
	}
	for range generatedCasesPerTable {
		nanos := random.Int64() >> random.IntN(63)
		if random.IntN(2) == 0 {
			nanos = -nanos
		}
		cases = append(cases, durationStringCase{nanos, time.Duration(nanos).String()})
	}
	return cases
}

type localTimeCase struct {
	Input   string            `json:"input"`
	Zone    string            `json:"zone"`
	Formats map[string]string `json:"formats"`
	Error   string            `json:"error"`
}

var localTimeZones = []string{"UTC", "America/New_York", "America/Sao_Paulo", "Europe/Stockholm", "Asia/Kolkata", "Asia/Tokyo", "Australia/Lord_Howe"}

var localTimeLayouts = []string{"2006-01-02", "2006-01-02 15:04", "2006-01-02 15:04:05", "15:04:05", "15:04:05.000", "Jan 2 15:04", time.RFC3339, time.RFC3339Nano}

var localTimeTables = []string{
	"2024-03-10T06:59:59Z", "2024-03-10T07:00:00Z", "2024-11-03T05:59:59Z", "2024-11-03T06:00:00Z",
	"2024-03-31T00:59:59Z", "2024-03-31T01:00:00Z", "2024-10-27T00:59:59Z", "2024-10-27T01:00:00Z",
	"2024-02-29T23:59:59.999999999Z", "2024-01-01T00:00:00+05:30", "2024-01-01T00:00:00-03:00", "2024-01-05T09:08:07.1Z", "2024-01-05T09:08:07.123456Z",
	"", "not a time", "2024-01-05", "2024-01-05T09:08:07", "2024-13-01T00:00:00Z", "2024-02-30T00:00:00Z", "2024-01-05t09:08:07z", "2024-01-05T09:08:07,5Z", "2024-01-05T09:08:07+24:00",
}

func localTimeCases(random *rand.Rand) []any {
	var cases []any
	for _, zone := range localTimeZones {
		for _, input := range localTimeTables {
			cases = append(cases, runLocalTime(input, zone))
		}
	}
	start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	for range generatedCasesPerTable {
		at := time.Unix(start+random.Int64N(end-start), int64(random.IntN(1e9))*int64(random.IntN(2))).UTC()
		cases = append(cases, runLocalTime(at.Format(time.RFC3339Nano), pick(random, localTimeZones)))
	}
	return cases
}

func runLocalTime(input, zone string) localTimeCase {
	location, err := time.LoadLocation(zone)
	if err != nil {
		fatal(err)
	}
	result := localTimeCase{Input: input, Zone: zone, Formats: map[string]string{}}
	at, err := time.Parse(time.RFC3339Nano, input)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	local := at.In(location)
	for _, layout := range localTimeLayouts {
		result.Formats[layout] = local.Format(layout)
	}
	return result
}

var jsonIndentTables = []string{
	`{}`, `[]`, `null`, `true`, `0`, `-1.5e+3`, `""`, `[[]]`, `[{}]`, `{"a":{}}`, `{"a":[]}`, `[1,2,3]`, `{"a":1,"b":[true,null,{"c":"d"}]}`,
	` { "a" : 1 , "b" : [ 1 , 2 ] } `, "{\n\"a\":\t1\r\n}", `{"z":1,"a":2}`, `"<>&"`, `"<>&"`, `"éé😀"`, `"\\\"\/\n"`, `{"":""}`, `1E400`, `[ ]`, `{ }`, `[ [ ] , { } ]`,
}

func jsonIndentCases(random *rand.Rand) []any {
	var cases []any
	for _, input := range jsonIndentTables {
		cases = append(cases, runJSONIndent(input))
	}
	for range generatedCasesPerTable {
		input, err := json.Marshal(randomJSONValue(random, 4))
		if err != nil {
			fatal(err)
		}
		cases = append(cases, runJSONIndent(string(input)))
	}
	return cases
}

func runJSONIndent(input string) textCase {
	var output bytes.Buffer
	if err := json.Indent(&output, []byte(input), "", "  "); err != nil {
		fatal(fmt.Errorf("json indent of %q: %w", input, err))
	}
	return textCase{input, output.String()}
}

var jsonStrings = []string{"", "plain", "<a href=\"x\">&</a>", "é", "日本語", "\u2028", "\U0001f600", "\t\n", "null", "{\"not\":\"json\"}"}

func randomJSONValue(random *rand.Rand, depth int) any {
	kinds := 5
	if depth > 0 {
		kinds = 7
	}
	switch random.IntN(kinds) {
	case 0:
		return nil
	case 1:
		return random.IntN(2) == 0
	case 2:
		return random.Int64() >> random.IntN(63)
	case 3:
		return random.Float64() * 1e6
	case 4:
		return pick(random, jsonStrings)
	case 5:
		values := make([]any, random.IntN(4))
		for i := range values {
			values[i] = randomJSONValue(random, depth-1)
		}
		return values
	default:
		values := map[string]any{}
		for range random.IntN(4) {
			values[pick(random, jsonStrings)] = randomJSONValue(random, depth-1)
		}
		return values
	}
}
