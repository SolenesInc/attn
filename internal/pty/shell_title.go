package pty

import (
	"net/url"
	"path/filepath"
)

const shellTitleRuneLimit = 256

type ShellTitleState struct {
	InCommand    bool
	SawMarker    bool
	Command      string
	ShellTitle   string
	ProgramTitle string
	Folder       string
}

func (s *ShellTitleState) apply(seg feedSegment) {
	if m := seg.Marker; m != nil {
		s.SawMarker = true
		switch m.Kind {
		case osc133PreExec:
			s.InCommand = true
			s.Command = ""
			if m.Cmdline != nil {
				s.Command = *m.Cmdline
			}
			s.ProgramTitle = ""
		case osc133PromptStart, osc133CommandEnd:
			s.InCommand = false
			s.Command, s.ProgramTitle = "", ""
		}
	}
	switch seg.OSCCode {
	case "0", "2":
		if s.InCommand {
			s.ProgramTitle = seg.OSCValue
		} else {
			s.ShellTitle = seg.OSCValue
		}
	case "7":
		if u, err := url.Parse(seg.OSCValue); err == nil && u.Scheme == "file" && u.Path != "" {
			s.Folder = u.Path
		}
	}
}

func (s *ShellTitleState) poll(foreign bool, processName string) {
	if !s.SawMarker {
		if s.InCommand != foreign {
			s.ProgramTitle = ""
		}
		s.InCommand = foreign
		if !foreign {
			s.Command = ""
		} else {
			s.Command = processName
		}
	} else if s.InCommand && s.Command == "" {
		s.Command = processName
	}
}

func (s ShellTitleState) Title() string {
	var title string
	if s.InCommand {
		title = s.ProgramTitle
		if title == "" {
			title = s.Command
		}
	} else {
		title = s.ShellTitle
		if title == "" && s.Folder != "" {
			title = filepath.Base(s.Folder)
		}
	}
	runes := []rune(title)
	if len(runes) > shellTitleRuneLimit {
		return string(runes[:shellTitleRuneLimit])
	}
	return title
}
