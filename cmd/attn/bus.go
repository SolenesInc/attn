package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/daemon"
	"github.com/victorarias/attn/internal/store"
)

// Reads and writes the profile database directly rather than going through daemon IPC:
// the enabled bit is database-only BY DESIGN, so the kill switch works with no daemon.
func runBus() {
	if len(os.Args) < 3 || os.Args[2] == "-h" || os.Args[2] == "--help" {
		writeBusHelp(os.Stdout)
		return
	}
	switch os.Args[2] {
	case "status":
		runBusStatus(os.Args[3:])
	case "trim":
		runBusTrim(os.Args[3:])
	case "enable":
		runBusSetEnabled(os.Args[3:], true)
	case "disable":
		runBusSetEnabled(os.Args[3:], false)
	default:
		fmt.Fprintf(os.Stderr, "bus: unknown command %q\n", os.Args[2])
		writeBusHelp(os.Stderr)
		os.Exit(2)
	}
}

func writeBusHelp(w io.Writer) {
	fmt.Fprint(w, `usage: attn bus <command>

commands:
  status [--json]
        show the event log's live window, how many events it holds and what
        they weigh; every fact class writing to it with its share and its
        recent rates, loudest first; every registered consumer's cursor,
        filter, enabled bit, and lag (head - cursor, plus how long its oldest
        unread event has waited); and what is wrong with any of it.

        Whichever enabled consumer or installed app consumer sits lowest is
        tagged "(retention floor)".
        Once it has held that position past the alarm's tripwire it is tagged
        "(PINNING <size>)" instead, and the same crossing writes a warning
        notification — the log is growing for as long as that lasts.

        ATTN_BUS_PIN_ALARM_AGE moves that tripwire (a duration, or 0 to turn it
        off). The daemon reads it to decide when to warn; this command reads it
        from its own environment to decide what to tag, so set it for both when
        you move it, or the table and the notification will disagree.

  trim
        run one retention pass now instead of waiting for the daemon's hourly
        tick: drop events past the age window, and reduce the compactable fact
        classes to the newest event per subject. Both stop at the cursor floor,
        so nothing an enabled consumer or installed app has yet to read is
        removed — a lagging consumer pins the log, and bus status shows the lag.

        ATTN_BUS_RETENTION moves the age window (a duration). It is thirty days
        by default, so a pass over a database younger than that removes nothing
        however far behind a consumer is; moving it is how a trim is watched
        doing anything at all. Set it for the daemon too, or its hourly pass and
        this one keep different windows.

  disable <consumer>
        stop delivering to a consumer. Its cursor is preserved, but a disabled
        ordinary consumer no longer holds the retention window open. An
        installed app consumer retains its unread facts until the app is enabled
        or uninstalled.

  enable <consumer>
        resume delivery from wherever the consumer's cursor stands.
`)
}

type busStatusJSON struct {
	Earliest int64 `json:"earliest"`
	Head     int64 `json:"head"`
	// Bytes counts the event text — name, subject, payload, source, stamp — not the
	// database file, which is shared with every other table.
	Rows     int64  `json:"rows"`
	Bytes    int64  `json:"bytes"`
	OldestAt string `json:"oldest_at,omitempty"`
	NewestAt string `json:"newest_at,omitempty"`
	// False when the snapshot came from the database rather than the daemon that owns the
	// delivery loops, which makes each consumer `live` field meaningless.
	Delivering        bool                `json:"delivering"`
	RetentionSeconds  float64             `json:"retention_seconds"`
	SurgeRatePerHour  float64             `json:"surge_rate_per_hour"`
	SurgeWindowSecs   float64             `json:"surge_window_seconds"`
	RecentWindowSecs  float64             `json:"recent_window_seconds"`
	BaselineWindowSec float64             `json:"baseline_window_seconds"`
	PinAlarmSeconds   float64             `json:"pin_alarm_seconds"`
	Producers         []busProducerReport `json:"producers"`
	Consumers         []busConsumerReport `json:"consumers"`
	Health            []busHealthReport   `json:"health"`
}

type busProducerReport struct {
	Name               string  `json:"name"`
	Events             int64   `json:"events"`
	Bytes              int64   `json:"bytes"`
	Subjects           int64   `json:"subjects"`
	Share              float64 `json:"share"`
	RecentPerHour      float64 `json:"recent_per_hour"`
	BaselinePerHour    float64 `json:"baseline_per_hour"`
	SustainedPerHour   float64 `json:"sustained_per_hour"`
	SurgePerHour       float64 `json:"surge_per_hour"`
	SurgeWindowSeconds float64 `json:"surge_window_seconds"`
	Surging            bool    `json:"surging"`
}

