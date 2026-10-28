package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/who"
)

func (d *Daemon) crewMemoryEnabled() bool {
	return parseBooleanSetting(d.homeSetting(SettingCrewMemoryEnabled))
}

func (d *Daemon) crewMemoryLock(member who.MemberKey) *sync.Mutex {
	lock, _ := d.crewMemoryLocks.LoadOrStore(member, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (d *Daemon) crewMemoryHome(member crew.Member) (string, error) {
	if err := d.requireCrewFiles(member); err != nil {
		return "", err
	}
	home := member.HomeDir(d.dataRoot)
	resolvedHome, err := config.CanonicalRuntimePath(home)
	if err != nil {
		return "", err
	}
	paths := []string{filepath.Join(home, "memory"), filepath.Join(home, "memory", "log.jsonl"), filepath.Join(home, "memory", "summaries")}
	levels, err := filepath.Glob(filepath.Join(home, "memory", "summaries", "*.jsonl"))
	if err != nil {
		return "", err
	}
	for _, path := range append(paths, levels...) {
		resolved, err := config.CanonicalRuntimePath(path)
		if err != nil {
			return "", err
		}
		if !pathWithin(resolvedHome, resolved) {
			return "", fmt.Errorf("memory path %s leaves member home %s through a symlink", path, home)
		}
	}
	return home, nil
}

func (d *Daemon) crewMemoryMember(sessionID protocol.SessionID, name, profileID string) (crew.Member, error) {
	if !d.crewMemoryEnabled() {
		return crew.Member{}, fmt.Errorf("crew memory is experimental and off: attn settings set crew_memory_enabled true")
	}
	if name == "" {
		member, bound, err := d.boundCrewMember(sessionID)
		if err != nil {
			return crew.Member{}, err
		}
		if !bound {
			return crew.Member{}, fmt.Errorf("session %s is not bound to a crew member, so it has no memory. A delegate reports on its seed: attn seed note <id> -m \"…\"", shortSessionID(sessionID))
		}
		return member, nil
	}
	b, err := d.bindings()
	if err != nil {
		return crew.Member{}, err
	}
	r, err := d.requestFromMessage(protocol.Ptr(sessionID), protocol.Ptr(profileID), b)
	if err != nil {
		return crew.Member{}, err
	}
	identity, err := d.resolveMember(r, name)
	if err != nil {
		return crew.Member{}, err
	}
	member, _, err := d.crewMember(identity.Key)
	if err != nil {
		return crew.Member{}, err
	}
	return member, nil
}

func (d *Daemon) handleCrewMemory(conn net.Conn, msg any) {
	result, err := d.crewMemoryCommand(msg)
	if err != nil {
		d.sendCrewError(conn, "memory", err)
		return
	}
	d.sendGardenResponse(conn, result)
}

func (d *Daemon) crewMemoryCommand(msg any) (protocol.Response, error) {
	var sessionID protocol.SessionID
	var name, profileID string
	read := false
	switch m := msg.(type) {
	case *protocol.CrewMemoryAddMessage:
		sessionID = protocol.Deref(m.SessionID)
	case *protocol.CrewMemoryRecallMessage:
		sessionID, name, profileID, read = protocol.Deref(m.SessionID), protocol.Deref(m.Member), protocol.Deref(m.ProfileID), true
	case *protocol.CrewMemoryZoomMessage:
		sessionID, name, profileID, read = protocol.Deref(m.SessionID), protocol.Deref(m.Member), protocol.Deref(m.ProfileID), true
	case *protocol.CrewMemorySummarizeMessage:
		sessionID = protocol.Deref(m.SessionID)
	case *protocol.CrewMemoryForgetMessage:
		sessionID = protocol.Deref(m.SessionID)
	case *protocol.CrewMemoryRewriteMessage:
		sessionID = protocol.Deref(m.SessionID)
	case *protocol.CrewMemoryImportMessage:
		sessionID = protocol.Deref(m.SessionID)
	}
	if !read {
		d.crewWakeMu.Lock()
		defer d.crewWakeMu.Unlock()
	}
	member, err := d.crewMemoryMember(protocol.TrimID(sessionID), strings.TrimSpace(name), profileID)
	if err != nil {
		return protocol.Response{}, err
	}
	home, err := d.crewMemoryHome(member)
	if err != nil {
		return protocol.Response{}, err
	}
	lock := d.crewMemoryLock(member.Key)
	lock.Lock()
	result, err := d.crewMemoryFiles(home, member, msg, read)
	lock.Unlock()
	if err != nil {
		return protocol.Response{}, err
	}
	if summarized := result.CrewMemorySummarizeResult; summarized != nil && summarized.PendingSummaries == 0 {
		if _, filed := member.FiledLetterFor(sessionID); filed && member.LetterSummary != "" {
			summarized.Handoff, err = d.crewTurnover(member, sessionID, filepath.Join(home, crew.HandoffsDirName, member.LetterFile), member.LetterClose)
		}
	}
	return result, err
}

func (d *Daemon) crewMemoryFiles(home string, member crew.Member, msg any, read bool) (protocol.Response, error) {
	var memory crew.Memory
	var err error
	if read {
		memory, err = crew.ReadMemory(home)
	} else {
		memory, err = crew.PrepareMemoryWrite(home)
	}
	if err != nil {
		return protocol.Response{}, err
	}
	if _, summarizing := msg.(*protocol.CrewMemorySummarizeMessage); summarizing && member.LetterSummary != "" && member.BindingSession == member.LetterSession {
		if _, err := reconcileCrewMemorySession(home, &memory, member, member.LetterSession, member.LetterFile, member.LetterSummary); err != nil {
			return protocol.Response{}, err
		}
	}
	response := protocol.Response{Ok: true}
	change := &protocol.CrewMemoryChangeResult{Member: member.Key.String(), Name: d.memberName(member.Key)}
	switch m := msg.(type) {
	case *protocol.CrewMemoryAddMessage:
		id, err := memory.Add(home, crew.MemoryEntry{At: time.Now().UTC(), Text: m.Text})
		if err != nil {
			return response, err
		}
		response.CrewMemoryAddResult = &protocol.CrewMemoryAddResult{Member: member.Key.String(), Name: d.memberName(member.Key), ID: strconv.Itoa(id)}
	case *protocol.CrewMemoryRecallMessage:
		result, err := recallCrewMemory(member.Key.String(), d.memberName(member.Key), memory, m.Pattern)
		if err != nil {
			return response, err
		}
		response.CrewMemoryReadResult = result
	case *protocol.CrewMemoryZoomMessage:
		result, err := zoomCrewMemory(home, member.Key.String(), d.memberName(member.Key), memory, m.Target)
		if err != nil {
			return response, err
		}
		response.CrewMemoryReadResult = result
	case *protocol.CrewMemorySummarizeMessage:
		for _, answer := range m.Answers {
			block, err := crew.ParseMemoryBlock(answer.Range)
			if err != nil {
				return response, err
			}
			if err := memory.Summarize(home, block, crew.MemorySummary{At: time.Now().UTC(), Text: answer.Text}); err != nil {
				return response, err
			}
		}
		result := &protocol.CrewMemorySummarizeResult{Member: member.Key.String(), Name: d.memberName(member.Key), PendingSummaries: len(memory.Pending()), Blocks: []protocol.CrewMemorySummaryBlock{}}
		for _, block := range memory.Ready() {
			var sources []crew.MemoryBlock
			if block.Hi-block.Lo <= crew.MemoryRawBlock {
				for i := block.Lo; i < block.Hi; i++ {
					sources = append(sources, crew.MemoryBlock{Lo: i, Hi: i + 1})
				}
			} else {
				mid := (block.Lo + block.Hi) / 2
				sources = []crew.MemoryBlock{{Lo: block.Lo, Hi: mid}, {Lo: mid, Hi: block.Hi}}
			}
			result.Blocks = append(result.Blocks, protocol.CrewMemorySummaryBlock{Range: block.String(), Lines: memory.Lines(sources)})
		}
		response.CrewMemorySummarizeResult = result
	case *protocol.CrewMemoryForgetMessage:
		id, err := strconv.Atoi(m.ID)
		if err != nil {
			return response, fmt.Errorf("%q is not an entry id", m.ID)
		}
		if err := memory.Forget(home, id); err != nil {
			return response, err
		}
		response.CrewMemoryChangeResult = change
	case *protocol.CrewMemoryRewriteMessage:
		block, err := crew.ParseMemoryBlock(m.Range)
		if err != nil {
			return response, err
		}
		if block.Hi-block.Lo < 2 {
			return response, fmt.Errorf("rewrite takes a summary range, such as 16-31; forget blanks one entry")
		}
		if _, exists := memory.Summary(block); !exists {
			return response, fmt.Errorf("no summary at %s", block)
		}
		if err := memory.Rewrite(home, block); err != nil {
			return response, err
		}
		response.CrewMemoryChangeResult = change
	case *protocol.CrewMemoryImportMessage:
		entries, err := d.crewMemoryImportEntries(member, memory, m.Lines)
		if err != nil {
			return response, err
		}
		if err := memory.Import(home, entries); err != nil {
			return response, err
		}
		change.Imported = protocol.Ptr(len(entries))
		response.CrewMemoryChangeResult = change
	}
	change.PendingSummaries = len(memory.Pending())
	return response, nil
}

func (d *Daemon) crewMemorySession(member crew.Member, sessionID protocol.SessionID, path, summary string) (int, int, error) {
	lock := d.crewMemoryLock(member.Key)
	lock.Lock()
	defer lock.Unlock()
	home, err := d.crewMemoryHome(member)
	if err != nil {
		return 0, 0, err
	}
	memory, err := crew.PrepareMemoryWrite(home)
	if err != nil {
		return 0, 0, err
	}
	id, err := reconcileCrewMemorySession(home, &memory, member, sessionID, filepath.Base(path), summary)
	return id, len(memory.Pending()), err
}

func reconcileCrewMemorySession(home string, memory *crew.Memory, member crew.Member, sessionID protocol.SessionID, letter, summary string) (int, error) {
	info, err := os.Stat(filepath.Join(home, crew.HandoffsDirName, letter))
	if err != nil {
		return 0, err
	}
	return memory.Session(home, crew.MemoryEntry{At: info.ModTime().UTC(), Text: summary, Session: &crew.MemorySession{Letter: letter, ID: string(sessionID)}})
}

func recallCrewMemory(member, name string, memory crew.Memory, pattern string) (*protocol.CrewMemoryReadResult, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, fmt.Errorf("memory recall pattern: %w", err)
	}
	result := &protocol.CrewMemoryReadResult{Member: member, Name: name, Lines: []string{}}
	matches, size := 0, 0
	for i, entry := range memory.Entries {
		if entry.Text == "" {
			continue
		}
		line := memory.Lines([]crew.MemoryBlock{{Lo: i, Hi: i + 1}})[0]
		if !re.MatchString(line) {
			continue
		}
		matches++
		result.Lines = append(result.Lines, line)
		size += len(line) + 1
		for size > crew.MemoryRecallBytes {
			size -= len(result.Lines[0]) + 1
			result.Lines = result.Lines[1:]
		}
	}
	result.Matches = protocol.Ptr(matches)
	return result, nil
}

func zoomCrewMemory(home, member, name string, memory crew.Memory, target string) (*protocol.CrewMemoryReadResult, error) {
	block, err := crew.ParseMemoryBlock(target)
	if err != nil {
		return nil, err
	}
	if block.Hi > len(memory.Entries) {
		return nil, fmt.Errorf("#%s is beyond the memory: it holds %d entries", block, len(memory.Entries))
	}
	result := &protocol.CrewMemoryReadResult{Member: member, Name: name, Lines: []string{}}
	if block.Hi-block.Lo == 1 {
		result.Lines = memory.Lines([]crew.MemoryBlock{block})
		if summary := memory.Entries[block.Lo].Session; summary != nil {
			result.Letter = protocol.Ptr(filepath.Join(home, crew.HandoffsDirName, summary.Letter))
			if summary.ID != "" {
				result.SessionID = protocol.Ptr(protocol.SessionID(summary.ID))
			}
		}
	} else {
		mid := (block.Lo + block.Hi) / 2
		result.Lines = memory.Lines([]crew.MemoryBlock{{Lo: block.Lo, Hi: mid}, {Lo: mid, Hi: block.Hi}})
	}
	return result, nil
}

func (d *Daemon) crewMemoryImportEntries(member crew.Member, memory crew.Memory, lines []protocol.CrewMemoryImportLine) ([]crew.MemoryEntry, error) {
	seen := map[string]bool{}
	for _, entry := range memory.Entries {
		if entry.Session != nil {
			seen[entry.Session.Letter] = true
		}
	}
	var entries []crew.MemoryEntry
	for _, line := range lines {
		if line.Letter == "" || filepath.Base(line.Letter) != line.Letter {
			return nil, fmt.Errorf("%q is not a letter filename", line.Letter)
		}
		if seen[line.Letter] {
			return nil, fmt.Errorf("letter %s already has a session line", line.Letter)
		}
		letter := filepath.Join(member.HomeDir(d.dataRoot), crew.HandoffsDirName, line.Letter)
		if err := d.validateCrewLetterPath(member, letter); err != nil {
			return nil, err
		}
		if _, err := os.Stat(letter); err != nil {
			return nil, err
		}
		at, err := crewHandoffTime(line.Letter)
		if err != nil {
			return nil, err
		}
		seen[line.Letter] = true
		entries = append(entries, crew.MemoryEntry{At: at, Text: line.Text, Session: &crew.MemorySession{Letter: line.Letter}})
	}
	return entries, nil
}
