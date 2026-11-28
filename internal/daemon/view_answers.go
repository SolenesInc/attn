package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// shownViewTile is a tile that shows a view file: where the file is, and the
// agent that opened it, when one did.
type shownViewTile struct {
	document tileDocument
	opener   protocol.SessionID
}

func (d *Daemon) shownViewTile(profileID, desktopID, tileID string) (shownViewTile, error) {
	desktop, err := d.store.GetDesktop(desktopID)
	if err != nil || desktop.ProfileID != profileID {
		return shownViewTile{}, fmt.Errorf("no desktop %s in this profile", desktopID)
	}
	leaf, found := tileLeafByID(desktop.Tree, tileID)
	if !found {
		return shownViewTile{}, fmt.Errorf("no tile %s on desktop %s", tileID, desktopID)
	}
	document, err := d.markdownTileDocument(leaf.TileParams)
	if err != nil {
		return shownViewTile{}, fmt.Errorf("tile %s shows no view file: %w", tileID, err)
	}
	return shownViewTile{document: document, opener: leaf.TileSessionID}, nil
}

func (d *Daemon) viewAnswersKey(document tileDocument) store.HostPath {
	return store.HostPath{Host: d.machineFor(document.host).ID(), Path: document.path}
}

// viewFilesOwner owns the files the user gives the view at key: the view
// itself, as its drafts are keyed.
func viewFilesOwner(key store.HostPath) protocol.AttachmentOwner {
	return protocol.AttachmentOwner{Kind: protocol.AttachmentOwnerKindView, ID: key.Host + ":" + key.Path}
}

// viewAnswers is what the user answered the view in document, with the files
// the answers give.
func (d *Daemon) viewAnswers(document tileDocument) (protocol.ViewAnswers, error) {
	key := d.viewAnswersKey(document)
	answers, err := d.store.ViewAnswers(key)
	if err != nil {
		return answers, err
	}
	answers.Owner = viewFilesOwner(key)
	answers.Files = []protocol.Attachment{}
	for _, draft := range answers.Drafts {
		for _, id := range draft.Attachments {
			a, err := d.store.Attachment(id)
			if err != nil {
				return answers, err
			}
			if a != nil && a.Owner == answers.Owner {
				answers.Files = append(answers.Files, a.Attachment)
			}
		}
	}
	return answers, nil
}

// showsViewOwning says whether a tile shows the view whose files owner owns.
func (d *Daemon) showsViewOwning(owner protocol.AttachmentOwner) bool {
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	for document, held := range delivery.documents {
		if held.view != nil && viewFilesOwner(d.viewAnswersKey(document)) == owner {
			return true
		}
	}
	return false
}

func (d *Daemon) handleViewAnswer(client *wsClient, msg *protocol.ViewAnswerMessage) {
	result := protocol.ViewAnswerResultMessage{Event: protocol.EventViewAnswerResult, RequestID: msg.RequestID}
	if err := d.answerView(client.selectedProfile(), msg); err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
	}
	d.sendToClient(client, result)
}

func (d *Daemon) answerView(profileID string, msg *protocol.ViewAnswerMessage) error {
	tile, err := d.shownViewTile(profileID, msg.DesktopID, msg.TileID)
	if err != nil {
		return err
	}
	if (msg.Answer.Part == nil) == (msg.Answer.Step == nil) {
		return errors.New("an answer names a question as part, or is a step on a row")
	}
	if msg.Answer.Step != nil && msg.Answer.Row == nil {
		return errors.New("a step is on a row: name it")
	}
	key := d.viewAnswersKey(tile.document)
	if err := d.checkViewFiles(profileID, viewFilesOwner(key), msg.Answer.Attachments); err != nil {
		return err
	}
	before, err := d.store.ViewAnswers(key)
	if err != nil {
		return err
	}
	if err := d.store.SaveViewAnswer(key, msg.Answer); err != nil {
		return err
	}
	for _, draft := range before.Drafts {
		if protocol.Deref(draft.Row) == protocol.Deref(msg.Answer.Row) && protocol.Deref(draft.Part) == protocol.Deref(msg.Answer.Part) {
			d.discardViewFiles(draft.Attachments, msg.Answer.Attachments)
		}
	}
	d.viewAnswersChanged(tile.document)
	return nil
}

