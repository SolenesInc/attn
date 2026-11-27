package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/victorarias/attn/internal/protocol"
)

// maxViewDataBytes bounds the values of all of a view's files together, so a
// view's document stays inside one 8 MiB message (protocol.MaxSocketFrameBytes)
// beside its 1 MiB file. Measured 2026-11-28 on a generated corpus shaped as
// the transcript task's (id, file, a 300-character transcript): 12 clips are
// 4.3 KB of JSON Lines and 200 clips are 71 KB.
const maxViewDataBytes = 4 << 20

// viewSources lists the sources the parts of a view name, in the order they
// first appear, and reads the values of each file from the view's folder.
func viewSources(folder string, parts []protocol.ViewPart) ([]protocol.ViewSource, []protocol.ViewValues) {
	sources := namedViewSources(parts)
	values := []protocol.ViewValues{}
	budget := maxViewDataBytes
	for i := range sources {
		source := &sources[i]
		if source.Kind != protocol.ViewSourceKindFile {
			source.State, source.Notice = protocol.ViewSourceStateUnavailable, protocol.Ptr("not available yet")
			continue
		}
		read, size, err := readViewDataFile(filepath.Join(folder, source.ID), budget)
		if err != nil {
			source.State, source.Notice = protocol.ViewSourceStateFailed, protocol.Ptr(fmt.Sprintf("%s: %v", source.ID, err))
			continue
		}
		if _, isObject := read.(map[string]any); source.Data && source.As == nil && !isObject {
			source.State, source.Notice = protocol.ViewSourceStateFailed, protocol.Ptr(fmt.Sprintf("%s holds no JSON object, so its values need a name: add as= to its :::data part", source.ID))
			continue
		}
		budget -= size
		source.State = protocol.ViewSourceStateOk
		values = append(values, protocol.ViewValues{Source: source.ID, Values: read})
	}
	return sources, values
}

// viewDataFiles is the files of a view's folder that its parts show, so a
// change to one reaches the tile.
func viewDataFiles(folder string, parts []protocol.ViewPart) []string {
	var files []string
	for _, source := range namedViewSources(parts) {
		if source.Kind == protocol.ViewSourceKindFile {
			files = append(files, filepath.Join(folder, source.ID))
		}
	}
	return files
}

func namedViewSources(parts []protocol.ViewPart) []protocol.ViewSource {
	sources := []protocol.ViewSource{}
	at := map[string]int{}
	name := func(source protocol.ViewSource) {
		key := string(source.Kind) + ":" + source.ID
		if i, seen := at[key]; seen {
			sources[i].Data = sources[i].Data || source.Data
			if source.As != nil {
				sources[i].As = source.As
			}
			return
		}
		at[key] = len(sources)
		sources = append(sources, source)
	}
	eachViewPart(parts, func(part protocol.ViewPart) {
		if data := part.Data; data != nil {
			for kind, id := range map[protocol.ViewSourceKind]*string{
				protocol.ViewSourceKindFile:  data.File,
				protocol.ViewSourceKindRun:   data.Run,
				protocol.ViewSourceKindAttn:  data.Attn,
				protocol.ViewSourceKindStore: data.Store,
			} {
				if id != nil {
					name(protocol.ViewSource{ID: *id, Kind: kind, Data: true, As: data.As})
				}
			}
		}
		if items := viewItemsOf(part); items != nil && items.File != nil {
			name(protocol.ViewSource{ID: *items.File, Kind: protocol.ViewSourceKindFile})
		}
	})
	return sources
}

func eachViewPart(parts []protocol.ViewPart, visit func(protocol.ViewPart)) {
	for _, part := range parts {
		visit(part)
		switch {
		case part.Each != nil:
			eachViewPart(part.Each.Blocks, visit)
		case part.OneByOne != nil:
			eachViewPart(part.OneByOne.Blocks, visit)
		}
	}
}

func viewItemsOf(part protocol.ViewPart) *protocol.ViewItems {
	switch {
	case part.List != nil:
		return &part.List.Items
	case part.Table != nil:
		return &part.Table.Items
	case part.Chart != nil:
		return &part.Chart.Items
	case part.Each != nil:
		return &part.Each.Items
	case part.OneByOne != nil:
		return &part.OneByOne.Items
	}
	return nil
}

// readViewDataFile reads a .json file as one value and a .jsonl file as the
// list of its lines' values, refusing more than budget bytes.
func readViewDataFile(path string, budget int) (any, int, error) {
	content, err := readMarkdownFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(content) > budget {
		return nil, 0, fmt.Errorf("the view's files hold more than %d bytes together", maxViewDataBytes)
	}
	if filepath.Ext(path) == ".json" {
		var value any
		if err := json.Unmarshal([]byte(content), &value); err != nil {
			return nil, 0, fmt.Errorf("not JSON: %v", err)
		}
		return value, len(content), nil
	}
	items := []any{}
	lines := bufio.NewScanner(bytes.NewReader([]byte(content)))
	lines.Buffer(nil, len(content)+1)
	for number := 1; lines.Scan(); number++ {
		line := bytes.TrimSpace(lines.Bytes())
		if len(line) == 0 {
			continue
		}
		var item any
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, 0, fmt.Errorf("line %d is not JSON: %v", number, err)
		}
		items = append(items, item)
	}
	return items, len(content), nil
}
