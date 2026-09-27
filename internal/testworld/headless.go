package testworld

import "github.com/victorarias/attn/internal/fakeagent"

func (w *World) AnswerHeadlessTasks(answer func(*fakeagent.HeadlessTask)) {
	w.kit.AnswerHeadlessTasks(answer)
}