// checkViewFiles refuses an answer whose files were not uploaded, whole, for
// the view owner owns.
func (d *Daemon) checkViewFiles(profileID string, owner protocol.AttachmentOwner, ids []string) error {
	for _, id := range ids {
		a, err := d.store.Attachment(id)
		if err != nil {
			return err
		}
		if a == nil || a.ProfileID != profileID || a.Owner != owner {
			return fmt.Errorf("attachment %s was not uploaded for this view", id)
		}
		if a.State != store.AttachmentReady && a.State != store.AttachmentCommitted {
			return fmt.Errorf("attachment %s (%q) is %s, expected ready", id, a.Attachment.Name, a.State)
		}
	}
	return nil
}

// discardViewFiles forgets the files of an answer the user replaced that the
// new answer does not keep, unless a send already gave them to the agent.
func (d *Daemon) discardViewFiles(replaced, kept []string) {
	d.attachmentMu.Lock()
	defer d.attachmentMu.Unlock()
	for _, id := range replaced {
		if slices.Contains(kept, id) {
			continue
		}
		a, err := d.store.Attachment(id)
		if err != nil || a == nil || a.State == store.AttachmentCommitted {
			continue
		}
		a.State = store.AttachmentDiscarded
		if err := d.store.SaveAttachment(*a); err != nil && !errors.Is(err, store.ErrAttachmentNotSaved) {
			d.logf("view answers: discarding the file %s: %v", id, err)
			continue
		}
		if err := d.removeAttachmentFiles(*a); err != nil {
			d.logf("view answers: removing the file %s: %v", id, err)
		}
	}
}

// viewAnswersChanged gives the tiles that show document its answers again.
func (d *Daemon) viewAnswersChanged(document tileDocument) {
	answers, err := d.viewAnswers(document)
	if err != nil {
		d.logf("view answers of %s: %v", document.path, err)
		return
	}
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	if held := delivery.documents[document]; held != nil && held.serial > 0 {
		held.answers = &answers
		held.serial++
	}
	delivery.mu.Unlock()
	d.nudgeDesktopTileContent()
}

// heldView is the view the home last read for document, if any tile shows
// it, with attn's data sets as profileID sees them, as its tiles show it.
func (d *Daemon) heldView(document tileDocument, profileID string) *protocol.ViewDocument {
	delivery := &d.desktopTiles
	delivery.mu.Lock()
	var view *protocol.ViewDocument
	if held := delivery.documents[document]; held != nil {
		view = held.view
	}
	delivery.mu.Unlock()
	if filled, _, shaped := d.withDataSets(view, profileID); shaped {
		return filled
	}
	return view
}

func (d *Daemon) handleViewSend(client *wsClient, msg *protocol.ViewSendMessage) {
	profileID := client.selectedProfile()
	d.life.Go("handleViewSend", func() {
		result := protocol.ViewSendResultMessage{Event: protocol.EventViewSendResult, RequestID: msg.RequestID}
		if sent, err := d.sendView(profileID, msg); err != nil {
			result.Error = protocol.Ptr(err.Error())
		} else {
			result.Success, result.Sent = true, &sent
		}
		d.sendToClient(client, result)
	})
}

