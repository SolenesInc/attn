package daemon

import (
	"bufio"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	// Measured: a row is ~450 bytes, so 500 rows is ~225 KB in one message.
	pastConversationsLimit = 500

	// Receipt (2026-08-09, pi 0.83.0, a real session file): 4 lines, 637 bytes
	// precede the first user message. 256 KB is a tripwire.
	pastConversationHeadBytes = 256 << 10

	pastConversationPreviewRunes = 200
)

type pastConversationFile struct {
	sessionID string
	path      string
	modified  int
	bytes     int
}

func (d *Daemon) listPastConversations() ([]protocol.PastConversation, bool) {
	return d.listPastConversationsIn(filepath.Join(config.DataDir(), "hosts", "state"))
}

func (d *Daemon) listPastConversationsIn(root string) ([]protocol.PastConversation, bool) {
	files := collectPastConversationFiles(root)
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified != files[j].modified {
			return files[i].modified > files[j].modified
		}
		return files[i].path < files[j].path
	})
	truncated := len(files) > pastConversationsLimit
	if truncated {
		files = files[:pastConversationsLimit]
	}
	conversations := make([]protocol.PastConversation, 0, len(files))
	for _, file := range files {
		cwd, preview := readPastConversationHead(file.path)
		conversations = append(conversations, protocol.PastConversation{
			SessionID: file.sessionID,
			File:      file.path,
			Cwd:       cwd,
			Preview:   preview,
			Modified:  file.modified,
			Bytes:     file.bytes,
			Live:      d.isHostSession(file.sessionID),
		})
	}
	return conversations, truncated
}

func collectPastConversationFiles(root string) []pastConversationFile {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var files []pastConversationFile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		sessionFiles, err := os.ReadDir(filepath.Join(root, sessionID))
		if err != nil {
			continue
		}
		for _, sessionFile := range sessionFiles {
			if sessionFile.IsDir() || !strings.HasSuffix(sessionFile.Name(), ".jsonl") {
				continue
			}
			info, err := sessionFile.Info()
			if err != nil || !isRegular(info) || info.Size() == 0 {
				continue
			}
			files = append(files, pastConversationFile{
				sessionID: sessionID,
				path:      filepath.Join(root, sessionID, sessionFile.Name()),
				modified:  int(info.ModTime().UnixMilli()),
				bytes:     int(info.Size()),
			})
		}
	}
	return files
}

func isRegular(info fs.FileInfo) bool { return info.Mode().IsRegular() }

func readPastConversationHead(path string) (cwd string, preview string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(&io.LimitedReader{R: file, N: pastConversationHeadBytes})
	scanner.Buffer(make([]byte, 0, 64<<10), pastConversationHeadBytes)
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Cwd     string `json:"cwd"`
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		switch entry.Type {
		case "session":
			cwd = entry.Cwd
		case "message":
			if entry.Message.Role != "user" {
				continue
			}
			for _, part := range entry.Message.Content {
				if part.Type != "text" {
					continue
				}
				if text := strings.TrimSpace(part.Text); text != "" {
					return cwd, clipPreview(text)
				}
			}
		}
	}
	return cwd, ""
}

func clipPreview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= pastConversationPreviewRunes {
		return text
	}
	return string(runes[:pastConversationPreviewRunes]) + "..."
}

func (d *Daemon) handleListPastConversations(client *wsClient, msg *protocol.ListPastConversationsMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdListPastConversations, "list_past_conversations is missing a request id")
		return
	}
	conversations, truncated := d.listPastConversations()
	d.sendToClient(client, &protocol.PastConversationsResultMessage{
		Event:         protocol.EventPastConversationsResult,
		RequestID:     requestID,
		Success:       true,
		Conversations: conversations,
		Truncated:     truncated,
	})
}
