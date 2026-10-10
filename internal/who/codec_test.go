package who_test

import (
	"database/sql"
	"database/sql/driver"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/victorarias/attn/internal/who"
	"pgregory.net/rapid"
)

type codec interface {
	comparable
	fmt.Stringer
	encoding.TextMarshaler
	driver.Valuer
}

func roundTrip[T codec](t *rapid.T, x T, parse func(string) (T, error)) {
	text := x.String()
	got, err := parse(text)
	if err != nil || got != x {
		t.Fatalf("parse %q = %v, %v; want %v", text, got, err, x)
	}
	encoded, err := x.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	var decoded T
	if err := any(&decoded).(encoding.TextUnmarshaler).UnmarshalText(encoded); err != nil || decoded != x {
		t.Fatalf("text round trip = %v, %v; want %v", decoded, err, x)
	}
	value, err := x.Value()
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []any{value, []byte(text)} {
		if err := any(&decoded).(sql.Scanner).Scan(src); err != nil || decoded != x {
			t.Fatalf("SQL round trip = %v, %v; want %v", decoded, err, x)
		}
	}
	encoded, err = json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != x {
		t.Fatalf("JSON round trip = %v, %v; want %v", decoded, err, x)
	}
}
func TestWhoCodecMatchesItsTextSpecification(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		id := rapid.StringMatching(`[a-zA-Z0-9_:/-]+`).Draw(t, "id")
		key := rapid.StringMatching(`[a-z][a-z0-9-]{0,39}`).Draw(t, "key")
		for _, text := range []string{"session:" + id, "member:" + key} {
			p, err := who.ParseParty(text)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, p, who.ParseParty)
			a, err := who.ParseActor(text)
			if err != nil || a != p.Actor() {
				t.Fatalf("party to actor %q: %v", text, err)
			}
			if narrowed, ok := a.Party(); !ok || narrowed != p {
				t.Fatalf("actor to party %q: %v, %v", text, narrowed, ok)
			}
			roundTrip(t, a, who.ParseActor)
			address, err := who.ParseAddress(text)
			if err != nil || address != p.Address() {
				t.Fatalf("party to address %q: %v", text, err)
			}
			roundTrip(t, address, who.ParseAddress)
		}
		for _, text := range []string{"user", "attn"} {
			a, err := who.ParseActor(text)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, a, who.ParseActor)
			if p, ok := a.Party(); ok || !p.IsZero() {
				t.Fatalf("%s narrowed to party %v, %v", text, p, ok)
			}
			if _, err := who.ParseParty(text); err == nil {
				t.Fatalf("party accepted %q", text)
			}
			if _, err := who.ParseAddress(text); err == nil {
				t.Fatalf("address accepted %q", text)
			}
		}
		for _, text := range []string{"seed:" + id} {
			a, err := who.ParseAddress(text)
			if err != nil {
				t.Fatal(err)
			}
			roundTrip(t, a, who.ParseAddress)
			if _, err := who.ParseParty(text); err == nil {
				t.Fatalf("party accepted %q", text)
			}
			if _, err := who.ParseActor(text); err == nil {
				t.Fatalf("actor accepted %q", text)
			}
		}
		arbitrary := rapid.String().Draw(t, "arbitrary")
		if p, err := who.ParseParty(arbitrary); err == nil && p.String() != arbitrary {
			t.Fatalf("party changed %q to %q", arbitrary, p.String())
		}
		if a, err := who.ParseActor(arbitrary); err == nil && a.String() != arbitrary {
			t.Fatalf("actor changed %q to %q", arbitrary, a.String())
		}
		if a, err := who.ParseAddress(arbitrary); err == nil && a.String() != arbitrary {
			t.Fatalf("address changed %q to %q", arbitrary, a.String())
		}
	})
}
func TestNobodyCannotEncode(t *testing.T) {
	for _, x := range []interface {
		encoding.TextMarshaler
		driver.Valuer
	}{who.Party{}, who.Actor{}, who.Address{}, who.Member(who.MemberKey{})} {
		if _, err := x.MarshalText(); !errors.Is(err, who.ErrNobody) {
			t.Fatalf("%T encoded: %v", x, err)
		}
		if _, err := x.Value(); !errors.Is(err, who.ErrNobody) {
			t.Fatalf("%T stored: %v", x, err)
		}
		if _, err := json.Marshal(x); !errors.Is(err, who.ErrNobody) {
			t.Fatalf("%T JSON encoded: %v", x, err)
		}
	}
	for _, text := range []string{"", "session:", "member:", "seed:", "chief:", "chief:profile", "session:has space", "chief:\t", "member:UPPER", "session:has\nnewline"} {
		if _, err := who.ParseParty(text); err == nil {
			t.Fatalf("party accepted %q", text)
		}
		if _, err := who.ParseActor(text); err == nil {
			t.Fatalf("actor accepted %q", text)
		}
		if _, err := who.ParseAddress(text); err == nil {
			t.Fatalf("address accepted %q", text)
		}
	}
	for _, v := range []interface {
		sql.Scanner
		encoding.TextUnmarshaler
	}{new(who.Party), new(who.Actor), new(who.Address)} {
		for _, src := range []any{"", []byte{}, nil} {
			if err := v.Scan(src); err != nil {
				t.Fatal(err)
			}
		}
		if err := v.UnmarshalText(nil); err != nil {
			t.Fatal(err)
		}
	}
}
