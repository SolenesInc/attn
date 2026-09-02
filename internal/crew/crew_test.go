package crew

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Built by shape, never copied from the live `~/.attn/crew`.
func writeCrewFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "crew")
	mustWrite(t, filepath.Join(root, "CREW.md"), "# The crew\n\nkeel, alder, trellis.\n")
	for _, member := range []struct {
		id       string
		handoffs []string
	}{
		{"alder", []string{"2026-08-06T22-51Z-alder.md", "2026-08-10T19-20Z-alder.md"}},
		{"keel", []string{"2026-08-06T00-30Z-keel.md", "2026-08-13T22-10Z-keel.md"}},
		{"trellis", []string{"2026-08-13T22-20Z-trellis.md"}},
	} {
		home := filepath.Join(root, member.id)
		mustWrite(t, filepath.Join(home, CharterFileName), "# "+member.id+"\n\nWhat I care about.\n")
		for _, name := range member.handoffs {
			mustWrite(t, filepath.Join(home, "handoffs", name), "Where I left off.\n")
		}
	}
	return root
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestScanHomes_ReadsEveryHomeAndPointsAtItsFiles(t *testing.T) {
	root := writeCrewFixture(t)

	members, err := ScanHomes(root, nil)
	if err != nil {
		t.Fatalf("ScanHomes: %v", err)
	}

	var ids []string
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	if got, want := strings.Join(ids, ","), "alder,keel,trellis"; got != want {
		t.Fatalf("members = %q, want %q", got, want)
	}
	for _, m := range members {
		if want := filepath.Join(root, m.ID); m.HomeDir != want {
			t.Errorf("%s home = %q, want %q", m.ID, m.HomeDir, want)
		}
		if want := filepath.Join(root, m.ID, CharterFileName); m.CharterPath != want {
			t.Errorf("%s charter = %q, want %q", m.ID, m.CharterPath, want)
		}
		if _, err := os.Stat(m.CharterPath); err != nil {
			t.Errorf("%s charter does not exist: %v", m.ID, err)
		}
	}
}

func TestScanHomes_CarriesNoProse(t *testing.T) {
	root := writeCrewFixture(t)
	body := "the charter's own words"
	mustWrite(t, filepath.Join(root, "keel", CharterFileName), body)

	members, err := ScanHomes(root, nil)
	if err != nil {
		t.Fatalf("ScanHomes: %v", err)
	}
	for _, m := range members {
		encoded, err := m.Encode()
		if err != nil {
			t.Fatalf("encode %s: %v", m.ID, err)
		}
		if strings.Contains(string(encoded), body) {
			t.Fatalf("%s's record carries the charter's prose: %s", m.ID, encoded)
		}
	}
}

func TestScanHomes_SkipsWhatIsNotAHome(t *testing.T) {
	root := writeCrewFixture(t)
	mustWrite(t, filepath.Join(root, "scratch", "note.md"), "not a member\n")
	mustWrite(t, filepath.Join(root, "Not A Member", CharterFileName), "# nope\n")

	var warnings []string
	members, err := ScanHomes(root, func(format string, args ...any) {
		warnings = append(warnings, format)
	})
	if err != nil {
		t.Fatalf("ScanHomes: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("members = %d, want the 3 real homes", len(members))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d (%v), want exactly the unusable home named", len(warnings), warnings)
	}
}

