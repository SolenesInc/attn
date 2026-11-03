package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func expandPickerPath(input string) (resolved string, homePath string, err error) {
	homePath, err = os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	trimmed := strings.TrimSpace(input)
	switch {
	case trimmed == "":
		return "", homePath, nil
	case trimmed == "~":
		return homePath, homePath, nil
	case strings.HasPrefix(trimmed, "~/"):
		return filepath.Join(homePath, trimmed[2:]), homePath, nil
	case strings.HasPrefix(trimmed, "~"):
		return filepath.Join(homePath, trimmed[1:]), homePath, nil
	default:
		return trimmed, homePath, nil
	}
}

func parseBrowseInput(input string) (directory string, prefix string, homePath string, err error) {
	trimmed := strings.TrimSpace(input)
	explicitDirectory := strings.HasSuffix(trimmed, string(os.PathSeparator))

	expanded, homePath, err := expandPickerPath(input)
	if err != nil || expanded == "" {
		return "", "", homePath, err
	}

	if explicitDirectory {
		return filepath.Clean(expanded), "", homePath, nil
	}

	lastSlash := strings.LastIndex(expanded, string(os.PathSeparator))
	if lastSlash == -1 {
		return "", "", homePath, nil
	}

	directory = expanded[:lastSlash+1]
	if directory == "" {
		directory = string(os.PathSeparator)
	}
	return filepath.Clean(directory), strings.ToLower(expanded[lastSlash+1:]), homePath, nil
}

func listDirectoryEntries(dirToQuery string, prefix string, extensions []string) ([]protocol.DirectoryEntry, error) {
	entries, err := os.ReadDir(dirToQuery)
	if err != nil {
		return nil, err
	}

	prefix = strings.ToLower(prefix)
	wanted := normalizeExtensions(extensions)
	var listed []protocol.DirectoryEntry
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := entry.Name()
		isDir := info.IsDir()
		switch {
		case isDir:
			if skippedDirName(name) {
				continue
			}
		case len(wanted) == 0 || !info.Mode().IsRegular() || !matchesExtension(name, wanted):
			continue
		}
		if prefix != "" && !strings.Contains(strings.ToLower(name), prefix) {
			continue
		}
		var isRepo *bool
		if isDir {
			gitInfo, err := os.Stat(filepath.Join(dirToQuery, name, ".git"))
			isRepo = protocol.Ptr(err == nil && gitInfo.IsDir())
		}
		listed = append(listed, protocol.DirectoryEntry{
			IsRepo: isRepo,
			Name:   name,
			Path:   filepath.Join(dirToQuery, name),
			IsDir:  isDir,
		})
	}

	sort.Slice(listed, func(i, j int) bool {
		if listed[i].IsDir != listed[j].IsDir {
			return listed[i].IsDir
		}
		left := strings.ToLower(listed[i].Name)
		right := strings.ToLower(listed[j].Name)
		if prefix != "" {
			leftStarts := strings.HasPrefix(left, prefix)
			rightStarts := strings.HasPrefix(right, prefix)
			if leftStarts != rightStarts {
				return leftStarts
			}
		}
		return left < right
	})

	return listed, nil
}

func inspectPickerPath(ctx context.Context, client *git.Client, input string) (*protocol.PathInspection, error) {
	resolved, homePath, err := expandPickerPath(input)
	if err != nil {
		return nil, err
	}
	resolved = git.CanonicalizePath(resolved)

	inspection := &protocol.PathInspection{
		InputPath:    input,
		ResolvedPath: resolved,
		HomePath:     protocol.Ptr(homePath),
		Exists:       false,
		IsDirectory:  false,
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return inspection, nil
		}
		return nil, err
	}

	inspection.Exists = true
	inspection.IsDirectory = info.IsDir()
	if !inspection.IsDirectory {
		return inspection, nil
	}

	repoRoot, isRepoTarget, err := client.ResolvePickerRepoTarget(ctx, resolved)
	if err != nil || !isRepoTarget || repoRoot == "" {
		return inspection, nil
	}
	inspection.RepoRoot = protocol.Ptr(repoRoot)
	return inspection, nil
}

func (d *Daemon) handleBrowseDirectoryWS(client *wsClient, msg *protocol.BrowseDirectoryMessage) {
	if len(msg.Extensions) > 0 && !client.isTrustedAppClient() {
		d.sendToClient(client, &protocol.BrowseDirectoryResultMessage{
			Event:     protocol.EventBrowseDirectoryResult,
			InputPath: msg.InputPath,
			RequestID: msg.RequestID,
			Success:   false,
			Error:     protocol.Ptr("listing files requires the authenticated attn app client"),
		})
		return
	}
	d.life.Go("handleBrowseDirectoryWS", func() {
		result, err := Call[protocol.BrowseDirectoryResultMessage](context.Background(), d.router(), *msg)
		if err != nil {
			result = protocol.BrowseDirectoryResultMessage{Event: protocol.EventBrowseDirectoryResult, InputPath: msg.InputPath, Error: protocol.Ptr(err.Error())}
		}
		result.RequestID = msg.RequestID
		d.sendToClient(client, &result)
	})
}

func (d *Daemon) browseDirectoryHere(_ context.Context, msg protocol.BrowseDirectoryMessage) (protocol.BrowseDirectoryResultMessage, error) {
	result := protocol.BrowseDirectoryResultMessage{Event: protocol.EventBrowseDirectoryResult, InputPath: msg.InputPath, Entries: []protocol.DirectoryEntry{}}
	dirToQuery, prefix, homePath, err := parseBrowseInput(msg.InputPath)
	if err != nil {
		result.Entries = nil
		result.Error = protocol.Ptr(err.Error())
		return result, nil
	}
	result.HomePath = protocol.Ptr(homePath)
	if dirToQuery == "" {
		result.Success = true
		return result, nil
	}
	result.Directory = dirToQuery
	entries, err := listDirectoryEntries(dirToQuery, prefix, msg.Extensions)
	if err != nil {
		result.Entries = nil
		result.Error = protocol.Ptr(err.Error())
		return result, nil
	}
	result.Entries, result.Success = entries, true
	return result, nil
}

func (d *Daemon) handleInspectPathWS(client *wsClient, msg *protocol.InspectPathMessage) {
	d.life.Go("handleInspectPathWS", func() {
		result, err := Call[protocol.InspectPathResultMessage](context.Background(), d.router(), *msg)
		if err != nil {
			result = protocol.InspectPathResultMessage{Event: protocol.EventInspectPathResult, Inspection: &protocol.PathInspection{InputPath: msg.Path}, Error: protocol.Ptr(err.Error())}
		}
		result.RequestID = msg.RequestID
		d.sendToClient(client, &result)
	})
}

func (d *Daemon) inspectPathHere(ctx context.Context, msg protocol.InspectPathMessage) (protocol.InspectPathResultMessage, error) {
	inspection, err := gitValue(ctx, d.gitExecution(), gitTask{Kind: gitTaskPicker, Lane: gitInteractive}, func(ctx context.Context, gitClient *git.Client) (*protocol.PathInspection, error) {
		return inspectPickerPath(ctx, gitClient, msg.Path)
	})
	if err != nil {
		return protocol.InspectPathResultMessage{}, err
	}
	return protocol.InspectPathResultMessage{Event: protocol.EventInspectPathResult, Inspection: inspection, Success: true}, nil
}
