package daemon

import (
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

func (d *Daemon) newCrewHome(profile, name string) (string, error) {
	root, err := d.notebookRoot(profile)
	if err != nil {
		return "", err
	}
	parent := filepath.Join(root, crew.HomesDirName)
	return reserveCrewHome(parent, name)
}

func reserveCrewHome(parent, name string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	base := filepath.Join(parent, strings.ToLower(name))
	candidate := base
	for n := 2; ; n++ {
		if err := os.Mkdir(candidate, 0o755); err == nil {
			return candidate, nil
		} else if !os.IsExist(err) {
			return "", err
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
}

func (d *Daemon) relocateCrewHome(id store.CrewIdentity, member crew.Member, revision int64, parent string) (crew.Member, int64, string, error) {
	if d.crewBindingLive(member) {
		return member, revision, "", fmt.Errorf("%s is awake; put it to sleep first: attn crew sleep %s", d.memberName(id.Key), d.memberName(id.Key))
	}
	source, err := filepath.EvalSymlinks(member.HomeDir)
	if err != nil {
		return member, revision, "", err
	}
	resolvedParent, err := config.CanonicalRuntimePath(parent)
	if err != nil {
		return member, revision, "", err
	}
	if pathWithin(source, resolvedParent) {
		return member, revision, "", fmt.Errorf("destination %s is inside %s's home %s", parent, id.Name, source)
	}
	home, err := reserveCrewHome(parent, id.Name)
	if err != nil {
		return member, revision, "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(home)
		}
	}()
	if err := copyCrewHome(source, home); err != nil {
		return member, revision, "", err
	}
	rebase := func(path string) string {
		if path == "" || !pathWithin(member.HomeDir, path) {
			return path
		}
		rel, _ := filepath.Rel(member.HomeDir, path)
		return filepath.Join(home, rel)
	}
	moved := member
	moved.CharterPath = rebase(member.CharterPath)
	moved.LetterPath = rebase(member.LetterPath)
	moved.CWD = rebase(member.CWD)
	moved.AwarenessDirs = append([]string{}, member.AwarenessDirs...)
	for i, dir := range moved.AwarenessDirs {
		moved.AwarenessDirs[i] = rebase(dir)
	}
	moved.HomeDir = home
	moved.NotebookHome = true
	schema, err := d.crewCollection()
	if err != nil {
		return member, revision, "", err
	}
	body, err := moved.Encode()
	if err != nil {
		return member, revision, "", err
	}
	fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, id.Key.String(), false)
	d.lockGardenRoles()
	written, err := d.store.CommitCrewHome(store.NewCrewMember{Identity: id, Doc: store.DocumentWrite{Schema: *schema, ID: id.Key.String(), Body: body, Expected: &revision}, Fact: fact}, time.Now())
	d.unlockGardenRoles()
	if err != nil {
		return member, revision, "", err
	}
	committed = true
	d.announceCommittedWrite(fact, written.Seq)
	d.publishFact(FactCrewUpdated, id.Key.String(), nil)
	cleanup := ""
	if err := os.RemoveAll(member.HomeDir); err != nil {
		cleanup = fmt.Sprintf("home moved to %s; old folder %s could not be removed: %v", home, member.HomeDir, err)
	}
	return moved, written.Rev, cleanup, nil
}

func copyCrewHome(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			if rel != "." {
				if err := os.Mkdir(target, info.Mode().Perm()); err != nil {
					return err
				}
			}
			return os.Chmod(target, info.Mode().Perm())
		case entry.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, in)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			return closeErr
		default:
			return fmt.Errorf("cannot move crew home containing %s (%s)", path, info.Mode())
		}
	})
}

func (d *Daemon) handleCrewMoveHome(conn net.Conn, msg *protocol.CrewMoveHomeMessage) {
	b, err := d.bindings()
	if err != nil {
		d.sendCrewError(conn, "move-home", err)
		return
	}
	r, err := d.requestFromMessage(msg.SourceSessionID, msg.ProfileID, b)
	if err != nil {
		d.sendCrewError(conn, "move-home", err)
		return
	}
	id, err := d.resolveMember(r, msg.Member)
	if err != nil {
		d.sendCrewError(conn, "move-home", err)
		return
	}
	d.crewWakeMu.Lock()
	defer d.crewWakeMu.Unlock()
	member, doc, err := d.crewMember(id.Key)
	if err != nil {
		d.sendCrewError(conn, "move-home", err)
		return
	}
	previous := member.HomeDir
	if member.NotebookHome || previous == "" {
		d.sendCrewError(conn, "move-home", fmt.Errorf("%s has no legacy home to move (%s)", id.Name, previous))
		return
	}
	root, err := d.notebookRoot(id.ProfileID)
	if err != nil {
		d.sendCrewError(conn, "move-home", err)
		return
	}
	moved, rev, cleanup, err := d.relocateCrewHome(id, member, doc.Rev, filepath.Join(root, crew.HomesDirName))
	if err != nil {
		d.sendCrewError(conn, "move-home", err)
		return
	}
	result := &protocol.CrewMoveHomeResult{Member: d.crewMemberWire(moved, rev), PreviousHome: previous}
	if cleanup != "" {
		result.CleanupError = &cleanup
	}
	d.sendGardenResponse(conn, protocol.Response{Ok: true, CrewMoveHomeResult: result})
}

func (d *Daemon) furnishCrewHome(id store.CrewIdentity, member *crew.Member, charter string) error {
	home, err := d.newCrewHome(id.ProfileID, id.Name)
	if err != nil {
		return err
	}
	member.HomeDir = home
	member.NotebookHome = true
	member.CharterPath = filepath.Join(home, crew.CharterFileName)
	if err := writeCrewCharter(member.CharterPath, charter); err != nil {
		_ = os.RemoveAll(home)
		return err
	}
	if d.isChief(id.Key) {
		member.CWD, err = d.notebookRoot(id.ProfileID)
	}
	return err
}