func (d *Daemon) sendView(profileID string, msg *protocol.ViewSendMessage) (protocol.ViewSent, error) {
	tile, err := d.shownViewTile(profileID, msg.DesktopID, msg.TileID)
	if err != nil {
		return protocol.ViewSent{}, err
	}
	view := d.heldView(tile.document, profileID)
	if view == nil {
		return protocol.ViewSent{}, fmt.Errorf("%s is not shown as a view, so there is nothing to send", tile.document.path)
	}
	key := d.viewAnswersKey(tile.document)
	answers, err := d.viewAnswers(tile.document)
	if err != nil {
		return protocol.ViewSent{}, err
	}
	if err := d.store.CommitViewFiles(answers.Owner, answers.Files); err != nil {
		return protocol.ViewSent{}, err
	}
	now := time.Now().UTC()
	responses, summary := viewResponses(tile.document.path, view, answers, now)
	encoded, err := json.MarshalIndent(responses, "", "  ")
	if err != nil {
		return protocol.ViewSent{}, err
	}
	write := protocol.ViewResponsesWriteMessage{Cmd: protocol.CmdViewResponsesWrite, View: tile.document.path, Responses: string(encoded) + "\n"}
	if tile.document.host != "" {
		write.Host = protocol.Ptr(tile.document.host)
	}
	written, err := Call[protocol.ViewResponsesWritten](d.life.Context(), d.router(), write)
	if err != nil {
		return protocol.ViewSent{}, fmt.Errorf("write the responses: %w", d.tileDocumentError(tile.document.host, err))
	}
	sent := protocol.ViewSent{At: now.Format(time.RFC3339Nano), Responses: written.Path}
	if tile.opener != "" && d.store.Get(tile.opener) != nil {
		to := d.partyOfSession(tile.opener).Address()
		text := prompts.RenderText("view", "answers-sent", prompts.Values{"title": strconv.Quote(responses.View), "summary": summary, "responses": written.Path})
		if _, err := d.sendToInbox(inbox.Item{To: to, Kind: inbox.ViewAnswers, Source: tile.document.path, Text: text}); err != nil {
			return protocol.ViewSent{}, fmt.Errorf("the responses are at %s, but telling the agent failed: %w", written.Path, err)
		}
		sent.Told = true
		if holder := d.inboxHolder(to); holder != nil && holder.State != protocol.SessionStatePendingApproval {
			d.handBackTurn(holder.ID)
		}
	}
	if err := d.store.RecordViewSent(key, sent); err != nil {
		return sent, err
	}
	d.viewAnswersChanged(tile.document)
	return sent, nil
}

// viewResponsesPath is where the responses to the view at path go: beside
// it, named after it.
func viewResponsesPath(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ".responses.json"
}

func (d *Daemon) writeViewResponses(_ context.Context, msg protocol.ViewResponsesWriteMessage) (protocol.ViewResponsesWritten, error) {
	if info, err := os.Stat(msg.View); err != nil || info.IsDir() {
		return protocol.ViewResponsesWritten{}, fmt.Errorf("no view file at %s", msg.View)
	}
	path := viewResponsesPath(msg.View)
	partial, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return protocol.ViewResponsesWritten{}, err
	}
	defer os.Remove(partial.Name())
	if _, err := partial.WriteString(msg.Responses); err != nil {
		partial.Close()
		return protocol.ViewResponsesWritten{}, err
	}
	if err := partial.Close(); err != nil {
		return protocol.ViewResponsesWritten{}, err
	}
	if err := os.Chmod(partial.Name(), 0o644); err != nil {
		return protocol.ViewResponsesWritten{}, err
	}
	return protocol.ViewResponsesWritten{Path: path}, os.Rename(partial.Name(), path)
}

// viewResponsesFile is what the agent reads back: each question's answer
// outside a list of items, and each row's verdict and answers, keyed by the
// row's id.
type viewResponsesFile struct {
	View    string                      `json:"view"`
	Path    string                      `json:"path"`
	SentAt  string                      `json:"sent_at"`
	Answers map[string]any              `json:"answers"`
	Rows    map[string]*viewResponseRow `json:"rows"`
}

type viewResponseRow struct {
	Verdict string         `json:"verdict,omitempty"`
	Answers map[string]any `json:"answers"`
}

// viewRow is one item of an each or a one-by-one, as the view shows it.
type viewRow struct {
	id        string
	stepped   bool
	questions viewQuestions
}

// viewQuestions are the questions among some parts, by name.
type viewQuestions map[string]viewQuestion

type viewQuestion struct {
	several bool
	files   bool
	// valued is a question-text with a Value:, which the user may change.
	valued bool
}

