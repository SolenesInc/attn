package pty

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type titleCorpus struct {
	Name   string `json:"name"`
	Folder string `json:"folder"`
	Events []struct {
		Data    []byte `json:"data"`
		Foreign *bool  `json:"foreign"`
		Process string `json:"process"`
	} `json:"events"`
	Titles []string `json:"titles"`
}

func TestShellTitleValuesRetainOnlyTheirVisibleUnicodePrefix(t *testing.T) {
	for _, size := range []int{0, 1, 255, 256, 257, 1024} {
		title := strings.Repeat("🦊", size)
		want := strings.Repeat("🦊", min(size, 256))
		state := ShellTitleState{}
		state.apply(feedSegment{OSCCode: "0", OSCValue: title})
		if state.ShellTitle != want || state.Title() != want {
			t.Fatalf("prompt title size %d was not bounded", size)
		}
		state.apply(feedSegment{Marker: &osc133Marker{Kind: osc133PreExec, Cmdline: &title}})
		if state.Command != want || state.Title() != want {
			t.Fatalf("command title size %d was not bounded", size)
		}
		state.apply(feedSegment{OSCCode: "2", OSCValue: title})
		if state.ProgramTitle != want || state.Title() != want {
			t.Fatalf("program title size %d was not bounded", size)
		}
		state = ShellTitleState{}
		state.poll(true, title)
		if state.Command != want || state.Title() != want {
			t.Fatalf("foreground title size %d was not bounded", size)
		}
		state.apply(feedSegment{OSCCode: "7", OSCValue: "file:///tmp/" + title})
		if size > 0 && state.Folder != want {
			t.Fatalf("folder title size %d was not bounded", size)
		}
	}
}

func TestShellTitleCorpus(t *testing.T) {
	files, err := filepath.Glob("testdata/shell-titles/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("shell title corpus is empty")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var corpus titleCorpus
		if err := json.Unmarshal(data, &corpus); err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{0, 1} {
			t.Run(corpus.Name+map[int]string{0: "/recorded", 1: "/bytewise"}[size], func(t *testing.T) {
				state := ShellTitleState{Folder: corpus.Folder}
				titles := []string{state.Title()}
				record := func() {
					if title := state.Title(); title != titles[len(titles)-1] {
						titles = append(titles, title)
					}
				}
				var scanner feedSegmenter
				for _, event := range corpus.Events {
					if event.Foreign != nil {
						state.poll(*event.Foreign, event.Process)
						record()
						continue
					}
					feed := func(data []byte) { scanner.Feed(data, func(seg feedSegment) { state.apply(seg); record() }) }
					if size == 0 {
						feed(event.Data)
					} else {
						for _, b := range event.Data {
							feed([]byte{b})
						}
					}
				}
				if !reflect.DeepEqual(titles, corpus.Titles) {
					t.Fatalf("title timeline:\n got %q\nwant %q", titles, corpus.Titles)
				}
			})
		}
	}
}
