package garden

import (
	"strings"
	"testing"
)

func TestQuestionLifecycleKeepsTheSeedTended(t *testing.T) {
	seed := Seed{
		ID: "s-7k3f9m", Status: StatusGrowing,
		TenderSession: "sess-a", TenderMember: "trellis",
	}
	asker := Tender{Session: "sess-a", Member: "trellis"}
	asked, err := ChangeQuestion(seed, QuestionAsk, "Which contract wins?", asker, "2026-09-07T10:00:00Z", "q-aaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if asked.Status != StatusGrowing || asked.TenderSession != seed.TenderSession || asked.Question == nil {
		t.Fatalf("asking moved the seed or lost the question: %+v", asked)
	}
	if asked.Question.Status != QuestionOpen || asked.Question.Text != "Which contract wins?" {
		t.Fatalf("question = %+v", asked.Question)
	}

	_, err = ChangeQuestion(asked, QuestionAsk, "A second call", asker, "later", "q-bbbbbb")
	if err == nil || !strings.Contains(err.Error(), "q-aaaaaa") || !strings.Contains(err.Error(), "Which contract wins?") {
		t.Fatalf("second ask did not name the open question: %v", err)
	}
}

func TestOnlyTheQuestionRaiserCanWithdraw(t *testing.T) {
	seed := Seed{ID: "s-7k3f9m", Status: StatusGrowing}
	asker := Tender{Session: "sess-a"}
	asked, err := ChangeQuestion(seed, QuestionAsk, "Ship today?", asker, "asked", "q-aaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeQuestion(asked, QuestionWithdraw, "", Tender{Session: "sess-b"}, "resolved", ""); err == nil || !strings.Contains(err.Error(), "sess-a") {
		t.Fatalf("another session withdrew the question: %v", err)
	}
	withdrawn, err := ChangeQuestion(asked, QuestionWithdraw, "", asker, "resolved", "")
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.Question == nil || withdrawn.Question.Status != QuestionWithdrawn || withdrawn.Question.ResolvedAt != "resolved" {
		t.Fatalf("withdrawn question = %+v", withdrawn.Question)
	}
	cleared, err := ChangeQuestion(withdrawn, QuestionClear, "", Tender{}, "", "")
	if err != nil || cleared.Question != nil {
		t.Fatalf("clear = %+v, %v", cleared.Question, err)
	}
}

func TestCrewRaiserCanWithdrawFromANewSession(t *testing.T) {
	seed := Seed{ID: "s-7k3f9m", Status: StatusGrowing}
	asked, err := ChangeQuestion(seed, QuestionAsk, "Ship?",
		Tender{Session: "old-day", Member: "trellis"}, "asked", "q-aaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeQuestion(asked, QuestionWithdraw, "",
		Tender{Session: "new-day", Member: "trellis"}, "resolved", ""); err != nil {
		t.Fatalf("the same crew raiser could not withdraw from a new session: %v", err)
	}
}

func TestAnswerAndDismissRequireAnOpenQuestionAndBody(t *testing.T) {
	seed := Seed{ID: "s-7k3f9m", Status: StatusGrowing}
	if _, err := ChangeQuestion(seed, QuestionAnswer, "yes", Tender{}, "", ""); err == nil {
		t.Fatal("answered a seed with no question")
	}
	asked, err := ChangeQuestion(seed, QuestionAsk, "Ship?", Tender{Session: "sess-a"}, "asked", "q-aaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []QuestionAction{QuestionAnswer, QuestionDismiss} {
		if _, err := ChangeQuestion(asked, action, "", Tender{}, "", ""); err == nil {
			t.Fatalf("%s accepted an empty body", action)
		}
		settled, err := ChangeQuestion(asked, action, "the call", Tender{}, "", "")
		if err != nil || settled.Question != nil {
			t.Fatalf("%s = %+v, %v", action, settled.Question, err)
		}
	}
}
