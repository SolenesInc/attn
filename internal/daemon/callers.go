package daemon

import "github.com/victorarias/attn/internal/protocol"

// resolveCallers maps the id a hook or CLI process sends for itself, ATTN_SESSION_ID, from its terminal
// to the session that terminal shows. Targets, and conversation reports for the router, keep their id.
func (d *Daemon) resolveCallers(msg any) {
	self := func(id *string) {
		if id != nil {
			*id = d.callerID(*id)
		}
	}
	switch m := msg.(type) {
	case *protocol.RegisterMessage:
		self(&m.ID)
	case *protocol.StateMessage:
		self(&m.ID)
	case *protocol.StopMessage:
		self(&m.ID)
	case *protocol.HookNotificationMessage:
		self(&m.ID)
	case *protocol.HookStopFailureMessage:
		self(&m.ID)
	case *protocol.HookCompactionMessage:
		self(&m.ID)
	case *protocol.FilesEditedMessage:
		self(&m.ID)
	case *protocol.OpenSentFilesMessage:
		self(m.SessionID)
	case *protocol.PullRequestCreatedMessage:
		self(&m.ID)
	case *protocol.PullRequestForgetMessage:
		self(&m.ID)
	case *protocol.PullRequestWatchMessage:
		self(&m.ID)
	case *protocol.PullRequestUnwatchMessage:
		self(&m.ID)
	case *protocol.AgentMsgMessage:
		self(&m.SourceSessionID)
	case *protocol.AgentCloseMessage:
		self(&m.SourceSessionID)
	case *protocol.AgentInboxMessage:
		self(&m.RecipientSessionID)
	case *protocol.AgentMsgStatusMessage:
		self(&m.SenderSessionID)
	case *protocol.SeedPlantMessage:
		self(m.SourceSessionID)
	case *protocol.SeedPlotMessage:
		self(m.SourceSessionID)
	case *protocol.SeedListMessage:
		self(m.SourceSessionID)
	case *protocol.SeedSearchMessage:
		self(m.SourceSessionID)
	case *protocol.SeedShowMessage:
		self(m.SourceSessionID)
	case *protocol.SeedArtifactTransferMessage:
		self(m.SourceSessionID)
	case *protocol.SeedTransitionMessage:
		self(m.SourceSessionID)
	case *protocol.SeedNoteMessage:
		self(m.SourceSessionID)
	case *protocol.SeedNotesMessage:
		self(m.SourceSessionID)
	case *protocol.SeedWatchMessage:
		self(&m.SourceSessionID)
	case *protocol.SeedReadyMessage:
		self(m.SourceSessionID)
	case *protocol.SeedSendToChiefMessage:
		self(m.SourceSessionID)
	case *protocol.CrewPrimeMessage:
		self(&m.SessionID)
	case *protocol.CrewHandoffMessage:
		self(&m.SessionID)
	case *protocol.WorkflowRunUpsertMessage:
		self(m.Run.SessionID)
	case *protocol.WorkflowRunListMessage:
		self(m.SessionID)
	case *protocol.DelegateMessage:
		self(m.SourceSessionID)
	case *protocol.NotebookGuideMessage:
		self(m.SessionID)
	case *protocol.JournalAppendMessage:
		self(m.SourceSessionID)
	case *protocol.PresentOpenMessage:
		self(&m.SourceSessionID)
	case *protocol.OpenMarkdownMessage:
		self(m.SessionID)
	case *protocol.OpenSeedMessage:
		self(m.SessionID)
	case *protocol.OpenBrowserMessage:
		self(m.SessionID)
	case *protocol.BrowserControlMessage:
		self(m.SessionID)
	case *protocol.RenameSessionMessage:
		self(&m.SessionID)
	case *protocol.AutoModeProposeMessage:
		self(m.ProposedBy)
	}
}
