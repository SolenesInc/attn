package crew

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/victorarias/attn/internal/crashpoint"
)

const MaxMemoryBytes = 280
const MemoryWakeBudget = 96
const MemoryRawBlock = 16
const MemoryRecallBytes = 20000

type MemorySession struct {
	Letter string `json:"letter"`
	ID     string `json:"id,omitempty"`
}

type MemoryEntry struct {
	At      time.Time      `json:"at"`
	Text    string         `json:"text"`
	Session *MemorySession `json:"session,omitempty"`
}

func (entry *MemoryEntry) UnmarshalJSON(body []byte) error {
	type storedEntry MemoryEntry
	var stored struct {
		storedEntry
		Legacy *struct {
			Letter  string `json:"letter"`
			Session string `json:"session"`
		} `json:"day"`
	}
	if err := json.Unmarshal(body, &stored); err != nil {
		return err
	}
	*entry = MemoryEntry(stored.storedEntry)
	if entry.Session == nil && stored.Legacy != nil {
		entry.Session = &MemorySession{Letter: stored.Legacy.Letter, ID: stored.Legacy.Session}
	}
	return nil
}

type MemorySummary struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

type MemoryBlock struct {
	Lo int
	Hi int
}

func (b MemoryBlock) String() string {
	if b.Hi == b.Lo+1 {
		return strconv.Itoa(b.Lo)
	}
	return fmt.Sprintf("%d-%d", b.Lo, b.Hi-1)
}

func ParseMemoryBlock(text string) (MemoryBlock, error) {
	left, right, ok := strings.Cut(text, "-")
	lo, err := strconv.Atoi(left)
	if err != nil || lo < 0 || lo == int(^uint(0)>>1) {
		return MemoryBlock{}, fmt.Errorf("%q is not an entry id or block", text)
	}
	if !ok {
		return MemoryBlock{lo, lo + 1}, nil
	}
	last, err := strconv.Atoi(right)
	if err == nil && last >= lo && last < int(^uint(0)>>1) {
		size := last - lo + 1
		if size >= 2 && size&(size-1) == 0 && lo%size == 0 {
			return MemoryBlock{lo, last + 1}, nil
		}
	}
	return MemoryBlock{}, fmt.Errorf("%q is not an aligned power-of-two block; copy a range such as 16-31 from the memory view", text)
}

func ValidateMemoryText(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("an entry is one nonempty line of at most %d bytes", MaxMemoryBytes)
	}
	if !utf8.ValidString(text) || strings.ContainsAny(text, "\r\n") || len(text) > MaxMemoryBytes {
		return fmt.Errorf("%d bytes; an entry is one UTF-8 line of at most %d bytes. Split it or say less", len(text), MaxMemoryBytes)
	}
	return nil
}

type Memory struct {
	Entries   []MemoryEntry
	Summaries map[int][]MemorySummary
}

func memoryLog(home string) string { return filepath.Join(home, "memory", "log.jsonl") }
func memoryLevel(home string, size int) string {
	return filepath.Join(home, "memory", "summaries", strconv.Itoa(size)+".jsonl")
}

func ReadMemory(home string) (Memory, error) {
	m := Memory{Summaries: make(map[int][]MemorySummary)}
	if err := readMemoryLines(memoryLog(home), &m.Entries); err != nil {
		return m, err
	}
	for i, entry := range m.Entries {
		if entry.At.IsZero() || (entry.Text != "" && ValidateMemoryText(entry.Text) != nil) || (entry.Session != nil && (entry.Session.Letter == "" || filepath.Base(entry.Session.Letter) != entry.Session.Letter)) {
			return m, fmt.Errorf("%s line %d: invalid memory entry", memoryLog(home), i+1)
		}
	}
	for size := 2; size <= len(m.Entries); size *= 2 {
		var level []MemorySummary
		if err := readMemoryLines(memoryLevel(home, size), &level); err != nil {
			return m, err
		}
		for i, summary := range level {
			if summary.At.IsZero() || ValidateMemoryText(summary.Text) != nil || i >= len(m.Entries)/size {
				return m, fmt.Errorf("%s line %d: invalid memory summary", memoryLevel(home, size), i+1)
			}
		}
		if level != nil {
			m.Summaries[size] = level
		}
	}
	return m, nil
}

func readMemoryLines[T any](path string, rows *[]T) error {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	*rows = []T{}
	for i, line := range bytes.SplitAfter(body, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var row T
		if line[len(line)-1] != '\n' {
			return fmt.Errorf("%s line %d: unfinished line; the next write drops this unacknowledged tail", path, i+1)
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return fmt.Errorf("%s line %d: %w", path, i+1, err)
		}
		*rows = append(*rows, row)
	}
	return nil
}

