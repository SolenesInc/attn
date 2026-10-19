package daemon

import (
	"reflect"
	"strings"
	"testing"
)

func TestMonitorViewFieldsDeclareTheirKindAndFact(t *testing.T) {
	view := reflect.TypeFor[monitorView]()
	for i := range view.NumField() {
		field := view.Field(i)
		kind := field.Type.Name()
		if !strings.HasPrefix(kind, "monitorValue[") && !strings.HasPrefix(kind, "monitorKeyedSet[") && !strings.HasPrefix(kind, "monitorLog[") {
			t.Errorf("View.%s: undeclared field kind %s", field.Name, kind)
		}
		fact, declared := field.Tag.Lookup("fact")
		if !declared || fact == "" {
			t.Errorf("View.%s: declare its existing fact, or - for no fact", field.Name)
		}
	}
}
