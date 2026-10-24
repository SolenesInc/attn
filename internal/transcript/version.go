package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

const (
	claudeVersionTail  = 4 << 20
	firstLineSizeLimit = 1 << 20
)

func HarnessVersion(path, agent string) string {
	switch agent {
	case "claude":
		return lastClaudeVersion(path)
	case "codex":
		var line struct {
			Type    string `json:"type"`
			Payload struct {
				CLIVersion string `json:"cli_version"`
			} `json:"payload"`
		}
		if readFirstLine(path, &line) && line.Type == "session_meta" {
			return line.Payload.CLIVersion
		}
	}
	return ""
}

func lastClaudeVersion(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	start := max(info.Size()-claudeVersionTail, 0)
	tail := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(tail, start); err != nil && err != io.EOF {
		return ""
	}
	for len(tail) > 0 {
		cut := bytes.LastIndexByte(bytes.TrimRight(tail, "\n"), '\n')
		line := tail[cut+1:]
		var stamped struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(line, &stamped) == nil && stamped.Version != "" {
			return stamped.Version
		}
		if cut < 0 {
			break
		}
		tail = tail[:cut]
	}
	return ""
}

func readFirstLine(path string, into any) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	line, err := bufio.NewReader(io.LimitReader(file, firstLineSizeLimit)).ReadBytes('\n')
	if err != nil && err != io.EOF {
		return false
	}
	return json.Unmarshal(line, into) == nil
}
