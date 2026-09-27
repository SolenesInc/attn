package daemon

import (
	"encoding/json"
	"testing"
	"time"
)

func reportCrash(t *testing.T, d *Daemon, app, kind, message string) {
	t.Helper()
	params, err := json.Marshal(appRuntimeCrashParams{App: app, Kind: kind, Error: message})
	if err != nil {
		t.Fatalf("marshal crash params: %v", err)
	}
	if _, err := d.appRuntimeMethod(jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"crash"`),
		Method:  appRuntimeCrashedMethod,
		Params:  params,
	}); err != nil {
		t.Fatalf("%s: %v", appRuntimeCrashedMethod, err)
	}
}

func TestCrashesOutsideTheWindowDoNotAccumulate(t *testing.T) {
	d := newAppDaemon(t)
	clock := newAppTestClock(d)
	installApp(t, d, "occasional", subscribing("ticket.*"))

	for i := 0; i < appCrashStrikes*3; i++ {
		reportCrash(t, d, "occasional", "uncaughtException", "Error: transient")
		clock.advance(appCrashWindow + time.Minute)
	}

	if !appEnabled(t, d, "occasional") {
		t.Fatalf("an app crashing once per %s window was disabled", appCrashWindow)
	}
}