func repairMemoryTail(path string) error {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) || (err == nil && (len(body) == 0 || body[len(body)-1] == '\n')) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.Truncate(path, int64(bytes.LastIndexByte(body, '\n')+1))
}

func PrepareMemoryWrite(home string) (Memory, error) {
	if err := repairMemoryTail(memoryLog(home)); err != nil {
		return Memory{}, err
	}
	levels, err := filepath.Glob(filepath.Join(home, "memory", "summaries", "*.jsonl"))
	if err != nil {
		return Memory{}, err
	}
	for _, path := range levels {
		if err := repairMemoryTail(path); err != nil {
			return Memory{}, err
		}
	}
	return ReadMemory(home)
}

func appendMemoryLine(path string, row any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(body, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func replaceMemoryLines[T any](path string, rows []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".memory-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (m *Memory) Add(home string, entry MemoryEntry) (int, error) {
	if err := ValidateMemoryText(entry.Text); err != nil {
		return 0, err
	}
	id := len(m.Entries)
	if err := appendMemoryLine(memoryLog(home), entry); err != nil {
		return 0, err
	}
	m.Entries = append(m.Entries, entry)
	return id, nil
}

func (m Memory) Summary(b MemoryBlock) (MemorySummary, bool) {
	level := m.Summaries[b.Hi-b.Lo]
	i := b.Lo / (b.Hi - b.Lo)
	if i >= len(level) {
		return MemorySummary{}, false
	}
	return level[i], true
}

func (m Memory) Pending() []MemoryBlock {
	var blocks []MemoryBlock
	for size := 2; size <= len(m.Entries); size *= 2 {
		for k := len(m.Summaries[size]); k < len(m.Entries)/size; k++ {
			blocks = append(blocks, MemoryBlock{k * size, (k + 1) * size})
		}
	}
	return blocks
}

func (m Memory) Ready() []MemoryBlock {
	var ready []MemoryBlock
	for _, b := range m.Pending() {
		if b.Hi-b.Lo > MemoryRawBlock {
			mid := (b.Lo + b.Hi) / 2
			_, left := m.Summary(MemoryBlock{b.Lo, mid})
			_, right := m.Summary(MemoryBlock{mid, b.Hi})
			if !left || !right {
				continue
			}
		}
		ready = append(ready, b)
	}
	return ready
}

func (m *Memory) Summarize(home string, b MemoryBlock, summary MemorySummary) error {
	if err := ValidateMemoryText(summary.Text); err != nil {
		return err
	}
	if _, exists := m.Summary(b); exists {
		return fmt.Errorf("block %s is already summarized", b)
	}
	size := b.Hi - b.Lo
	if b.Hi > len(m.Entries) || size < 2 || size&(size-1) != 0 || b.Lo != len(m.Summaries[size])*size {
		return fmt.Errorf("block %s is not the next completed block at size %d", b, size)
	}
	if size > MemoryRawBlock {
		mid := (b.Lo + b.Hi) / 2
		_, left := m.Summary(MemoryBlock{b.Lo, mid})
		_, right := m.Summary(MemoryBlock{mid, b.Hi})
		if !left || !right {
			return fmt.Errorf("block %s needs both halves summarized first", b)
		}
	}
	if err := appendMemoryLine(memoryLevel(home, size), summary); err != nil {
		return err
	}
	m.Summaries[size] = append(m.Summaries[size], summary)
	return nil
}

func (m *Memory) Rewrite(home string, b MemoryBlock) error {
	if len(m.Entries) == 0 {
		return nil
	}
	for size := 1 << (bits.Len(uint(len(m.Entries))) - 1); size >= b.Hi-b.Lo; size /= 2 {
		k := b.Lo / size
		if level := m.Summaries[size]; len(level) > k {
			if err := replaceMemoryLines(memoryLevel(home, size), level[:k]); err != nil {
				return err
			}
			m.Summaries[size] = level[:k]
			crashpoint.At("crew-memory-summary-invalidated")
		}
	}
	return nil
}

func (m *Memory) Forget(home string, id int) error {
	if id < 0 || id >= len(m.Entries) {
		return fmt.Errorf("entry #%d does not exist; the memory has %d entries", id, len(m.Entries))
	}
	if err := m.Rewrite(home, MemoryBlock{id / 2 * 2, id/2*2 + 2}); err != nil {
		return err
	}
	m.Entries[id].Text = ""
	return replaceMemoryLines(memoryLog(home), m.Entries)
}

func (m *Memory) Session(home string, entry MemoryEntry) (int, error) {
	if err := ValidateMemoryText(entry.Text); err != nil {
		return 0, err
	}
	for id, old := range m.Entries {
		if old.Session == nil || old.Session.ID == "" || old.Session.ID != entry.Session.ID {
			continue
		}
		if (old.Text == entry.Text || old.Text == "") && *old.Session == *entry.Session {
			return id, nil
		}
		if err := m.Rewrite(home, MemoryBlock{id / 2 * 2, id/2*2 + 2}); err != nil {
			return 0, err
		}
		entry.At = old.At
		m.Entries[id] = entry
		return id, replaceMemoryLines(memoryLog(home), m.Entries)
	}
	return m.Add(home, entry)
}

func (m *Memory) Import(home string, entries []MemoryEntry) error {
	for _, level := range m.Summaries {
		if level != nil {
			return fmt.Errorf("memory import is closed: the first summary already exists")
		}
	}
	for _, entry := range entries {
		if err := ValidateMemoryText(entry.Text); err != nil {
			return err
		}
	}
	all := append(append([]MemoryEntry{}, m.Entries...), entries...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })
	if err := replaceMemoryLines(memoryLog(home), all); err != nil {
		return err
	}
	m.Entries = all
	return nil
}

func memoryCoverAt(total int, alpha float64) []MemoryBlock {
	root := 1 << bits.Len(uint(total-1))
	var out []MemoryBlock
	var visit func(int, int)
	visit = func(lo, hi int) {
		if lo >= total {
			return
		}
		size := hi - lo
		if size > 1 && (hi > total || float64(size) > alpha*float64(total-lo)) {
			mid := (lo + hi) / 2
			visit(lo, mid)
			visit(mid, hi)
		} else {
			out = append(out, MemoryBlock{lo, hi})
		}
	}
	visit(0, root)
	return out
}

func MemoryCover(total, budget int) []MemoryBlock {
	if total <= 0 {
		return nil
	}
	if total <= budget {
		out := make([]MemoryBlock, total)
		for i := range out {
			out[i] = MemoryBlock{i, i + 1}
		}
		return out
	}
	lo, hi := 0.0, 1.0
	for range 60 {
		mid := (lo + hi) / 2
		if len(memoryCoverAt(total, mid)) > budget {
			lo = mid
		} else {
			hi = mid
		}
	}
	out := memoryCoverAt(total, hi)
	for len(out) < budget {
		i := len(out) - 1
		for i >= 0 && out[i].Hi-out[i].Lo == 1 {
			i--
		}
		if i < 0 {
			break
		}
		b := out[i]
		mid := (b.Lo + b.Hi) / 2
		out = append(out, MemoryBlock{})
		copy(out[i+2:], out[i+1:])
		out[i], out[i+1] = MemoryBlock{b.Lo, mid}, MemoryBlock{mid, b.Hi}
	}
	return out
}

func (m Memory) Expand(blocks []MemoryBlock) []MemoryBlock {
	var out []MemoryBlock
	var expand func(MemoryBlock)
	expand = func(b MemoryBlock) {
		if b.Hi-b.Lo == 1 {
			out = append(out, b)
		} else if _, exists := m.Summary(b); exists {
			out = append(out, b)
		} else {
			mid := (b.Lo + b.Hi) / 2
			expand(MemoryBlock{b.Lo, mid})
			expand(MemoryBlock{mid, b.Hi})
		}
	}
	for _, b := range blocks {
		expand(b)
	}
	return out
}

func (m Memory) Lines(blocks []MemoryBlock) []string {
	var lines []string
	for _, b := range m.Expand(blocks) {
		if b.Hi-b.Lo == 1 {
			e := m.Entries[b.Lo]
			text := e.Text
			if text == "" {
				text = "(forgotten)"
			}
			if e.Session != nil {
				text += " [session: " + e.Session.Letter + "]"
			}
			lines = append(lines, fmt.Sprintf("#%d %s %s", b.Lo, e.At.UTC().Format(time.RFC3339), text))
		} else {
			summary, _ := m.Summary(b)
			lines = append(lines, fmt.Sprintf("#%s %s", b, summary.Text))
		}
	}
	return lines
}

func (m Memory) WakeView() string {
	lines := m.Lines(MemoryCover(len(m.Entries), MemoryWakeBudget))
	if len(lines) == 0 {
		lines = append(lines, "No memory entries yet.")
	}
	if pending := len(m.Pending()); pending != 0 {
		lines = append(lines, fmt.Sprintf("%d summaries pending; summarize at closure. Missing summaries are shown as their halves.", pending))
	}
	return strings.Join(lines, "\n")
}
