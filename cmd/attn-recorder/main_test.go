package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
)

type fakeRecorderSession struct {
	done   chan struct{}
	result recordingResult
	stops  int
}

func (f *fakeRecorderSession) PID() int                { return 44 }
func (f *fakeRecorderSession) Done() <-chan struct{}   { return f.done }
func (f *fakeRecorderSession) Result() recordingResult { return f.result }
func (f *fakeRecorderSession) Stop() recordingResult {
	f.stops++
	select {
	case <-f.done:
	default:
		close(f.done)
	}
	return f.result
}

func TestBrokerOwnsRecordingForConnectionLifetime(t *testing.T) {
	directory := t.TempDir()
	outputPath := filepath.Join(directory, "clip.mp4")
	exitCode := 0
	session := &fakeRecorderSession{
		done:   make(chan struct{}),
		result: recordingResult{Bytes: 123, ExitCode: &exitCode},
	}
	var received startRequest
	server := broker{
		token:        "secret",
		recorderPath: "/bundle/AttnRecorderCapture",
		start: func(path string, windowID uint32, bundleID, output string) (recorderSession, error) {
			received = startRequest{WindowID: windowID, TargetBundleID: bundleID, OutputPath: output}
			return session, nil
		},
	}
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go server.serveConnection(serverConn)

	encoder := json.NewEncoder(clientConn)
	decoder := json.NewDecoder(clientConn)
	if err := encoder.Encode(startRequest{
		Token:          "secret",
		Action:         "start",
		WindowID:       42,
		TargetBundleID: "com.attn.manager.profile",
		OutputPath:     outputPath,
	}); err != nil {
		t.Fatal(err)
	}
	var started map[string]any
	if err := decoder.Decode(&started); err != nil {
		t.Fatal(err)
	}
	if started["event"] != "started" || started["pid"] != float64(44) {
		t.Fatalf("unexpected started event: %#v", started)
	}
	if err := encoder.Encode(controlRequest{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	var finished map[string]any
	if err := decoder.Decode(&finished); err != nil {
		t.Fatal(err)
	}
	if finished["event"] != "finished" || finished["bytes"] != float64(123) {
		t.Fatalf("unexpected finished event: %#v", finished)
	}
	if session.stops != 1 {
		t.Fatalf("stop calls = %d, want 1", session.stops)
	}
	if received.WindowID != 42 || received.TargetBundleID != "com.attn.manager.profile" {
		t.Fatalf("unexpected recording request: %#v", received)
	}
	wantOutput, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput = filepath.Join(wantOutput, "clip.mp4")
	if received.OutputPath != wantOutput {
		t.Fatalf("output path = %q, want %q", received.OutputPath, wantOutput)
	}
}

func TestBrokerRejectsInvalidTokenBeforeStarting(t *testing.T) {
	started := false
	server := broker{
		token: "secret",
		start: func(string, uint32, string, string) (recorderSession, error) {
			started = true
			return nil, nil
		},
	}
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go server.serveConnection(serverConn)

	encoder := json.NewEncoder(clientConn)
	decoder := json.NewDecoder(clientConn)
	if err := encoder.Encode(startRequest{Token: "wrong", Action: "start"}); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event["event"] != "error" || event["error"] != "invalid token" {
		t.Fatalf("unexpected error event: %#v", event)
	}
	if started {
		t.Fatal("recorder started for an invalid token")
	}
}

func TestManifestContainsOnlyConnectionDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", manifestName)
	manifest := recordingManifest{Port: 43210, Token: "secret", PID: 12, StartedAt: 34}
	if err := writeManifest(path, manifest); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(contents, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 || fields["port"] != float64(43210) || fields["token"] != "secret" ||
		fields["pid"] != float64(12) || fields["started_at"] != float64(34) {
		t.Fatalf("unexpected manifest fields: %s", contents)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %o, want 600", info.Mode().Perm())
	}
}

func TestValidateOutputPath(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "clip.mp4")
	resolved, err := validateOutputPath(valid)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(resolved) != "clip.mp4" {
		t.Fatalf("resolved output = %q", resolved)
	}
	if _, err := validateOutputPath(filepath.Join(directory, "clip.mov")); err == nil {
		t.Fatal("accepted a non-mp4 output")
	}
	if err := os.WriteFile(valid, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := validateOutputPath(valid); err == nil {
		t.Fatal("accepted an existing output")
	}
}

func TestValidateBundleID(t *testing.T) {
	for _, bundleID := range []string{"com.attn.manager.profile", "com.spotify.client"} {
		if err := validateBundleID(bundleID); err != nil {
			t.Fatalf("validateBundleID(%q): %v", bundleID, err)
		}
	}
	for _, bundleID := range []string{"", "com.attn.manager\nother", "has spaces"} {
		if err := validateBundleID(bundleID); err == nil {
			t.Fatalf("validateBundleID(%q) unexpectedly succeeded", bundleID)
		}
	}
}
