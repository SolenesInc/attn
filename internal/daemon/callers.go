package daemon

import (
	"reflect"
	"strings"

	"github.com/victorarias/attn/internal/protocol"
)

// callerFields name the caller in every command that carries one; a command whose caller travels
// in id or session_id is listed in resolveCallers.
var callerFields = map[string]bool{
	"source_session_id": true, "sender_session_id": true, "recipient_session_id": true,
	"caller_session_id": true, "caller_id": true, "proposed_by": true,
}

// resolveCallers maps the id a hook or CLI process sends for itself, ATTN_SESSION_ID, from its terminal
// to the session that terminal shows. Targets, and conversation reports for the router, keep their id.
func (d *Daemon) resolveCallers(msg any) {
	self := func(id *string) {
		if id != nil {
			*id = d.callerID(*id)
		}
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Pointer && v.Elem().Kind() == reflect.Struct {
		v = v.Elem()
		for i := range v.NumField() {
			name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
			if !callerFields[name] {
				continue
			}
			switch field := v.Field(i); {
			case field.Kind() == reflect.String:
				field.SetString(d.callerID(field.String()))
			case field.Kind() == reflect.Pointer && !field.IsNil() && field.Elem().Kind() == reflect.String:
				field.Elem().SetString(d.callerID(field.Elem().String()))
			}
		}
	}
	switch m := msg.(type) {
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
	case *protocol.CrewPrimeMessage:
		self(&m.SessionID)
	case *protocol.CrewHandoffMessage:
		self(&m.SessionID)
	case *protocol.WorkflowRunUpsertMessage:
		self(m.Run.SessionID)
	case *protocol.WorkflowRunListMessage:
		self(m.SessionID)
	case *protocol.NotebookGuideMessage:
		self(m.SessionID)
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
	}
}
