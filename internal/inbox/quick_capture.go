package inbox

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func QuickCaptureText(message, captureID string, attachments []protocol.QuickCaptureAttachment) string {
	var files strings.Builder
	for _, a := range attachments {
		fmt.Fprintln(&files, prompts.RenderText("session", "quick-capture-file", prompts.Values{"name": strconv.Quote(a.Name), "media_type": a.MediaType, "bytes": strconv.Itoa(a.Bytes), "capture_id": captureID, "attachment_id": a.ID}))
	}
	return prompts.RenderText("session", "quick-capture", prompts.Values{"message": message, "files": files.String()})
}
