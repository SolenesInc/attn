package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
)

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func (d *Daemon) resolvedCrewRoot() (string, error) {
	root, err := config.CanonicalRuntimePath(filepath.Join(d.dataRoot, crew.HomesDirName))
	if err != nil {
		return "", fmt.Errorf("resolve this daemon's crew root: %w", err)
	}
	return root, nil
}

func (d *Daemon) validateCrewMemberPaths(member crew.Member) error {
	root, err := d.resolvedCrewRoot()
	if err != nil {
		return err
	}
	for _, candidate := range []struct{ label, stored string }{
		{"home", member.HomeDir},
		{"charter", member.CharterPath},
	} {
		label, stored := candidate.label, candidate.stored
		resolved, err := config.CanonicalRuntimePath(stored)
		if err != nil {
			return fmt.Errorf("refusing crew member %s: resolve stored %s path %q: %w", d.storedMemberName(member.Key.String()), label, stored, err)
		}
		if stored == "" || !filepath.IsAbs(stored) || !pathWithin(root, resolved) {
			return fmt.Errorf("crew member %s: stored %s path %q is outside this daemon's crew root %q. Check whether attn.db was copied from another instance", d.storedMemberName(member.Key.String()), label, stored, root)
		}
	}
	return nil
}

func (d *Daemon) validateCrewLetterPath(member crew.Member, stored string) error {
	if err := d.validateCrewMemberPaths(member); err != nil {
		return err
	}
	handoffsDir, err := d.validateCrewHandoffsDir(member)
	if err != nil {
		return err
	}
	expectedRoot, err := config.CanonicalRuntimePath(handoffsDir)
	if err != nil {
		return fmt.Errorf("resolve %s's handoffs root %q: %w", d.storedMemberName(member.Key.String()), handoffsDir, err)
	}
	letter, err := config.CanonicalRuntimePath(stored)
	if err != nil {
		return fmt.Errorf("resolve %s's stored handoff path %q: %w", d.storedMemberName(member.Key.String()), stored, err)
	}
	if stored == "" || !filepath.IsAbs(stored) || !pathWithin(expectedRoot, letter) {
		return fmt.Errorf("crew member %s: stored handoff path %q is outside their handoffs root %q. Check for a copied attn.db or a symlink outside their home", d.storedMemberName(member.Key.String()), stored, expectedRoot)
	}
	return nil
}

func (d *Daemon) validateCrewHandoffsDir(member crew.Member) (string, error) {
	if err := d.validateCrewMemberPaths(member); err != nil {
		return "", err
	}
	home, err := config.CanonicalRuntimePath(member.HomeDir)
	if err != nil {
		return "", fmt.Errorf("resolve %s's home %q: %w", d.storedMemberName(member.Key.String()), member.HomeDir, err)
	}
	stored := filepath.Join(member.HomeDir, crew.HandoffsDirName)
	resolved, err := config.CanonicalRuntimePath(stored)
	if err != nil {
		return "", fmt.Errorf("resolve %s's handoffs directory %q: %w", d.storedMemberName(member.Key.String()), stored, err)
	}
	if !pathWithin(home, resolved) {
		return "", fmt.Errorf("crew member %s: handoffs directory %q resolves outside their home %q. Check for a copied attn.db or a symlink outside their home", d.storedMemberName(member.Key.String()), stored, home)
	}
	return stored, nil
}

func instanceCrewRootContaining(userHome, target string) string {
	if strings.TrimSpace(userHome) == "" || strings.TrimSpace(target) == "" {
		return ""
	}
	home, err := filepath.Abs(userHome)
	if err != nil {
		return ""
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return ""
	}
	home = filepath.Clean(home)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(home, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		home, err = config.CanonicalRuntimePath(home)
		if err != nil {
			return ""
		}
		target, err = config.CanonicalRuntimePath(target)
		if err != nil {
			return ""
		}
		rel, err = filepath.Rel(home, target)
	}
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return canonicalInstanceCrewRootContaining(userHome, target)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 || parts[1] != crew.HomesDirName {
		return canonicalInstanceCrewRootContaining(userHome, target)
	}
	if parts[0] != ".attn" && !strings.HasPrefix(parts[0], ".attn-") {
		return canonicalInstanceCrewRootContaining(userHome, target)
	}
	return filepath.Join(home, parts[0], crew.HomesDirName)
}

