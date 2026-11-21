package inbox

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func QuickCaptureText(message string, attachments []protocol.Attachment) string {
	return prompts.RenderText("session", "quick-capture", prompts.Values{"message": message, "files": FileLines(attachments)})
}

func FileLines(attachments []protocol.Attachment) string {
	var files strings.Builder
	for _, a := range attachments {
		fmt.Fprintln(&files, prompts.RenderText("session", "quick-capture-file", prompts.Values{"name": strconv.Quote(a.Name), "media_type": a.MediaType, "bytes": strconv.Itoa(a.Bytes), "attachment_id": a.ID}))
	}
	return files.String()
}