func questionsAmong(parts []protocol.ViewPart) viewQuestions {
	questions := viewQuestions{}
	for _, part := range parts {
		switch {
		case part.Question != nil:
			questions[part.Question.Name] = viewQuestion{}
		case part.QuestionMulti != nil:
			questions[part.QuestionMulti.Name] = viewQuestion{several: true}
		case part.QuestionText != nil:
			questions[part.QuestionText.Name] = viewQuestion{valued: part.QuestionText.Value != nil}
		case part.QuestionFile != nil:
			questions[part.QuestionFile.Name] = viewQuestion{files: true}
		}
	}
	return questions
}

// viewResponses joins the user's answers to the view at path into its
// responses file, and says in one clause what the user did.
func viewResponses(path string, view *protocol.ViewDocument, answers protocol.ViewAnswers, now time.Time) (viewResponsesFile, string) {
	files := map[string]protocol.Attachment{}
	for _, file := range answers.Files {
		files[file.ID] = file
	}
	responses := viewResponsesFile{View: viewTitle(path, view), Path: path, SentAt: now.Format(time.RFC3339), Answers: map[string]any{}, Rows: map[string]*viewResponseRow{}}
	var rows []viewRow
	viewRows(view.Parts, newViewScope(view), &rows)
	shown := map[string]viewRow{}
	for _, row := range rows {
		shown[row.id] = row
	}
	top := questionsAmong(view.Parts)
	steps := map[string]protocol.ViewStep{}
	changedRows := map[string]bool{}
	topAnswers := 0
	for _, draft := range answers.Drafts {
		row := protocol.Deref(draft.Row)
		questions := top
		if row != "" {
			shownRow, isShown := shown[row]
			if !isShown {
				continue
			}
			questions = shownRow.questions
		}
		if draft.Step != nil {
			steps[row] = *draft.Step
			continue
		}
		part := protocol.Deref(draft.Part)
		question, asked := questions[part]
		if !asked {
			continue
		}
		if row == "" {
			responses.Answers[part] = viewAnswerValue(draft, question, files)
			topAnswers++
			continue
		}
		if responses.Rows[row] == nil {
			responses.Rows[row] = &viewResponseRow{Answers: map[string]any{}}
		}
		responses.Rows[row].Answers[part] = viewAnswerValue(draft, question, files)
		if question.valued && draft.Text != nil {
			changedRows[row] = true
		}
	}
	var kept, unseen int
	var changed, skipped []string
	stepped := false
	for _, row := range rows {
		if !row.stepped {
			continue
		}
		stepped = true
		verdict := ""
		switch {
		case steps[row.id] == protocol.ViewStepSkipped:
			verdict = "skipped"
			skipped = append(skipped, row.id)
		case changedRows[row.id]:
			verdict = "changed"
			changed = append(changed, row.id)
		case steps[row.id] == protocol.ViewStepKept:
			verdict = "kept"
			kept++
		default:
			unseen++
			continue
		}
		if responses.Rows[row.id] == nil {
			responses.Rows[row.id] = &viewResponseRow{Answers: map[string]any{}}
		}
		responses.Rows[row.id].Verdict = verdict
	}
	if !stepped {
		count := topAnswers
		for _, row := range responses.Rows {
			count += len(row.Answers)
		}
		return responses, countOf(count, "answer", "answers")
	}
	said := []string{fmt.Sprintf("%d kept", kept)}
	if len(changed) > 0 {
		said = append(said, fmt.Sprintf("%d changed (%s)", len(changed), strings.Join(changed, ", ")))
	}
	if len(skipped) > 0 {
		said = append(said, fmt.Sprintf("%d skipped (%s)", len(skipped), strings.Join(skipped, ", ")))
	}
	if unseen > 0 {
		said = append(said, fmt.Sprintf("%d not seen", unseen))
	}
	if topAnswers > 0 {
		said = append(said, countOf(topAnswers, "answer", "answers")+" besides")
	}
	return responses, strings.Join(said, ", ")
}

func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// viewAnswerValue is how the responses file says an answer: the picked label
// of a choice question, the list of labels of a question-multi, the text, or
// for a question-file the line that retrieves each file.
func viewAnswerValue(answer protocol.ViewAnswer, question viewQuestion, files map[string]protocol.Attachment) any {
	switch {
	case question.files:
		lines := []string{}
		for _, id := range answer.Attachments {
			if file, held := files[id]; held {
				lines = append(lines, strings.TrimSpace(inbox.FileLines([]protocol.Attachment{file})))
			}
		}
		return lines
	case answer.Text != nil:
		return *answer.Text
	case question.several:
		return answer.Picked
	case len(answer.Picked) > 0:
		return answer.Picked[0]
	default:
		return nil
	}
}