func canonicalInstanceCrewRootContaining(userHome, target string) string {
	target, err := config.CanonicalRuntimePath(target)
	if err != nil || target == "" {
		return ""
	}
	entries, err := os.ReadDir(userHome)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != ".attn" && !strings.HasPrefix(name, ".attn-") {
			continue
		}
		storedRoot := filepath.Join(userHome, name, crew.HomesDirName)
		root, err := config.CanonicalRuntimePath(storedRoot)
		if err == nil && pathWithin(root, target) {
			return storedRoot
		}
	}
	return ""
}

func (d *Daemon) resolveCrewWorkDir(dir string) (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve the user home while checking crew directory %q: %w", dir, err)
	}
	return d.resolveCrewWorkDirForHome(dir, userHome)
}

func (d *Daemon) resolveCrewWorkDirForHome(dir, userHome string) (string, error) {
	original, err := absoluteCrewDir(dir)
	if err != nil || original == "" {
		return original, err
	}
	resolved, err := config.CanonicalRuntimePath(original)
	if err != nil {
		return "", fmt.Errorf("resolve crew directory %q: %w", dir, err)
	}
	foreignRoot := instanceCrewRootContaining(userHome, original)
	if foreignRoot == "" {
		foreignRoot = instanceCrewRootContaining(userHome, resolved)
	}
	if foreignRoot == "" {
		return original, nil
	}
	storedForeignRoot := foreignRoot
	foreignRoot, err = config.CanonicalRuntimePath(storedForeignRoot)
	if err != nil {
		return "", fmt.Errorf("resolve foreign crew root %q: %w", storedForeignRoot, err)
	}
	ownRoot, err := d.resolvedCrewRoot()
	if err != nil {
		return "", err
	}
	if foreignRoot != ownRoot {
		if storedForeignRoot != foreignRoot {
			return "", fmt.Errorf("refusing crew directory %q: it is inside another instance's crew root %q, which resolves to %q; this daemon's crew root is %q", dir, storedForeignRoot, foreignRoot, ownRoot)
		}
		return "", fmt.Errorf("refusing crew directory %q: it resolves inside another instance's crew root %q; this daemon's crew root is %q", dir, foreignRoot, ownRoot)
	}
	return original, nil
}

func (d *Daemon) resolveCrewRecordedDir(dir string) (string, error) {
	resolved, err := resolveCrewDir(dir)
	if err != nil || resolved == "" {
		return resolved, err
	}
	return d.resolveCrewWorkDir(resolved)
}

func (d *Daemon) validateCrewWorkDirs(member crew.Member) error {
	if _, err := d.resolveCrewWorkDir(member.CWD); err != nil {
		return fmt.Errorf("%s's cwd: %w", d.storedMemberName(member.Key.String()), err)
	}
	return d.validateCrewAwarenessDirs(member)
}

func (d *Daemon) validateCrewAwarenessDirs(member crew.Member) error {
	for _, dir := range member.AwarenessDirs {
		if _, err := d.resolveCrewWorkDir(dir); err != nil {
			return fmt.Errorf("%s's awareness directory: %w", d.storedMemberName(member.Key.String()), err)
		}
	}
	return nil
}

func (d *Daemon) validateCrewBoundLaunchDir(sessionID protocol.SessionID, dir string) (string, error) {
	members, _, err := d.readCrewMembers()
	if docstore.IsUndeclaredCollection(err) {
		return dir, nil
	}
	if err != nil {
		return "", err
	}
	for _, member := range members {
		if member.BindingSession != sessionID {
			continue
		}
		resolved, err := d.resolveCrewWorkDir(dir)
		if err != nil {
			return "", fmt.Errorf("%s's plugin launch directory: %w", d.storedMemberName(member.Key.String()), err)
		}
		return resolved, nil
	}
	return dir, nil
}
