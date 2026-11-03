package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/scripts/perfdaemon"
)

type request struct {
	name string
	msg  map[string]any
	cli  []string
}

type binaries []string

func (b *binaries) String() string     { return strings.Join(*b, ",") }
func (b *binaries) Set(v string) error { *b = append(*b, v); return nil }

func main() {
	profile := flag.String("profile", "", "garden profile id every request reads")
	runs := flag.Int("n", 10, "runs per command")
	show := flag.String("show", "", "also send seed_show for this seed id")
	search := flag.String("search", "", "also send seed_search for this query")
	vars := flag.String("vars", "", "report daemon CPU per request above idle from this /debug/vars URL")
	var clis binaries
	flag.Var(&clis, "cli", "run seed ls and seed ready --all through this attn binary; repeat to interleave binaries")
	flag.Parse()
	if *profile == "" {
		fail(fmt.Errorf("-profile is required: name the garden profile to read"))
	}
	if *runs < 1 {
		fail(fmt.Errorf("-n must be at least 1, got %d", *runs))
	}
	instance, err := perfdaemon.Instance("measures that instance's daemon")
	if err != nil {
		fail(err)
	}
	if *vars != "" {
		if _, err := perfdaemon.InstanceVars(instance, *vars); err != nil {
			fail(err)
		}
	}

	requests := []request{
		{"ls", map[string]any{"cmd": "seed_list"}, []string{"seed", "ls"}},
		{"ready --all", map[string]any{"cmd": "seed_ready", "all": true}, []string{"seed", "ready", "--all"}},
	}
	if *show != "" {
		requests = append(requests, request{"show", map[string]any{"cmd": "seed_show", "seed_id": *show}, nil})
	}
	if *search != "" {
		requests = append(requests, request{"search", map[string]any{"cmd": "seed_search", "query": *search}, nil})
	}
	for _, r := range requests {
		r.msg["profile_id"] = *profile
	}

	socket := config.SocketPath()
	fmt.Printf("instance %s, socket %s, profile %s, %d runs per command\n", instance, socket, *profile, *runs)
	idle := 0.0
	if *vars != "" {
		idle = awaitIdle(*vars)
		fmt.Printf("daemon idle: %.2f cpu ms/s\n", idle)
	}
	for _, r := range requests {
		measureSocket(socket, r, *runs, *vars, idle)
	}
	if len(clis) > 0 {
		measureCLI(clis, requests, *profile, *runs)
	}
}

func measureSocket(socket string, r request, runs int, vars string, idle float64) {
	_, _, reply := timed(socket, r.msg)
	var firsts, totals, cpus []time.Duration
	for range runs {
		var startCPU int64
		if vars != "" {
			startCPU = cpuMS(vars)
		}
		start := time.Now()
		first, total, last := timed(socket, r.msg)
		firsts, totals, reply = append(firsts, first), append(totals, total), last
		if vars != "" {
			above := float64(cpuMS(vars)-startCPU) - idle*time.Since(start).Seconds()
			cpus = append(cpus, time.Duration(above*float64(time.Millisecond)))
		}
	}
	line := fmt.Sprintf("socket %-11s first byte %s, total %s, %d bytes, %s",
		r.name, spread(firsts), spread(totals), len(reply), items(reply))
	if vars != "" {
		line += ", daemon cpu above idle " + spread(cpus)
	}
	fmt.Println(line)
}

