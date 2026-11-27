package daemon_test

import "testing"

func TestSettingsForThisMachineKeepTheirLocalKeysAcrossARestart(t *testing.T) {
	w := newWorld(t)
	app := w.App()
	want := map[string]string{
		"new_session_yolo_local":                    "true",
		"code_review_setup_offer_local_/work/repo":  "off",
		"new_session_destination_local_/work/repo":  "new_worktree",
		"new_session_destination_outpost-1_/remote": "main_repo",
	}
	for key, value := range want {
		setSetting(t, app, key, value)
	}
	require := func(when string, settings map[string]any) {
		t.Helper()
		for key, value := range want {
			if settings[key] != value {
				t.Errorf("%s: %s = %v, want %s", when, key, settings[key], value)
			}
		}
	}
	require("after setting them", w.App().Initial.Settings)
	w.restart()
	require("after a restart", w.App().Initial.Settings)
}
