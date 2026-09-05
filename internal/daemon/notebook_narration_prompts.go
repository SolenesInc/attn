package daemon

import (
	"fmt"
	"strings"

	"github.com/victorarias/attn/internal/prompts"
)

func buildSummarizeSessionPrompt(transcriptPath, sessionID, rawDigestPath string) string {
	return prompts.RenderText("keeper", "summarize", prompts.Values{"transcript_path": transcriptPath, "session_id": sessionID, "raw_digest_path": rawDigestPath})
}

type narrateWorkspacePromptInputs struct {
	WorkspaceTitle      string
	WorkspaceID         string
	ContextSnapshotPath string
	RawSessionsDir      string
	TranscriptPaths     []string
	JournalPath         string
	JournalDir          string
	KnowledgeDir        string
	IsRemovalPass       bool
}

func buildNarrateWorkspacePrompt(in narrateWorkspacePromptInputs) string {
	paths := prompts.RenderText("keeper", "transcript-paths", prompts.Values{"has_paths": fmt.Sprint(len(in.TranscriptPaths) > 0), "paths": strings.Join(in.TranscriptPaths, "\n- ")})
	return prompts.RenderText("keeper", "narrate", prompts.Values{
		"workspace_title":       in.WorkspaceTitle,
		"workspace_id":          in.WorkspaceID,
		"context_snapshot_path": in.ContextSnapshotPath,
		"raw_sessions_dir":      in.RawSessionsDir,
		"transcript_paths":      paths,
		"journal_path":          in.JournalPath,
		"journal_dir":           in.JournalDir,
		"knowledge_dir":         in.KnowledgeDir,
		"is_removal_pass":       fmt.Sprint(in.IsRemovalPass),
	})
}
