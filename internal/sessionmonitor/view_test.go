package sessionmonitor

import (
	"reflect"
	"strings"
	"testing"
)

func TestMonitorViewFieldsDeclareTheirKindAndFact(t *testing.T) {
	view := reflect.TypeFor[View]()
	for i := range view.NumField() {
		field := view.Field(i)
		kind := field.Type.Name()
		if !strings.HasPrefix(kind, "Value[") && !strings.HasPrefix(kind, "KeyedSet[") && !strings.HasPrefix(kind, "Log[") {
			t.Errorf("View.%s: undeclared field kind %s", field.Name, kind)
		}
		fact, declared := field.Tag.Lookup("fact")
		if !declared || fact == "" {
			t.Errorf("View.%s: declare its existing fact, or - for no fact", field.Name)
		}
	}
}
