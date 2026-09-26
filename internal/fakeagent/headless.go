package fakeagent

import (
	"context"
	"fmt"
	"os"
)

const methodHeadless = "headless"

type headlessTask struct {
	Harness Harness  `json:"harness"`
	Argv    []string `json:"argv"`
}

func answerNoHeadlessTask(cfg config, h Harness) int {
	control, err := dialControl(cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake %s: %v\n", h, err)
		return 1
	}
	if err := control.start().call(context.Background(), methodHeadless, headlessTask{Harness: h, Argv: os.Args}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "fake %s: %v\n", h, err)
	}
	control.close()
	fmt.Fprintf(os.Stderr, "fake %s answers no headless task\n", h)
	return 1
}

func (k *Kit) HeadlessTasks() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.headlessTasks
}
