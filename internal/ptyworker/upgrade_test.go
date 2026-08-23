package ptyworker

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// The handoff JSON is the whole contract for an adopted session's config: Run
// overwrites cfg with what the file carried and argv re-supplies only the three
// adopt fields. A new Config field that does not survive that round trip hands
// adopted sessions a zero where spawned ones get a value, and nothing else in
// the system would notice.
func TestHandoffCarriesEveryConfigField(t *testing.T) {
	// Set by argv on the adopt half, so the file's copy is deliberately ignored.
	suppliedByArgv := map[string]bool{
		"Logf":            true, // a func; json:"-" and re-derived from the flags
		"AdoptHandoff":    true,
		"AdoptPtmxFD":     true,
		"AdoptListenerFD": true,
		"Debug":           true,
	}

	var cfg Config
	value := reflect.ValueOf(&cfg).Elem()
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if suppliedByArgv[field.Name] {
			continue
		}
		if !fillNonZero(value.Field(i)) {
			t.Fatalf("Config.%s is a %s the handoff test cannot populate; teach fillNonZero about it",
				field.Name, field.Type)
		}
	}

	raw, err := json.Marshal(handoffFile{Config: cfg})
	if err != nil {
		t.Fatalf("marshal handoff: %v", err)
	}
	var hf handoffFile
	if err := json.Unmarshal(raw, &hf); err != nil {
		t.Fatalf("unmarshal handoff: %v", err)
	}

	got := reflect.ValueOf(&hf.Config).Elem()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if suppliedByArgv[field.Name] {
			continue
		}
		if got.Field(i).IsZero() {
			t.Errorf("Config.%s did not survive the handoff; an adopted session would get the zero value", field.Name)
		}
	}
}

// fillNonZero writes a recognizable non-zero value into v, walking structs so a
// nested config field is populated rather than skipped.
func fillNonZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Slice:
		elem := reflect.New(v.Type().Elem()).Elem()
		if !fillNonZero(elem) {
			return false
		}
		v.Set(reflect.Append(v, elem))
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if !fillNonZero(v.Index(i)) {
				return false
			}
		}
	case reflect.Map:
		key := reflect.New(v.Type().Key()).Elem()
		val := reflect.New(v.Type().Elem()).Elem()
		if !fillNonZero(key) || !fillNonZero(val) {
			return false
		}
		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(key, val)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		return fillNonZero(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !v.Field(i).CanSet() {
				continue
			}
			if !fillNonZero(v.Field(i)) {
				return false
			}
		}
	default:
		return false
	}
	return true
}

func TestHandoffPathsLiveBesideTheRegistryNotInIt(t *testing.T) {
	// Recover and ReapDataDir glob registry/*.json and parse every hit as a
	// registry entry, so a handoff written there is read as a malformed entry
	// and deleted mid-swap.
	registry := filepath.Join("/data", "workers", "inst", "registry", "sess.json")
	jsonPath, dumpPath := HandoffPaths(registry, "sess")
	wantDir := filepath.Join("/data", "workers", "inst", "handoff")
	if got := filepath.Dir(jsonPath); got != wantDir {
		t.Errorf("handoff json lives in %s, want %s", got, wantDir)
	}
	if got := filepath.Dir(dumpPath); got != wantDir {
		t.Errorf("handoff dump lives in %s, want %s", got, wantDir)
	}
}
