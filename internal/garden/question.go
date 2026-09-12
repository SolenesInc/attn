package garden

import (
	"fmt"
	"strings"
)

type Question struct {
	ID             string `json:"id"`
	Text           string `json:"text"`
	AskedAt        string `json:"asked_at"`
	AskedBySession string `json:"asked_by_session"`
	AskedByMember  string `json:"asked_by_member"`
	Status         string `json:"status"`
	ResolvedAt     string `json:"resolved_at,omitempty"`
}

const (
	QuestionOpen      = "open"
	QuestionWithdrawn = "withdrawn"
)

type QuestionAction string

const (
	QuestionAsk      QuestionAction = "ask"
	QuestionAnswer   QuestionAction = "answer"
	QuestionDismiss  QuestionAction = "dismiss"
	QuestionWithdraw QuestionAction = "withdraw"
	QuestionClear    QuestionAction = "clear"
)

var QuestionActions = []QuestionAction{
	QuestionAsk, QuestionAnswer, QuestionDismiss, QuestionWithdraw, QuestionClear,
}

func ParseQuestionAction(raw string) (QuestionAction, error) {
	action := QuestionAction(strings.TrimSpace(strings.ToLower(raw)))
	for _, known := range QuestionActions {
		if action == known {
			return action, nil
		}
	}
	return "", fmt.Errorf("%q is not a question action; the actions are ask, answer, dismiss, withdraw, clear", raw)
}

func ChangeQuestion(seed Seed, action QuestionAction, text string, actor Tender, at, questionID string) (Seed, error) {
	text = strings.TrimSpace(text)
	if action == QuestionAsk {
		if Closed(seed.Status) {
			return Seed{}, fmt.Errorf("%s is %s and cannot ask a question; replant it first: attn seed replant %s", seed.ID, seed.Status, seed.ID)
		}
		if !actor.Named() {
			return Seed{}, fmt.Errorf("asking on %s records who raised it; run it from an attn session, or pass --member <name>", seed.ID)
		}
		if seed.Question != nil && seed.Question.Status == QuestionOpen {
			return Seed{}, fmt.Errorf("%s already has open question %s: %q; answer, dismiss, or withdraw it before asking another", seed.ID, seed.Question.ID, TrimReason(seed.Question.Text))
		}
		if err := validateQuestionText(action, text, seed.ID); err != nil {
			return Seed{}, err
		}
		next := seed
		next.Question = &Question{
			ID: questionID, Text: text, AskedAt: at,
			AskedBySession: actor.Session, AskedByMember: actor.Member,
			Status: QuestionOpen,
		}
		return next, nil
	}

	if seed.Question == nil {
		return Seed{}, fmt.Errorf("%s has no question to %s", seed.ID, action)
	}
	question := *seed.Question
	switch action {
	case QuestionAnswer, QuestionDismiss:
		if question.Status != QuestionOpen {
			return Seed{}, fmt.Errorf("question %s on %s is %s and cannot be %sed", question.ID, seed.ID, question.Status, action)
		}
		if err := validateQuestionText(action, text, seed.ID); err != nil {
			return Seed{}, err
		}
		next := seed
		next.Question = nil
		return next, nil
	case QuestionWithdraw:
		if question.Status != QuestionOpen {
			return Seed{}, fmt.Errorf("question %s on %s is already %s", question.ID, seed.ID, question.Status)
		}
		if !question.RaisedBy(actor) {
			return Seed{}, fmt.Errorf("only %s, who raised question %s, can withdraw it", question.Asker().DisplayName(), question.ID)
		}
		question.Status = QuestionWithdrawn
		question.ResolvedAt = at
		next := seed
		next.Question = &question
		return next, nil
	case QuestionClear:
		if question.Status != QuestionWithdrawn {
			return Seed{}, fmt.Errorf("question %s on %s is %s; Clear is only for a withdrawn question", question.ID, seed.ID, question.Status)
		}
		next := seed
		next.Question = nil
		return next, nil
	default:
		return Seed{}, fmt.Errorf("%q is not a question action", action)
	}
}

func validateQuestionText(action QuestionAction, text, seedID string) error {
	if text == "" {
		switch action {
		case QuestionAsk:
			return fmt.Errorf("asking on %s needs the question: attn seed ask %s -m \"what needs a human call?\"", seedID, seedID)
		case QuestionAnswer:
			return fmt.Errorf("answering %s needs the answer: attn seed answer %s -m \"the decision\"", seedID, seedID)
		case QuestionDismiss:
			return fmt.Errorf("dismissing the question on %s records why it is the wrong ask: attn seed dismiss %s -m \"why\"", seedID, seedID)
		}
	}
	if n := len(text); n > MaxNoteBytes {
		return fmt.Errorf("that %s is %d bytes and the limit is %d; put supporting detail on the seed log", action, n, MaxNoteBytes)
	}
	return nil
}

func (q Question) Asker() Tender {
	return Tender{Session: q.AskedBySession, Member: q.AskedByMember}
}

func (q Question) RaisedBy(actor Tender) bool {
	if q.AskedByMember != "" && actor.Member != "" {
		return q.AskedByMember == actor.Member
	}
	return q.AskedBySession != "" && q.AskedBySession == actor.Session
}
