package ask

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/document"
	"github.com/victorarias/attn/internal/inbox"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

const MaxAnswerBytes = 16 << 10

func Reply(ask protocol.Ask, answers []protocol.AskAnswer, note string, files []protocol.Attachment) (string, error) {
	questions := Questions(ask.Parts)
	byKey, err := answersByKey(questions, answers)
	if err != nil {
		return "", err
	}
	note = strings.TrimSpace(note)
	if len(byKey) == 0 && note == "" && len(files) == 0 {
		return "", fmt.Errorf("answer at least one question, write a note or attach a file")
	}
	onQuestion, onNote := FilesByQuestion(answers, files)
	entries := make([]string, 0, len(questions))
	for i, question := range questions {
		values := entryValues(i+1, question, byKey[question.Key], inbox.FileLines(onQuestion[question.Key]))
		entries = append(entries, prompts.RenderText("ask", "entry", values))
	}
	text := prompts.RenderText("ask", "answer", prompts.Values{
		"subject": strconv.Quote(ask.Subject),
		"ask_id":  ask.ID,
		"entries": strings.Join(entries, "\n\n"),
		"note":    blockquote(note),
		"files":   inbox.FileLines(onNote),
	})
	if len(text) > MaxAnswerBytes {
		return "", fmt.Errorf("an answer holds at most %d KiB", MaxAnswerBytes>>10)
	}
	return text, nil
}

func Answered(ask protocol.Ask) (string, error) {
	if ask.Reply == nil {
		return "", nil
	}
	questions := Questions(ask.Parts)
	byKey, err := answersByKey(questions, ask.Reply.Answers)
	if err != nil {
		return "", err
	}
	sections := make([]string, 0, len(questions)+2)
	onQuestion, onNote := FilesByQuestion(ask.Reply.Answers, ask.Reply.Attachments)
	for i, question := range questions {
		values := answerValues(question, byKey[question.Key], attachedNames(onQuestion[question.Key]))
		answer := prompts.RenderText("ask", "entry-answer", values)
		sections = append(sections, fmt.Sprintf("**%d. %s**\n\n%s", i+1, question.Prompt, answer))
	}
	if note := strings.TrimSpace(protocol.Deref(ask.Reply.Note)); note != "" {
		sections = append(sections, blockquote(note))
	}
	if names := attachedNames(onNote); names != "" {
		sections = append(sections, names)
	}
	return strings.Join(sections, "\n\n"), nil
}

func FollowUp(ask protocol.Ask, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("write the follow-up")
	}
	followUp := prompts.RenderText("ask", "follow-up", prompts.Values{
		"subject": strconv.Quote(ask.Subject),
		"ask_id":  ask.ID,
		"text":    blockquote(text),
	})
	if len(followUp) > MaxAnswerBytes {
		return "", fmt.Errorf("a follow-up holds at most %d KiB", MaxAnswerBytes>>10)
	}
	return followUp, nil
}

func answersByKey(questions []protocol.AskQuestion, answers []protocol.AskAnswer) (map[string]protocol.AskAnswer, error) {
	byKey := make(map[string]protocol.AskAnswer, len(answers))
	for _, answer := range answers {
		question, ok := questionByKey(questions, answer.Key)
		if !ok {
			return nil, fmt.Errorf("the ask has no question %q", answer.Key)
		}
		if _, dup := byKey[answer.Key]; dup {
			return nil, fmt.Errorf("question %s is answered twice", answer.Key)
		}
		answer, err := normalizeAnswer(question, answer)
		if err != nil {
			return nil, err
		}
		if answered(answer) {
			byKey[answer.Key] = answer
		}
	}
	return byKey, nil
}

