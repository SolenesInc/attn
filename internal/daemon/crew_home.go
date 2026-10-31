package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) notebookCrewHomes(profileID string) (string, error) {
	root, _, err := d.ensureNotebookScaffold(profileID)
	if err != nil {
		return "", fmt.Errorf("prepare the Notebook for crew homes: %w", err)
	}
	return filepath.Join(root, crew.HomesDirName), nil
}

func reserveCrewHome(parent, name string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	base := filepath.Join(parent, strings.ToLower(name))
	candidate := base
	for n := 2; ; n++ {
		err := os.Mkdir(candidate, 0o755)
		if err == nil {
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
}

type crewHomeCommit func(store.CrewHomeMove) ([]store.DocumentWriteResult, error)

func (d *Daemon) relocateCrewHome(member crew.Member, revision int64, parent, name string, commit crewHomeCommit) (crew.Member, int64, error) {
	if d.crewBindingLive(member) {
		return member, revision, fmt.Errorf("%s is awake and works from its home; put it to sleep first: attn crew sleep %s", member.Name, member.Name)
	}
	if err := d.requireCrewFiles(member); err != nil {
		return member, revision, err
	}
	previous := member.HomeDir(d.dataRoot)
	source, err := config.CanonicalRuntimePath(previous)
	if err != nil {
		return member, revision, err
	}
	destination, err := config.CanonicalRuntimePath(parent)
	if err != nil {
		return member, revision, err
	}
	if pathWithin(source, destination) {
		return member, revision, fmt.Errorf("cannot move %s's home into %s, which is inside it", member.Name, parent)
	}
	home, err := reserveCrewHome(parent, name)
	if err != nil {
		return member, revision, err
	}
	moved := member
	moved.Home = home
	moved.CWD = rebaseCrewPath(previous, home, member.CWD)
	moved.AwarenessDirs = make([]string, len(member.AwarenessDirs))
	for i, dir := range member.AwarenessDirs {
		moved.AwarenessDirs[i] = rebaseCrewPath(previous, home, dir)
	}
	schema, err := d.crewCollection()
	if err != nil {
		_ = os.Remove(home)
		return member, revision, err
	}
	body, err := moved.Encode()
	if err != nil {
		_ = os.Remove(home)
		return member, revision, err
	}
	fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.ID, false)
	documents := []store.DocumentCommit{{Write: store.DocumentWrite{Schema: *schema, ID: member.ID, Body: body, Expected: &revision}, Fact: fact}}
	dispatches, err := d.dispatchesFollowingHome(previous, home)
	if err != nil {
		_ = os.Remove(home)
		return member, revision, fmt.Errorf("find the saved sessions that move with %s's home: %w", member.Name, err)
	}
	documents = append(documents, dispatches...)
	if err := syscall.Rename(source, home); err != nil {
		_ = os.Remove(home)
		if errors.Is(err, syscall.EXDEV) {
			return member, revision, fmt.Errorf("cannot move %s's home from %s to %s: they are on different volumes, and attn moves a home only within one volume; nothing changed", member.Name, previous, home)
		}
		return member, revision, fmt.Errorf("move %s's home from %s to %s: %w", member.Name, previous, home, err)
	}
	written, err := commit(store.CrewHomeMove{Documents: documents, From: previous, To: home})
	if err != nil {
		if restoreErr := os.Rename(home, source); restoreErr != nil {
			return member, revision, errors.Join(err, fmt.Errorf("put %s's home back from %s to %s: %w; its files are at %s", member.Name, home, previous, restoreErr, home))
		}
		return member, revision, err
	}
	for i, document := range documents {
		d.announceCommittedWrite(document.Fact, written[i].Seq)
	}
	d.publishFact(FactCrewUpdated, member.ID, nil)
	d.logf("crew: moved %s's home from %s to %s", member.ID, previous, home)
	return moved, written[0].Rev, nil
}

func (d *Daemon) dispatchesFollowingHome(previous, home string) ([]store.DocumentCommit, error) {
	sessions, err := d.store.SessionsInDirectory(previous)
	if err != nil || len(sessions) == 0 {
		return nil, err
	}
	schema, err := d.dispatchesCollection()
	if docstore.IsUndeclaredCollection(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var commits []store.DocumentCommit
	for _, id := range sessions {
		execution, doc, found, err := d.gardenDispatchDocument(id)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		cwd := rebaseCrewPath(previous, home, execution.Cwd)
		if cwd == execution.Cwd {
			continue
		}
		execution.Cwd = cwd
		body, err := execution.Encode()
		if err != nil {
			return nil, err
		}
		commits = append(commits, store.DocumentCommit{
			Write: store.DocumentWrite{Schema: *schema, ID: doc.ID, Body: body, Expected: &doc.Rev},
			Fact:  documentChangedFact(garden.Namespace, garden.CollectionDispatches, doc.ID, false),
		})
	}
	return commits, nil
}

func (d *Daemon) commitCrewHomeMove(move store.CrewHomeMove) ([]store.DocumentWriteResult, error) {
	return d.store.MoveCrewHome(move, time.Now())
}

func rebaseCrewPath(previous, home, path string) string {
	if path == "" || !pathWithin(previous, path) {
		return path
	}
	rel, _ := filepath.Rel(previous, path)
	return filepath.Join(home, rel)
}

func (d *Daemon) crewMoveHome(msg *protocol.CrewMoveHomeMessage) (*protocol.CrewMoveHomeResult, error) {
	asked, err := d.crewAskedMember(askerOf(msg.SourceSessionID, msg.ProfileID), msg.Member)
	if err != nil {
		return nil, err
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	member, doc, err := d.crewMember(asked.ID)
	if err != nil {
		return nil, err
	}
	previous := member.HomeDir(d.dataRoot)
	if !member.InDataDir() {
		return nil, fmt.Errorf("%s's home is already outside attn's data folder, at %s", member.Name, previous)
	}
	parent, err := d.notebookCrewHomes(member.ProfileID)
	if err != nil {
		return nil, err
	}
	moved, revision, err := d.relocateCrewHome(member, doc.Rev, parent, member.Name, d.commitCrewHomeMove)
	if err != nil {
		return nil, err
	}
	return &protocol.CrewMoveHomeResult{Member: d.crewMemberWire(moved, revision), PreviousHome: previous}, nil
}

func (d *Daemon) handleCrewMoveHome(conn net.Conn, msg *protocol.CrewMoveHomeMessage) {
	result, err := d.crewMoveHome(msg)
	if err != nil {
		d.sendCrewError(conn, "move-home", err)
		return
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewMoveHomeResult: result})
}

func (d *Daemon) crewMembersWithHomes(profileID string) (int, error) {
	members, _, err := d.readCrewMembers()
	if docstore.IsUndeclaredCollection(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	count := 0
	for _, member := range members {
		if member.ProfileID != profileID || member.Retired || member.Chief {
			continue
		}
		if _, err := os.Stat(member.HomeDir(d.dataRoot)); err == nil {
			count++
		}
	}
	return count, nil
}

func (d *Daemon) placeChiefNotebook(member crew.Member, revision int64, root string) (crew.Member, int64, error) {
	if !member.Chief {
		return member, revision, fmt.Errorf("only the Chief's setup picks a Notebook folder; set this profile's folder with attn settings set %s <folder>", SettingNotebookProfileRoot)
	}
	if member.Agent != "" {
		return member, revision, fmt.Errorf("%s's Notebook folder is picked once, at setup; change the profile's folder with attn settings set %s <folder>", member.Name, SettingNotebookProfileRoot)
	}
	previous, changed, err := d.pointProfileNotebook(member.ProfileID, root)
	if err != nil {
		return member, revision, err
	}
	placed, revision, err := d.moveHomeIntoNotebook(member, revision)
	if err != nil && changed {
		if restoreErr := d.setProfileNotebookRoot(member.ProfileID, previous); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("put the profile's Notebook folder back to %q: %w", previous, restoreErr))
		}
	}
	return placed, revision, err
}

func (d *Daemon) pointProfileNotebook(profileID, root string) (previous string, changed bool, err error) {
	current, err := d.notebookRoot(profileID)
	if err != nil {
		return "", false, err
	}
	wanted, err := expandProfileNotebookRoot(strings.TrimSpace(root))
	if err != nil {
		return "", false, err
	}
	if same, err := sameDir(current, wanted); err != nil || same {
		return "", false, err
	}
	previous = d.profileNotebookRoot(profileID)
	if err := d.setProfileNotebookRoot(profileID, root); err != nil {
		return "", false, err
	}
	return previous, true, nil
}

func (d *Daemon) setProfileNotebookRoot(profileID, root string) error {
	_, err := d.setSetting(&protocol.SetSettingMessage{Cmd: protocol.CmdSetSetting, Key: string(SettingNotebookProfileRoot), Value: root, ProfileID: &profileID})
	return err
}

func (d *Daemon) moveHomeIntoNotebook(member crew.Member, revision int64) (crew.Member, int64, error) {
	parent, err := d.notebookCrewHomes(member.ProfileID)
	if err != nil {
		return member, revision, err
	}
	if within, err := sameDirOrWithin(parent, member.HomeDir(d.dataRoot)); err != nil || within {
		return member, revision, err
	}
	return d.relocateCrewHome(member, revision, parent, member.Name, d.commitCrewHomeMove)
}

func sameDir(a, b string) (bool, error) {
	left, right, err := canonicalPair(a, b)
	return left == right, err
}

func sameDirOrWithin(root, path string) (bool, error) {
	left, right, err := canonicalPair(root, path)
	return err == nil && pathWithin(left, right), err
}

func canonicalPair(a, b string) (string, string, error) {
	left, err := config.CanonicalRuntimePath(a)
	if err != nil {
		return "", "", err
	}
	right, err := config.CanonicalRuntimePath(b)
	return left, right, err
}