func timed(socket string, msg map[string]any) (first, total time.Duration, reply []byte) {
	frame, err := json.Marshal(msg)
	if err != nil {
		fail(err)
	}
	start := time.Now()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		fail(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(start.Add(time.Minute)); err != nil {
		fail(err)
	}
	if _, err := conn.Write(append(frame, '\n')); err != nil {
		fail(err)
	}
	in := bufio.NewReaderSize(conn, 1<<20)
	if _, err := in.Peek(1); err != nil {
		fail(fmt.Errorf("%s: %w", msg["cmd"], err))
	}
	first = time.Since(start)
	reply, err = in.ReadBytes('\n')
	if err != nil {
		fail(fmt.Errorf("%s: %w", msg["cmd"], err))
	}
	total = time.Since(start)
	var head struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(reply, &head); err != nil || !head.Ok {
		fail(fmt.Errorf("%s refused: %s %v", msg["cmd"], head.Error, err))
	}
	return first, total, reply
}

func items(reply []byte) string {
	var top map[string]json.RawMessage
	_ = json.Unmarshal(reply, &top)
	var parts []string
	for _, raw := range top {
		var result map[string]json.RawMessage
		if json.Unmarshal(raw, &result) != nil {
			continue
		}
		for key, value := range result {
			var list []json.RawMessage
			if json.Unmarshal(value, &list) == nil && len(list) > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", key, len(list)))
			}
			if key == "total" || key == "matched" || key == "searched" {
				parts = append(parts, key+"="+string(value))
			}
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func measureCLI(clis []string, requests []request, profile string, runs int) {
	type sample struct{ wall, cpu []time.Duration }
	samples := map[string]*sample{}
	outBytes := map[string]int{}
	key := func(bin string, r request) string { return bin + "\x00" + r.name }
	for round := range runs + 1 {
		for _, r := range requests {
			if r.cli == nil {
				continue
			}
			for _, bin := range clis {
				wall, cpu, out := runCLI(bin, append(slices.Clone(r.cli), "--profile", profile))
				if round == 0 {
					continue
				}
				s := samples[key(bin, r)]
				if s == nil {
					s = &sample{}
					samples[key(bin, r)] = s
				}
				s.wall, s.cpu = append(s.wall, wall), append(s.cpu, cpu)
				outBytes[key(bin, r)] = out
			}
		}
	}
	for _, r := range requests {
		if r.cli == nil {
			continue
		}
		for _, bin := range clis {
			s := samples[key(bin, r)]
			fmt.Printf("cli %-11s %s: wall %s, cpu %s, %d bytes out\n", r.name, bin, spread(s.wall), spread(s.cpu), outBytes[key(bin, r)])
		}
	}
}

func runCLI(bin string, args []string) (wall, cpu time.Duration, out int) {
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	if err := cmd.Run(); err != nil {
		fail(fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, stderr.String()))
	}
	wall = time.Since(start)
	return wall, cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime(), stdout.Len()
}

func spread(xs []time.Duration) string {
	sorted := slices.Clone(xs)
	slices.Sort(sorted)
	ms := func(d time.Duration) string { return fmt.Sprintf("%.1f", float64(d)/float64(time.Millisecond)) }
	return fmt.Sprintf("median %s ms (min %s, max %s)", ms(sorted[len(sorted)/2]), ms(sorted[0]), ms(sorted[len(sorted)-1]))
}

const (
	idleCeiling = 20.0
	idleWindow  = 3 * time.Second
	idleWait    = 2 * time.Minute
)

func awaitIdle(vars string) float64 {
	var rate float64
	for start := time.Now(); time.Since(start) < idleWait; {
		if rate = idleRate(vars, idleWindow); rate <= idleCeiling {
			return rate
		}
	}
	fail(fmt.Errorf("the daemon used %.1f cpu ms/s over the last %s, above the idle ceiling of %.0f, for %s; wait for its startup work to finish or find what keeps it busy", rate, idleWindow, idleCeiling, idleWait))
	return 0
}

func idleRate(vars string, over time.Duration) float64 {
	before := cpuMS(vars)
	time.Sleep(over)
	return float64(cpuMS(vars)-before) / over.Seconds()
}

func cpuMS(vars string) int64 {
	out, err := perfdaemon.ReadVars(vars)
	if err != nil {
		fail(err)
	}
	return out.CPUTimeMS
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