type busConsumerReport struct {
	Name                string `json:"name"`
	Cursor              int64  `json:"cursor"`
	Lag                 int64  `json:"lag"`
	Filter              string `json:"filter"`
	Enabled             bool   `json:"enabled"`
	UpdatedAt           string `json:"updated_at,omitempty"`
	Live                bool   `json:"live"`
	Stalled             string `json:"stalled,omitempty"`
	OldestUnreadAt      string `json:"oldest_unread_at,omitempty"`
	HoldsRetentionFloor bool   `json:"holds_retention_floor"`
	// The flag beside it is what tells "0 bytes held" from "not measured".
	PinAlarm    bool  `json:"pin_alarm"`
	PinnedBytes int64 `json:"pinned_bytes"`
}

type busHealthReport struct {
	Level   string `json:"level"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Message string `json:"message"`
}

func busStatusReport(s bus.Status) busStatusJSON {
	out := busStatusJSON{
		Earliest:          s.Earliest,
		Head:              s.Head,
		Rows:              s.Rows,
		Bytes:             s.Bytes,
		OldestAt:          formatBusTime(s.OldestAt),
		NewestAt:          formatBusTime(s.NewestAt),
		Delivering:        s.Delivering,
		RetentionSeconds:  s.RetentionWindow.Seconds(),
		SurgeRatePerHour:  bus.SurgeRatePerHour,
		SurgeWindowSecs:   bus.SurgeWindow.Seconds(),
		RecentWindowSecs:  bus.RecentWindow.Seconds(),
		BaselineWindowSec: bus.BaselineWindow.Seconds(),
		PinAlarmSeconds:   s.PinAlarmAge.Seconds(),
		Producers:         []busProducerReport{},
		Consumers:         []busConsumerReport{},
		Health:            []busHealthReport{},
	}
	for _, p := range s.Producers {
		out.Producers = append(out.Producers, busProducerReport{
			Name: p.Name, Events: p.Events, Bytes: p.Bytes, Subjects: p.Subjects,
			Share: p.Share, RecentPerHour: p.RecentPerHour,
			BaselinePerHour: p.BaselinePerHour, SustainedPerHour: p.SustainedPerHour,
			SurgePerHour: p.SurgePerHour, SurgeWindowSeconds: p.SurgeWindow.Seconds(),
			Surging: p.Surging,
		})
	}
	for _, c := range s.Consumers {
		out.Consumers = append(out.Consumers, busConsumerReport{
			Name: c.Name, Cursor: c.Cursor, Lag: c.Lag, Filter: c.Filter,
			Enabled: c.Enabled, UpdatedAt: formatBusTime(c.UpdatedAt),
			Live: c.Live, Stalled: c.Stalled,
			OldestUnreadAt:      formatBusTime(c.OldestUnreadAt),
			HoldsRetentionFloor: c.HoldsRetentionFloor,
			PinAlarm:            c.PinAlarm,
			PinnedBytes:         c.PinnedBytes,
		})
	}
	for _, h := range s.Health {
		out.Health = append(out.Health, busHealthReport{
			Level: h.Level, Kind: h.Kind, Subject: h.Subject, Message: h.Message,
		})
	}
	return out
}

func formatBusTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func runBusStatus(args []string) {
	asJSON := false
	for _, a := range args {
		switch a {
		case "--json":
			asJSON = true
		case "-h", "--help":
			writeBusHelp(os.Stdout)
			return
		default:
			fmt.Fprintf(os.Stderr, "bus status: unknown flag %q\n", a)
			os.Exit(2)
		}
	}

	s, closeStore := openBusStore()
	defer closeStore()

	// The retention-pin tripwire comes from this process environment because it must draw
	// the line where the daemon draws it, or this table and a notification disagree.
	b := bus.New(bus.Options{
		Store:       daemon.NewBusStore(s),
		Compactable: daemon.CompactableFacts,
		Retention:   bus.RetentionFromEnv(busStderrLog),
		PinAlarmAge: bus.PinAlarmAgeFromEnv(busStderrLog),
	})
	status, err := b.Status()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus status: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		out, err := json.MarshalIndent(busStatusReport(status), "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "bus status: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}
	writeBusStatus(os.Stdout, status, time.Now())
}

// A real log carries ~50 producer classes and the tail is noise. --json never
// truncates.
const busProducerLines = 15

func writeBusStatus(w io.Writer, s bus.Status, now time.Time) {
	fmt.Fprintf(w, "log: seq %d..%d, %d event(s) holding %s",
		s.Earliest, s.Head, s.Rows, humanBytes(s.Bytes))
	if !s.OldestAt.IsZero() {
		fmt.Fprintf(w, "; oldest %s old, retention %s",
			humanAge(now.Sub(s.OldestAt)), humanAge(s.RetentionWindow))
	}
	fmt.Fprintln(w)

	if len(s.Producers) > 0 {
		fmt.Fprintf(w, "\nproducers (rates per hour over the last %s and %s):\n",
			humanAge(bus.RecentWindow), humanAge(bus.BaselineWindow))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "FACT\tEVENTS\tSHARE\tSUBJECTS\tBYTES\t1H/H\t24H/H")
		shown := s.Producers
		if len(shown) > busProducerLines {
			shown = shown[:busProducerLines]
		}
		for _, p := range shown {
			name := p.Name
			if p.Surging {
				name += " !"
			}
			fmt.Fprintf(tw, "%s\t%d\t%.1f%%\t%d\t%s\t%.0f\t%.0f\n",
				name, p.Events, p.Share*100, p.Subjects, humanBytes(p.Bytes),
				p.RecentPerHour, p.BaselinePerHour)
		}
		_ = tw.Flush()
		if rest := s.Producers[len(shown):]; len(rest) > 0 {
			var events int64
			var share float64
			for _, p := range rest {
				events += p.Events
				share += p.Share
			}
			fmt.Fprintf(w, "... and %d quieter class(es) holding %d event(s), %.1f%% of the log (--json lists every one)\n",
				len(rest), events, share*100)
		}
	}

	fmt.Fprintln(w)
	if len(s.Consumers) == 0 {
		fmt.Fprintln(w, "no registered consumers: every subscriber on this bus is ephemeral,")
		fmt.Fprintln(w, "so nothing holds a cursor and nothing pins retention.")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "CONSUMER\tCURSOR\tLAG\tOLDEST UNREAD\tENABLED\tFILTER")
		for _, c := range s.Consumers {
			name := c.Name
			switch {
			case c.PinAlarm:
				name += fmt.Sprintf(" (PINNING %s)", humanBytes(c.PinnedBytes))
			case c.HoldsRetentionFloor:
				name += " (retention floor)"
			}
			unread := "-"
			if !c.OldestUnreadAt.IsZero() {
				unread = humanAge(now.Sub(c.OldestUnreadAt))
			}
			fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%t\t%s\n",
				name, c.Cursor, c.Lag, unread, c.Enabled, c.Filter)
		}
		_ = tw.Flush()
	}

	if len(s.Health) > 0 {
		fmt.Fprintln(w)
		for _, h := range s.Health {
			fmt.Fprintf(w, "%s: %s\n", strings.ToUpper(h.Level), h.Message)
		}
	}
	if !s.Delivering {
		fmt.Fprintln(w, "\n(read from the database, not from the daemon: whether a consumer's")
		fmt.Fprintln(w, "delivery loop is actually running is not visible from here.)")
	}
}

func humanAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.0fd", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	default:
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
}

func runBusTrim(args []string) {
	for _, a := range args {
		switch a {
		case "-h", "--help":
			writeBusHelp(os.Stdout)
			return
		default:
			fmt.Fprintf(os.Stderr, "bus trim: unknown flag %q\n", a)
			os.Exit(2)
		}
	}

	s, closeStore := openBusStore()
	defer closeStore()

	before, _, err := s.BusLogSize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus trim: measuring the log: %v\n", err)
		os.Exit(1)
	}
	b := bus.New(bus.Options{
		Store:       daemon.NewBusStore(s),
		Compactable: daemon.CompactableFacts,
		Retention:   bus.RetentionFromEnv(busStderrLog),
		Log:         busStderrLog,
	})
	removed, passErr := b.Trim()
	after, bytes, err := s.BusLogSize()
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus trim: measuring the log: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("removed %d event(s); log now holds %d of %d, weighing %s\n",
		removed, after, before, humanBytes(bytes))
	// Also what a clean log prints, so a failed pass has to exit non-zero for a
	// script to tell the two apart.
	if passErr != nil {
		fmt.Fprintf(os.Stderr, "bus trim: %v\n", passErr)
		os.Exit(1)
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func runBusSetEnabled(args []string, enabled bool) {
	verb := "enable"
	if !enabled {
		verb = "disable"
	}
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(os.Stderr, "usage: attn bus %s <consumer>\n", verb)
		os.Exit(2)
	}
	name := strings.TrimSpace(args[0])

	s, closeStore := openBusStore()
	defer closeStore()

	if _, ok, err := s.GetBusConsumer(name); err != nil {
		fmt.Fprintf(os.Stderr, "bus %s: %v\n", verb, err)
		os.Exit(1)
	} else if !ok {
		fmt.Fprintf(os.Stderr, "bus %s: no consumer named %q (see `attn bus status`)\n", verb, name)
		os.Exit(1)
	}
	flipped, err := s.SetBusConsumerEnabled(name, enabled, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus %s: %v\n", verb, err)
		os.Exit(1)
	}
	if !flipped {
		fmt.Fprintf(os.Stderr, "bus %s: consumer %q was removed while this command ran, so nothing was changed (see `attn bus status`)\n", verb, name)
		os.Exit(1)
	}
	fmt.Printf("consumer %q %sd\n", name, verb)
}

// Keeps bus logging off stdout, so it never lands in the middle of --json output
// someone is parsing.
func busStderrLog(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func openBusStore() (*store.Store, func()) {
	s, err := store.NewWithDB(config.DBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "bus: opening %s: %v\n", config.DBPath(), err)
		os.Exit(1)
	}
	return s, func() { _ = s.Close() }
}
