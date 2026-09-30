package daemon

import (
	"io/fs"
	"strings"

	"github.com/google/uuid"
)

func conversationFileAllowed(agent, resumeID, name string) bool {
	id, err := uuid.Parse(resumeID)
	if agent != "claude" || err != nil || id.String() != resumeID || !fs.ValidPath(name) {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) >= 3 && parts[0] == ".claude" && parts[1] == "file-history" && parts[2] == resumeID {
		return true
	}
	return len(parts) >= 4 && parts[0] == ".claude" && parts[1] == "projects" &&
		(parts[3] == resumeID || (len(parts) == 4 && parts[3] == resumeID+".jsonl"))
}
