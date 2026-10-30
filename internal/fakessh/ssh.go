package fakessh

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

func Main() {
	mode := os.Getenv("ATTN_TESTWORLD_SSH")
	if mode == "" {
		return
	}
	if mode == "master" {
		serveFakeMaster()
		os.Exit(0)
	}
	args := os.Args[1:]
	if path := os.Getenv("ATTN_TESTWORLD_SSH_CALLS"); path != "" {
		log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			panic(err)
		}
		data, _ := json.Marshal(args)
		_, err = log.Write(append(data, '\n'))
		log.Close()
		if err != nil {
			panic(err)
		}
	}

	control, action, destination, script := "", "", "", ""
	master, batch := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o":
			i++
			if i >= len(args) {
				os.Exit(2)
			}
			option := args[i]
			if strings.HasPrefix(option, "ControlPath=") {
				control = strings.TrimPrefix(option, "ControlPath=")
			}
			if option == "BatchMode=yes" {
				batch = true
			}
			if option == "ControlMaster=yes" {
				master = true
			}
		case "-O":
			i++
			action = args[i]
		default:
			if !strings.HasPrefix(args[i], "-") {
				if destination == "" {
					destination = args[i]
				} else {
					script = args[i]
				}
			}
		}
	}
	if root := os.Getenv("ATTN_TESTWORLD_SSH_CONTROL_ROOT"); root != "" {
		control = filepath.Join(root, filepath.Base(control))
	}
	fail := func(detail string) { fmt.Fprintln(os.Stderr, detail); os.Exit(255) }
	if action != "" {
		conn, err := net.Dial("unix", control)
		if err != nil {
			fail("Control socket connect: " + err.Error())
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, action+"\n")
		answer, err := io.ReadAll(conn)
		if err != nil {
			fail(err.Error())
		}
		if action == "check" && string(answer) != "running\n" {
			fail("Control master is not running")
		}
		os.Exit(0)
	}
	data, err := os.ReadFile(os.Getenv("ATTN_TESTWORLD_SSH_ROUTES"))
	if err != nil {
		fail(err.Error())
	}
	var routes map[string]string
	_ = json.Unmarshal(data, &routes)
	route := routes[destination]
	if strings.HasPrefix(route, "prompt:") {
		if batch {
			fail("Host key verification failed.")
		}
		fmt.Fprint(os.Stdout, "Are you sure you want to continue connecting (yes/no)? ")
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(answer) != "yes" {
			fail("Host key verification failed.")
		}
		route = strings.TrimPrefix(route, "prompt:")
		routes[destination] = route
		updated, _ := json.Marshal(routes)
		if err := os.WriteFile(os.Getenv("ATTN_TESTWORLD_SSH_ROUTES"), updated, 0600); err != nil {
			fail(err.Error())
		}
	}
	if strings.HasPrefix(route, "error:") {
		fail(strings.TrimPrefix(route, "error:"))
	}
	if route == "" {
		fail("ssh: Could not resolve hostname " + destination + ": nodename nor servname provided, or not known")
	}
	if master {
		binary, _ := os.Executable()
		ready, signal, _ := os.Pipe()
		cmd := exec.Command(binary)
		cmd.Env = append(os.Environ(), "ATTN_TESTWORLD_SSH=master", "ATTN_TESTWORLD_MASTER="+control, "ATTN_TESTWORLD_DESTINATION="+destination)
		cmd.ExtraFiles = []*os.File{signal}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err = cmd.Start(); err != nil {
			fail(err.Error())
		}
		signal.Close()
		var b [1]byte
		_, err = ready.Read(b[:])
		ready.Close()
		if err != nil {
			fail(err.Error())
		}
		os.Exit(0)
	}
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: control, Net: "unix"})
	if err != nil {
		fail("Control socket connect: " + err.Error())
	}
	_, _ = io.WriteString(conn, "run\n")
	result, err := os.CreateTemp(filepath.Dir(os.Getenv("ATTN_TESTWORLD_SSH_ROUTES")), "ssh-result-")
	if err != nil {
		fail(err.Error())
	}
	result.Close()
	_ = json.NewEncoder(conn).Encode(struct{ Script, ResultFile string }{script, result.Name()})
	go func() { _, _ = io.Copy(conn, os.Stdin); _ = conn.CloseWrite() }()
	_, _ = io.Copy(os.Stdout, conn)
	conn.Close()
	data, err = os.ReadFile(result.Name())
	_ = os.Remove(result.Name())
	var status struct {
		Code   int
		Stderr string
	}
	if err != nil || json.Unmarshal(data, &status) != nil {
		fail("ssh: connection to master closed")
	}
	_, _ = io.WriteString(os.Stderr, status.Stderr)
	os.Exit(status.Code)
}
func serveFakeMaster() {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: os.Getenv("ATTN_TESTWORLD_MASTER"), Net: "unix"})
	if err != nil {
		return
	}
	signal := os.NewFile(3, "ready")
	_, _ = signal.Write([]byte{1})
	signal.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer listener.Close()
	data, _ := os.ReadFile(os.Getenv("ATTN_TESTWORLD_SSH_ROUTES"))
	var routes map[string]string
	_ = json.Unmarshal(data, &routes)
	lifeline, err := net.Dial("unix", routes[os.Getenv("ATTN_TESTWORLD_DESTINATION")])
	if err != nil {
		return
	}
	defer lifeline.Close()
	_ = json.NewEncoder(lifeline).Encode(struct{ Lifeline bool }{true})
	go func() { _, _ = io.Copy(io.Discard, lifeline); cancel(); listener.Close() }()
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		var first [1]byte
		action := ""
		for {
			if _, err = conn.Read(first[:]); err != nil {
				break
			}
			if first[0] == '\n' {
				break
			}
			action += string(first[:])
		}
		if action == "exit" {
			listener.Close()
			cancel()
			workers.Wait()
			conn.Close()
			return
		}
		if action != "run" {
			if action == "check" {
				_, _ = io.WriteString(conn, "running\n")
			}
			conn.Close()
			continue
		}
		workers.Go(func() {
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			data, _ := os.ReadFile(os.Getenv("ATTN_TESTWORLD_SSH_ROUTES"))
			var routes map[string]string
			_ = json.Unmarshal(data, &routes)
			remote, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: routes[os.Getenv("ATTN_TESTWORLD_DESTINATION")], Net: "unix"})
			if err != nil {
				return
			}
			defer remote.Close()
			stopRemote := context.AfterFunc(ctx, func() { remote.Close() })
			defer stopRemote()
			input := make(chan struct{})
			go func() { defer close(input); _, _ = io.Copy(remote, conn); _ = remote.CloseWrite() }()
			_, _ = io.Copy(conn, remote)
			_ = conn.CloseWrite()
			conn.CloseRead()
			<-input
		})
	}
}