func normalizeAnswer(question protocol.AskQuestion, answer protocol.AskAnswer) (protocol.AskAnswer, error) {
	picked := make([]string, 0, len(answer.Picked))
	for _, label := range answer.Picked {
		canonical, ok := document.ChoiceLabel(question.Choices, label)
		if !ok {
			return answer, fmt.Errorf("question %s has no choice %q", question.Key, label)
		}
		picked = append(picked, canonical)
	}
	answer.Picked = picked
	other := strings.TrimSpace(protocol.Deref(answer.Other))
	text := strings.TrimSpace(protocol.Deref(answer.Text))
	condition := strings.TrimSpace(protocol.Deref(answer.Condition))
	if len(answer.Attachments) > 0 && question.Kind != protocol.AskQuestionKindFile {
		return answer, fmt.Errorf("question %s takes no files", question.Key)
	}
	if condition != "" && len(picked) == 0 {
		return answer, fmt.Errorf("question %s takes a condition only on a pick", question.Key)
	}
	switch question.Kind {
	case protocol.AskQuestionKindText:
		if len(picked) > 0 || other != "" {
			return answer, fmt.Errorf("question %s takes a written answer", question.Key)
		}
	case protocol.AskQuestionKindFile:
		if len(picked) > 0 || other != "" || text != "" {
			return answer, fmt.Errorf("question %s takes files", question.Key)
		}
	case protocol.AskQuestionKindOne:
		if text != "" {
			return answer, fmt.Errorf("question %s takes a choice", question.Key)
		}
		if len(picked) > 1 || (len(picked) == 1 && other != "") {
			return answer, fmt.Errorf("question %s takes one choice", question.Key)
		}
	default:
		if text != "" {
			return answer, fmt.Errorf("question %s takes choices", question.Key)
		}
	}
	answer.Other = optional(other)
	answer.Text = optional(text)
	answer.Condition = optional(condition)
	return answer, nil
}

func entryValues(index int, question protocol.AskQuestion, answer protocol.AskAnswer, files string) prompts.Values {
	values := answerValues(question, answer, files)
	values["index"] = strconv.Itoa(index)
	values["prompt"] = question.Prompt
	return values
}

func answerValues(question protocol.AskQuestion, answer protocol.AskAnswer, files string) prompts.Values {
	return prompts.Values{
		"question_files": files,
		"picked":         pickedLabels(question, answer.Picked),
		"recommended":    strconv.FormatBool(recommendedPick(question, answer)),
		"condition":      protocol.Deref(answer.Condition),
		"other":          protocol.Deref(answer.Other),
		"text":           blockquote(protocol.Deref(answer.Text)),
		"unanswered":     strconv.FormatBool(!answered(answer)),
	}
}

func pickedLabels(question protocol.AskQuestion, picked []string) string {
	if question.Kind != protocol.AskQuestionKindAny {
		return strings.Join(picked, ", ")
	}
	quoted := make([]string, len(picked))
	for i, label := range picked {
		quoted[i] = strconv.Quote(label)
	}
	return strings.Join(quoted, ", ")
}

func recommendedPick(question protocol.AskQuestion, answer protocol.AskAnswer) bool {
	recommended := protocol.Deref(question.Recommended)
	return recommended != "" && len(answer.Picked) == 1 && answer.Other == nil && answer.Picked[0] == recommended
}

func questionByKey(questions []protocol.AskQuestion, key string) (protocol.AskQuestion, bool) {
	for _, question := range questions {
		if question.Key == key {
			return question, true
		}
	}
	return protocol.AskQuestion{}, false
}

func answered(answer protocol.AskAnswer) bool {
	return len(answer.Picked) > 0 || protocol.Deref(answer.Other) != "" || protocol.Deref(answer.Text) != "" || len(answer.Attachments) > 0
}

func FilesByQuestion(answers []protocol.AskAnswer, files []protocol.Attachment) (map[string][]protocol.Attachment, []protocol.Attachment) {
	question := map[string]string{}
	for _, answer := range answers {
		for _, id := range answer.Attachments {
			question[id] = answer.Key
		}
	}
	onQuestion := map[string][]protocol.Attachment{}
	var onNote []protocol.Attachment
	for _, file := range files {
		if key, ok := question[file.ID]; ok {
			onQuestion[key] = append(onQuestion[key], file)
		} else {
			onNote = append(onNote, file)
		}
	}
	return onQuestion, onNote
}

func attachedNames(files []protocol.Attachment) string {
	if len(files) == 0 {
		return ""
	}
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
	}
	return "Attached: " + strings.Join(names, ", ")
}

func blockquote(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight("> "+line, " ")
	}
	return strings.Join(lines, "\n")
}

func optional(text string) *string {
	if text == "" {
		return nil
	}
	return protocol.Ptr(text)
}
