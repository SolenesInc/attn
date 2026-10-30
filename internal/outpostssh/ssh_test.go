package outpostssh_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/fakeagent"
	"github.com/victorarias/attn/internal/outpostssh"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "outpostssh-tests-")
	if err != nil {
		panic(err)
	}
	config.ScopeTestEnvironment(dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func commandWorld(t *testing.T) (*outpostssh.Client, func() [][]string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nATTN_SSH_COMMAND_FAKE=1 exec \"" + binary + "\" -test.run=TestSSHCommandProcess -- \"$@\"\n"
	if err = os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("ATTN_SSH_ARGV_LOG", log)
	client := outpostssh.For("user@machine", filepath.Join(dir, "master.sock"), "named")
	return client, func() [][]string {
		data, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		var rows [][]string
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var argv []string
			if err = json.Unmarshal([]byte(line), &argv); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, argv)
		}
		return rows
	}
}
func TestSSHCommandProcess(t *testing.T) {
	if os.Getenv("ATTN_SSH_COMMAND_FAKE") == "" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	file, _ := os.OpenFile(os.Getenv("ATTN_SSH_ARGV_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	_ = json.NewEncoder(file).Encode(args)
	file.Close()
	if sample := os.Getenv("ATTN_SSH_FAILURE"); sample != "" {
		_, _ = io.WriteString(os.Stderr, sample)
		os.Exit(255)
	}
	for i, arg := range args {
		if arg == "-O" && args[i+1] == "check" {
			os.Exit(255)
		}
	}
	last := args[len(args)-1]
	if socket := os.Getenv("ATTN_SSH_STDERR_HOLDER"); socket != "" && strings.Contains(last, "_bridge") {
		control, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
		if err != nil {
			panic(err)
		}
		if _, _, err := control.WriteMsgUnix([]byte{1}, syscall.UnixRights(int(os.Stderr.Fd())), nil); err != nil {
			panic(err)
		}
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			panic(err)
		}
		os.Exit(0)
	}
	if strings.Contains(last, "uname -sm") {
		_, _ = io.WriteString(os.Stdout, "Linux x86_64\n{\"version\":\"test\",\"sourceFingerprint\":\"git:test\",\"protocolVersion\":\"test\"}\nb false\n")
	} else if strings.Contains(last, "_bridge") {
		_, _ = io.Copy(os.Stdout, os.Stdin)
	} else {
		_, _ = io.WriteString(os.Stdout, "installed\n")
	}
	os.Exit(0)
}
func TestSSHCommands(t *testing.T) {
	client, commands := commandWorld(t)
	ctx := context.Background()
	if err := os.WriteFile(client.ControlPath, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureMaster(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(client.ControlPath); !os.IsNotExist(err) {
		t.Fatalf("stale control socket remains: %v", err)
	}
	probe, err := client.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if probe.Platform != "linux/amd64" || probe.Build == nil || probe.Build.SourceFingerprint != "git:test" || probe.Build.ProtocolVersion != "test" {
		t.Fatalf("probe: %+v", probe)
	}
	if _, err := client.Run(ctx, "echo hello"); err != nil {
		t.Fatal(err)
	}
	var lines []string
	if err := client.Stream(ctx, "cat > /dev/null; echo installed", strings.NewReader("binary"), func(line string) { lines = append(lines, line) }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lines, []string{"installed"}) {
		t.Fatal(lines)
	}
	conn, err := client.Bridge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	_ = conn.(interface{ CloseWrite() error }).CloseWrite()
	out, err := io.ReadAll(conn)
	if err != nil || string(out) != "hello" {
		t.Fatalf("pipe: %q %v", out, err)
	}
	conn.Close()
	if err := client.ExitMaster(ctx); err != nil {
		t.Fatal(err)
	}
	rows := commands()
	common := []string{"-o", "ControlPath=" + client.ControlPath, "-o", "RemoteCommand=none", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "BatchMode=yes"}
	master := append(append([]string{}, common...), "-o", "ControlMaster=yes", "-o", "ControlPersist=yes", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4", "-N", "-T", "-a", "-x", "user@machine")
	if !reflect.DeepEqual(rows[0], append(append([]string{}, common...), "-O", "check", "user@machine")) || !reflect.DeepEqual(rows[1], master) || !reflect.DeepEqual(rows[len(rows)-1], append(append([]string{}, common...), "-O", "exit", "user@machine")) {
		t.Fatalf("master commands: %q", rows)
	}
	for _, argv := range rows[2 : len(rows)-1] {
		expected := append(append([]string{}, common...), "-o", "ControlMaster=no", "-T", "-a", "-x", "user@machine")
		if !reflect.DeepEqual(argv[:len(argv)-1], expected) {
			t.Fatalf("remote argv: %q", argv)
		}
		script := argv[len(argv)-1]
		if !strings.HasPrefix(script, "sh -c '") || strings.Count(script, "'") != 2 || !strings.Contains(script, `export ATTN_INSTANCE="named";`) {
			t.Fatalf("remote script: %s", script)
		}
	}
	interactive := client.MasterCommand()
	for _, arg := range interactive {
		if strings.HasPrefix(arg, "BatchMode=") {
			t.Fatal(interactive)
		}
	}
}
func TestSSHFailures(t *testing.T) {
	samples, err := filepath.Glob("testdata/*.stderr")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range samples {
		t.Run(filepath.Base(path), func(t *testing.T) {
			client, _ := commandWorld(t)
			sample, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("ATTN_SSH_FAILURE", string(sample))
			_, err = client.Run(context.Background(), "true")
			var failure *outpostssh.Error
			if !errors.As(err, &failure) || failure.ExitCode != 255 || failure.Stderr != string(sample) {
				t.Fatalf("failure: %v", err)
			}
			conn, err := client.Bridge(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.ReadAll(conn)
			conn.Close()
			if !errors.As(err, &failure) || failure.ExitCode != 255 || failure.Stderr != string(sample) {
				t.Fatalf("bridge failure: %v", err)
			}
			want := strings.HasPrefix(filepath.Base(path), "auth-") || strings.HasPrefix(filepath.Base(path), "host-")
			if failure.Connectable() != want {
				t.Fatalf("connectable=%v want=%v: %s", failure.Connectable(), want, sample)
			}
		})
	}
}

func TestBridgeCloseDoesNotWaitForOtherStderrHolders(t *testing.T) {
	client, _ := commandWorld(t)
	dir, err := os.MkdirTemp("", "ssh-fd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "holder.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("ATTN_SSH_STDERR_HOLDER", socket)
	conn, err := client.Bridge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	control, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	oob := make([]byte, syscall.CmsgSpace(4))
	_, size, _, _, err := control.ReadMsgUnix([]byte{0}, oob)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := syscall.ParseSocketControlMessage(oob[:size])
	if err != nil || len(messages) != 1 {
		t.Fatalf("stderr descriptor messages=%v: %v", messages, err)
	}
	fds, err := syscall.ParseUnixRights(&messages[0])
	if err != nil || len(fds) != 1 {
		t.Fatalf("stderr descriptors=%v: %v", fds, err)
	}
	held := os.NewFile(uintptr(fds[0]), "external SSH stderr holder")
	t.Cleanup(func() { held.Close(); conn.Close() })
	if err := conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(control); err != nil {
		t.Fatal(err)
	}
	t.Log("SSH command exited; independent stderr writer remains open")
	finished := make(chan error, 1)
	started := time.Now()
	go func() { finished <- conn.Close() }()
	guard := time.NewTimer(fakeagent.HangGuard)
	defer guard.Stop()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Bridge.Close returned in %s while independent stderr writer remains open", time.Since(started))
	case <-guard.C:
		t.Errorf("Bridge.Close did not return within existing HangGuard=%s while independent stderr writer remained open", fakeagent.HangGuard)
		held.Close()
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		t.Logf("Bridge.Close returned after independent stderr writer release, elapsed %s", time.Since(started))
	}
}