func TestScanHomes_MissingDirectoryIsAnEmptyRoster(t *testing.T) {
	members, err := ScanHomes(filepath.Join(t.TempDir(), "no-crew-here"), func(string, ...any) {
		t.Error("a missing crew directory warned")
	})
	if err != nil {
		t.Fatalf("ScanHomes: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("members = %d, want none", len(members))
	}
}

func TestValidateID(t *testing.T) {
	for _, ok := range []string{"trellis", "keel", "a", "night-shift", "k2"} {
		if err := ValidateID(ok); err != nil {
			t.Errorf("ValidateID(%q) = %v, want accepted", ok, err)
		}
	}
	for _, bad := range []string{"", "Trellis", "2keel", "with space", "with/slash", "-lead", DaemonID, strings.Repeat("a", MaxIDChars+1)} {
		if err := ValidateID(bad); err == nil {
			t.Errorf("ValidateID(%q) = nil, want refused", bad)
		}
	}
}

func TestValidateID_LongNameNamesTheLimitAndTheAsk(t *testing.T) {
	asked := strings.Repeat("a", MaxIDChars+7)
	err := ValidateID(asked)
	if err == nil {
		t.Fatal("ValidateID accepted an over-long id")
	}
	for _, want := range []string{"47", "40"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
}

func TestResolve_FoldsCaseAndAnswersNoForStrangers(t *testing.T) {
	members := []Member{{ID: "keel"}, {ID: "trellis"}}

	for _, name := range []string{"trellis", "Trellis", " TRELLIS "} {
		member, ok := Resolve(name, members)
		if !ok || member.ID != "trellis" {
			t.Errorf("Resolve(%q) = %v, %v; want trellis", name, member.ID, ok)
		}
	}
	if _, ok := Resolve("some-worker", members); ok {
		t.Error("Resolve matched an unregistered name")
	}
	if _, ok := Resolve("", members); ok {
		t.Error("Resolve matched the empty name")
	}
}

func TestDecode_IgnoresUnknownKeys(t *testing.T) {
	member, err := Decode([]byte(`{"id":"keel","home_dir":"/h","mood":"curious"}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if member.ID != "keel" || member.HomeDir != "/h" {
		t.Fatalf("Decode = %+v", member)
	}
}

// A declared field a query filters on must exist in every stored body, or a filter on `binding_session = ""` would not match the sleeping members.
func TestEncode_WritesDeclaredFieldsEvenWhenEmpty(t *testing.T) {
	encoded, err := Member{ID: "keel"}.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for _, field := range MembersSchema().Fields {
		if !strings.Contains(string(encoded), `"`+field.Name+`"`) {
			t.Errorf("declared field %q is missing from an empty member's body: %s", field.Name, encoded)
		}
	}
}

func TestFiledLetterFor_AnswersOnlyTheSessionThatFiledIt(t *testing.T) {
	member := Member{ID: "keel", LetterPath: "/homes/keel/handoffs/a.md", LetterSession: "day-1"}
	if path, ok := member.FiledLetterFor("day-1"); !ok || path != member.LetterPath {
		t.Fatalf("FiledLetterFor(day-1) = %q, %t; want the letter it filed", path, ok)
	}
	for _, session := range []string{"day-2", ""} {
		if path, ok := member.FiledLetterFor(session); ok {
			t.Errorf("FiledLetterFor(%q) = %q; a letter another day wrote is not this day's", session, path)
		}
	}
	if _, ok := (Member{ID: "keel", LetterSession: "day-1"}).FiledLetterFor("day-1"); ok {
		t.Error("a member with no recorded letter reported one")
	}
}

func TestMembersSchema_IsAValidDeclaration(t *testing.T) {
	if err := MembersSchema().Validate(); err != nil {
		t.Fatalf("MembersSchema is not declarable: %v", err)
	}
}

func TestDisplayName_WritesTheIDAsAName(t *testing.T) {
	for id, want := range map[string]string{
		"trellis":   "Trellis",
		"keel":      "Keel",
		"alder":     "Alder",
		"a":         "A",
		"mary-jane": "Mary-jane",
		"Trellis":   "Trellis",
		"":          "",
		"  keel  ":  "Keel",
		"ólafur":    "Ólafur",
	} {
		if got := DisplayName(id); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestDisplayName_StillResolvesToItsMember(t *testing.T) {
	members := []Member{{ID: "trellis"}, {ID: "keel"}}
	member, ok := Resolve(DisplayName("keel"), members)
	if !ok || member.ID != "keel" {
		t.Fatalf("Resolve(%q) = %q, %t; want keel", DisplayName("keel"), member.ID, ok)
	}
	if err := ValidateID(member.ID); err != nil {
		t.Fatalf("the resolved id is not a member id: %v", err)
	}
}

func TestHolderName_NamesTheMemberAndLeavesASessionAlone(t *testing.T) {
	if got := HolderName("trellis", "sess-a"); got != "Trellis" {
		t.Errorf("HolderName(trellis, sess-a) = %q, want Trellis", got)
	}
	if got := HolderName("", "sess-a"); got != "sess-a" {
		t.Errorf("HolderName(\"\", sess-a) = %q, want the session id untouched", got)
	}
	if got := HolderName("  ", "  "); got != "" {
		t.Errorf("an unheld thing displays as %q, want empty", got)
	}
}

func TestTheDaemonsOwnNameIsReservedAndStaysLowercase(t *testing.T) {
	if err := ValidateID(DaemonID); err == nil || !strings.Contains(err.Error(), DaemonID) {
		t.Fatalf("ValidateID(%q) = %v, want a refusal that names it", DaemonID, err)
	}
	if got := DisplayName(DaemonID); got != DaemonID {
		t.Errorf("DisplayName(%q) = %q, want it written the way the product is", DaemonID, got)
	}
	if got := HolderName(DaemonID, ""); got != DaemonID {
		t.Errorf("HolderName(%q, \"\") = %q, want %q", DaemonID, got, DaemonID)
	}
}