// viewTitle is the view's first heading, or its file's name.
func viewTitle(path string, view *protocol.ViewDocument) string {
	for _, part := range view.Parts {
		if part.Markdown == nil {
			continue
		}
		var text strings.Builder
		for _, piece := range part.Markdown.Text {
			text.WriteString(protocol.Deref(piece.Text))
		}
		for _, line := range strings.Split(text.String(), "\n") {
			if heading, isHeading := strings.CutPrefix(strings.TrimSpace(line), "# "); isHeading {
				return strings.TrimSpace(heading)
			}
		}
	}
	return filepath.Base(path)
}

// viewRows lists the rows of the view's each and one-by-one parts, in the
// order the view shows them.
func viewRows(parts []protocol.ViewPart, scope viewScope, out *[]viewRow) {
	for _, part := range parts {
		var items *protocol.ViewItems
		var blocks []protocol.ViewPart
		stepped := false
		switch {
		case part.Each != nil:
			items, blocks = &part.Each.Items, part.Each.Blocks
		case part.OneByOne != nil:
			items, blocks, stepped = &part.OneByOne.Items, part.OneByOne.Blocks, true
		default:
			continue
		}
		for n, item := range scope.items(*items) {
			*out = append(*out, viewRow{id: viewRowID(item, n), stepped: stepped, questions: questionsAmong(blocks)})
			viewRows(blocks, scope.within(item), out)
		}
	}
}

// viewRowID is how the responses name a row: its item's id, or its place
// from 1 when the item has none.
func viewRowID(item any, n int) string {
	if fields, isObject := item.(map[string]any); isObject {
		switch id := fields["id"].(type) {
		case string:
			if id != "" {
				return id
			}
		case float64:
			return strconv.FormatFloat(id, 'f', -1, 64)
		}
	}
	return strconv.Itoa(n + 1)
}

type viewScopeData struct {
	as    string
	value any
}

// viewScope is where a part reads its fields, as the app reads them: the
// items around it, innermost first, then the view's data.
type viewScope struct {
	data   []viewScopeData
	files  map[string]any
	around []any
}

func newViewScope(view *protocol.ViewDocument) viewScope {
	scope := viewScope{files: map[string]any{}}
	for _, values := range view.Values {
		for _, source := range view.Sources {
			if source.ID != values.Source {
				continue
			}
			if source.Data {
				scope.data = append(scope.data, viewScopeData{as: protocol.Deref(source.As), value: values.Values})
			}
			if source.Kind == protocol.ViewSourceKindFile {
				scope.files[source.ID] = values.Values
			}
		}
	}
	return scope
}

func (s viewScope) within(item any) viewScope {
	s.around = append(append([]any{}, s.around...), item)
	return s
}

func (s viewScope) items(items protocol.ViewItems) []any {
	var value any
	switch {
	case items.File != nil:
		value = s.files[*items.File]
	case len(items.Field) > 0:
		value = s.field(items.Field)
	}
	list, _ := value.([]any)
	return list
}

func (s viewScope) field(path []string) any {
	for i := len(s.around) - 1; i >= 0; i-- {
		if value, found := valueAt(s.around[i], path); found {
			return value
		}
	}
	for i := len(s.data) - 1; i >= 0; i-- {
		data := s.data[i]
		switch {
		case data.as != "" && path[0] == data.as:
			if value, found := valueAt(data.value, path[1:]); found {
				return value
			}
		case data.as == "":
			if value, found := valueAt(data.value, path); found {
				return value
			}
		}
	}
	return nil
}

func valueAt(root any, path []string) (any, bool) {
	value := root
	for _, name := range path {
		fields, isObject := value.(map[string]any)
		if !isObject {
			return nil, false
		}
		if value, isObject = fields[name]; !isObject {
			return nil, false
		}
	}
	return value, true
}
