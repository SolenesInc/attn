package daemon

import (
	"encoding/json"
	"slices"

	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/setting"
)

var builtInViews = []string{"attn", "sessions", "pull-requests"}

type namedView struct {
	View string `json:"view"`
}

func viewNamedByTileParams(params string) (string, bool) {
	var named namedView
	if json.Unmarshal([]byte(params), &named) != nil || named.View == "" {
		return "", false
	}
	return named.View, true
}

func (d *Daemon) storedViewTileParams(view string) (string, error) {
	if !d.on(setting.ViewsEnabled) {
		return "", profiles.Errorf(profiles.CodeInvalid, "a tile shows the view %q only while the experimental setting views_enabled is on", view)
	}
	if !slices.Contains(builtInViews, view) {
		return "", profiles.Errorf(profiles.CodeInvalid, "tile_params name the view %q; attn's views are attn, sessions and pull-requests", view)
	}
	encoded, _ := json.Marshal(namedView{View: view})
	return string(encoded), nil
}
