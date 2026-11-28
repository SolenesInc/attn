package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/victorarias/attn/internal/command"
	"github.com/victorarias/attn/internal/layouttree"
	"github.com/victorarias/attn/internal/profiles"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/setting"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/viewlang"
	"github.com/victorarias/attn/internal/views"
)

func (d *Daemon) keptViewCommands() map[string]command.Handler {
	return map[string]command.Handler{
		"view list":    command.Typed(d.runViewList),
		"view read":    command.Typed(d.runViewRead),
		"view export":  command.Typed(d.runViewExport),
		"view keep":    command.Typed(d.runViewKeep),
		"view edit":    command.Typed(d.runViewEdit),
		"view restore": command.Typed(d.runViewRestore),
		"view fork":    command.Typed(d.runViewFork),
		"view delete":  command.Typed(d.runViewDelete),
		"view show":    command.Typed(d.runViewShow),
		"view hide":    command.Typed(d.runViewHide),
	}
}

// viewsProfile is the profile whose views the caller works with, while
// views_enabled is on.
func (d *Daemon) viewsProfile(c command.Caller) (profiles.Profile, error) {
	if !d.on(setting.ViewsEnabled) {
		return profiles.Profile{}, errors.New("views are off: turn on the experimental setting views_enabled")
	}
	return d.resolveGardenProfile(c.Session, c.Profile, "")
}

// refusedView turns a refusal of the views rules into the command's refusal,
// each problem on its own line.
func refusedView(err error) error {
	var refused *views.Refusal
	if !errors.As(err, &refused) {
		return err
	}
	message := refused.Message
	for _, problem := range refused.Problems {
		message += fmt.Sprintf("\nline %d: %s %s", problem.Line, problem.Text, problem.Fix)
	}
	return command.Refuse(refused.Code, "%s", message)
}

func notKept(name string) error {
	return command.Refuse(command.CodeNotFound, "this profile keeps no view %s", name)
}

func notYetBuiltIn(name string) error {
	return command.Refuse(command.CodeBuiltIn, "%s is one of attn's own views, which are not view files yet, so it is not available to read, export, edit, restore or fork yet", name)
}

// keptView is the kept view name of the profile, refusing one of attn's own
// and a name the profile does not keep.
func (d *Daemon) keptView(profileID, name string) (store.KeptView, error) {
	if views.BuiltIn(name) {
		return store.KeptView{}, notYetBuiltIn(name)
	}
	view, kept, err := d.store.KeptView(profileID, name)
	if err == nil && !kept {
		err = notKept(name)
	}
	return view, err
}

// callerViewFolder reads the folder at the caller's path on the caller's
// machine, and the id of that machine.
func (d *Daemon) callerViewFolder(ctx context.Context, c command.Caller, arg, value string) (views.Files, []string, string, error) {
	path, found, err := d.callerPath(ctx, c, callerFile(arg, value))
	if err != nil {
		return nil, nil, "", err
	}
	host := callerHost(ctx)
	read := protocol.ViewFolderReadMessage{Cmd: protocol.CmdViewFolderRead, Path: path}
	if host != "" {
		read.Host = protocol.Ptr(host)
	}
	folder, err := Call[protocol.ViewFolder](ctx, d.router(), read)
	if err != nil {
		_, err = found.explain(nil, d.tileDocumentError(host, err))
		return nil, nil, "", err
	}
	if folder.Bytes > views.MaxBytes {
		return nil, nil, "", command.Refuse(command.CodeTooLarge, "the view's files hold %d bytes, more than the %d a view keeps", folder.Bytes, views.MaxBytes)
	}
	files, err := decodedViewFiles(stringsOf(folder.Files))
	return files, folder.Missing, d.machineFor(host).ID(), err
}

func callerHost(ctx context.Context) string {
	if origin, remote := OriginOf(ctx); remote {
		return origin.Host
	}
	return ""
}

// viewMachine is the machine a version of a view made of files runs on: none,
// so the home holds it, when it runs no command; else the machine its
// commands already run on, or the machine of whoever saves the first version
// with commands.
func viewMachine(files views.Files, runsOn, saver string) string {
	if len(viewCommands(viewlang.Check(string(files[views.ViewFile])).Parts)) == 0 {
		return ""
	}
	if runsOn != "" {
		return runsOn
	}
	return saver
}

func savedVersion(c command.Caller, note string) store.ViewVersion {
	return store.ViewVersion{Note: strings.TrimSpace(note), SavedAt: time.Now(), SavedBy: string(c.Session)}
}

// replaced places the view's newest version, and takes the folder off the
// machine that held view before, when the new version runs elsewhere.
func (d *Daemon) replaced(ctx context.Context, view store.KeptView) {
	d.placed(ctx, view.ProfileID, view.Name)
	if now, kept, err := d.store.KeptView(view.ProfileID, view.Name); err == nil && kept && d.machineFor(now.MachineID).ID() != d.machineFor(view.MachineID).ID() {
		d.removeKeptViewFolder(ctx, view)
	}
}

func (d *Daemon) removeKeptViewFolder(ctx context.Context, view store.KeptView) {
	remove := protocol.ViewFolderWriteMessage{Cmd: protocol.CmdViewFolderWrite, ProfileID: view.ProfileID, View: view.Name, Files: protocol.RecordString{}}
	if host := d.machineFor(view.MachineID).Outpost(); host != "" {
		remove.Host = protocol.Ptr(host)
	}
	if _, err := Call[protocol.ViewFolderWritten](ctx, d.router(), remove); err != nil {
		d.logf("remove the folder of view %s from its machine: %v", view.Name, err)
	}
}

func (d *Daemon) placed(ctx context.Context, profileID, name string) {
	if err := d.placeKeptView(ctx, profileID, name); err != nil {
		d.logf("kept view %s: %v", name, err)
	}
}

func (d *Daemon) runViewKeep(ctx context.Context, c command.Caller, a command.ViewKeepArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	if err := views.CheckName(a.Name); err != nil {
		return nil, refusedView(err)
	}
	folder, _, machineID, err := d.callerViewFolder(ctx, c, "path", a.Path)
	if err != nil {
		return nil, err
	}
	files, err := views.Kept(folder)
	if err != nil {
		return nil, refusedView(err)
	}
	if err := d.store.KeepView(profile.ID, a.Name, viewMachine(files, "", machineID), savedVersion(c, a.Note), files); errors.Is(err, store.ErrViewExists) {
		return nil, command.Refuse(command.CodeExists, "this profile already keeps a view %s", a.Name)
	} else if err != nil {
		return nil, err
	}
	d.placed(ctx, profile.ID, a.Name)
	return protocol.ViewKeepResult{View: a.Name, Version: 1}, nil
}

func (d *Daemon) runViewEdit(ctx context.Context, c command.Caller, a command.ViewEditArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	view, err := d.keptView(profile.ID, a.View)
	if err != nil {
		return nil, err
	}
	folder, missing, saver, err := d.callerViewFolder(ctx, c, "from", a.From)
	if err != nil {
		return nil, err
	}
	kept, err := d.store.ViewFiles(profile.ID, view.Name, view.Version)
	if err != nil {
		return nil, err
	}
	files, err := views.Edited(view.Name, view.Version, kept, folder, missing)
	if err != nil {
		return nil, refusedView(err)
	}
	version, err := d.store.SaveViewVersion(profile.ID, view.Name, view.Version, viewMachine(files, view.MachineID, saver), savedVersion(c, a.Note), files)
	if errors.Is(err, store.ErrStaleView) {
		return nil, command.Refuse(command.CodeStaleExport, "%s changed while saving; export it again and reapply your change", view.Name)
	} else if err != nil {
		return nil, err
	}
	d.replaced(ctx, view)
	return protocol.ViewEditResult{View: view.Name, Version: version, PreviousVersion: view.Version}, nil
}

func (d *Daemon) runViewRestore(ctx context.Context, c command.Caller, a command.ViewRestoreArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	view, err := d.keptView(profile.ID, a.View)
	if err != nil {
		return nil, err
	}
	restore := view.Version - 1
	if a.Version != nil {
		restore = int(*a.Version)
	}
	versions, err := d.store.ViewVersions(profile.ID, view.Name)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(versions, func(v store.ViewVersion) bool { return v.Version == restore }) || restore == view.Version {
		return nil, command.Refuse(command.CodeNotFound, "%s has no earlier version %d; its versions are 1 to %d", view.Name, restore, view.Version)
	}
	files, err := d.store.ViewFiles(profile.ID, view.Name, restore)
	if err != nil {
		return nil, err
	}
	saved := savedVersion(c, "")
	saved.RestoredFrom = restore
	version, err := d.store.SaveViewVersion(profile.ID, view.Name, view.Version, viewMachine(files, view.MachineID, d.machineFor(callerHost(ctx)).ID()), saved, files)
	if errors.Is(err, store.ErrStaleView) {
		return nil, command.Refuse(command.CodeStaleExport, "%s changed while restoring", view.Name)
	} else if err != nil {
		return nil, err
	}
	d.replaced(ctx, view)
	return protocol.ViewRestoreResult{View: view.Name, Version: version, RestoredFrom: restore}, nil
}

func (d *Daemon) runViewFork(ctx context.Context, c command.Caller, a command.ViewForkArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	view, err := d.keptView(profile.ID, a.View)
	if err != nil {
		return nil, err
	}
	if err := views.CheckName(a.Name); err != nil {
		return nil, refusedView(err)
	}
	files, err := d.store.ViewFiles(profile.ID, view.Name, view.Version)
	if err != nil {
		return nil, err
	}
	saved := savedVersion(c, fmt.Sprintf("a copy of version %d of %s", view.Version, view.Name))
	if err := d.store.KeepView(profile.ID, a.Name, view.MachineID, saved, files); errors.Is(err, store.ErrViewExists) {
		return nil, command.Refuse(command.CodeExists, "this profile already keeps a view %s", a.Name)
	} else if err != nil {
		return nil, err
	}
	d.placed(ctx, profile.ID, a.Name)
	return protocol.ViewForkResult{View: a.Name, From: view.Name, Version: 1}, nil
}

func (d *Daemon) runViewDelete(ctx context.Context, c command.Caller, a command.ViewDeleteArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	if views.BuiltIn(a.View) {
		return nil, command.Refuse(command.CodeBuiltIn, "%s is one of attn's own views and cannot be deleted", a.View)
	}
	view, err := d.keptView(profile.ID, a.View)
	if err != nil {
		return nil, err
	}
	closed := []protocol.ViewShownOn{}
	shown, err := d.viewShownOn(profile.ID, view.Name)
	if err != nil {
		return nil, err
	}
	for _, on := range shown {
		removed, err := d.removeViewTiles(on.DesktopID, view.Name)
		if err != nil {
			return nil, err
		}
		if len(removed) > 0 {
			closed = append(closed, on)
		}
	}
	if err := d.discardKeptViewFiles(view); err != nil {
		return nil, err
	}
	if err := d.store.DeleteView(view, d.machineFor(view.MachineID).ID()); err != nil {
		return nil, err
	}
	d.removeKeptViewFolder(ctx, view)
	return protocol.ViewDeleteResult{View: view.Name, Closed: closed}, nil
}

// viewShownOn is each tile of the profile's desktops that names the view.
func (d *Daemon) viewShownOn(profileID, name string) ([]protocol.ViewShownOn, error) {
	_, desktops, err := d.store.ProfileArrangement(profileID)
	if err != nil {
		return nil, err
	}
	shown := []protocol.ViewShownOn{}
	for _, desktop := range desktops {
		for _, leaf := range layouttree.TileLeaves(desktop.Tree) {
			if named, ok := profiles.ViewNamed(leaf.TileParams); ok && named == name && leaf.TileKind == string(layouttree.TileKindMarkdown) {
				shown = append(shown, protocol.ViewShownOn{DesktopID: desktop.ID, Desktop: profiles.DesktopLabel(desktop, desktops), TileID: leaf.TileID})
			}
		}
	}
	return shown, nil
}

func (d *Daemon) removeViewTiles(desktopID, name string) ([]string, error) {
	removed := []string{}
	desktop, err := d.store.EditDesktopArrangement(desktopID, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		for _, leaf := range layouttree.TileLeaves(desktop.Tree) {
			if named, ok := profiles.ViewNamed(leaf.TileParams); ok && named == name {
				desktop.Tree, _ = layouttree.Remove(desktop.Tree, leaf.TileID)
				removed = append(removed, leaf.TileID)
			}
		}
		return desktop, nil
	})
	if err == nil && len(removed) > 0 {
		d.publishArrangementChanged(desktop.ProfileID)
	}
	return removed, err
}

// viewDesktop is the desktop ref names in the profile, the Home desktop when
// it names none.
func (d *Daemon) viewDesktop(profile profiles.Profile, ref string) (profiles.Desktop, error) {
	if strings.TrimSpace(ref) == "" {
		return d.store.GetDesktop(profiles.HomeDesktopID(profile.ID))
	}
	desktop, err := d.resolveDesktopRef(profile, ref)
	if profiles.IsNotFound(err) {
		return desktop, command.Refuse(command.CodeNotFound, "%v", err)
	}
	return desktop, err
}

// viewExists refuses a name that is neither one of attn's views nor kept.
func (d *Daemon) viewExists(profileID, name string) error {
	if views.BuiltIn(name) {
		return nil
	}
	_, kept, err := d.store.KeptView(profileID, name)
	if err == nil && !kept {
		return notKept(name)
	}
	return err
}

func (d *Daemon) runViewShow(_ context.Context, c command.Caller, a command.ViewShowArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	if err := d.viewExists(profile.ID, a.View); err != nil {
		return nil, err
	}
	desktop, err := d.viewDesktop(profile, a.Desktop)
	if err != nil {
		return nil, err
	}
	result := protocol.ViewShowResult{View: a.View, DesktopID: desktop.ID, TileID: "tile-view-" + a.View, Added: true}
	updated, err := d.store.EditDesktopArrangement(desktop.ID, func(desktop profiles.Desktop) (profiles.Desktop, error) {
		for _, leaf := range layouttree.TileLeaves(desktop.Tree) {
			if named, ok := profiles.ViewNamed(leaf.TileParams); ok && named == a.View {
				result.TileID, result.Added = leaf.TileID, false
				return desktop, nil
			}
		}
		if layouttree.HasLeaf(desktop.Tree, result.TileID) {
			result.TileID = "tile-view-" + a.View + "-" + uuid.NewString()[:8]
		}
		tile := layouttree.Node{Type: "tile", TileID: result.TileID, TileKind: string(layouttree.TileKindMarkdown), TileParams: profiles.ViewParams(a.View)}
		desktop.Tree = layouttree.FromColumns(append(layouttree.Columns(desktop.Tree), tile), func() string { return "split-" + uuid.NewString() })
		return desktop, nil
	})
	if err != nil {
		return nil, err
	}
	if result.Added {
		d.publishArrangementChanged(updated.ProfileID)
	}
	return result, nil
}

func (d *Daemon) runViewHide(_ context.Context, c command.Caller, a command.ViewHideArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	if err := d.viewExists(profile.ID, a.View); err != nil {
		return nil, err
	}
	desktop, err := d.viewDesktop(profile, a.Desktop)
	if err != nil {
		return nil, err
	}
	removed, err := d.removeViewTiles(desktop.ID, a.View)
	if err != nil {
		return nil, err
	}
	return protocol.ViewHideResult{View: a.View, DesktopID: desktop.ID, Removed: removed}, nil
}

func (d *Daemon) runViewList(_ context.Context, c command.Caller, a command.ViewListArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	kept, err := d.store.KeptViews(profile.ID)
	if err != nil {
		return nil, err
	}
	var listings []protocol.ViewListing
	for _, name := range profiles.BuiltInViews {
		listings = append(listings, protocol.ViewListing{Name: name, BuiltIn: true})
	}
	for _, view := range kept {
		listings = append(listings, protocol.ViewListing{Name: view.Name, Version: protocol.Ptr(view.Version)})
	}
	if a.View != "" {
		listings = slices.DeleteFunc(listings, func(l protocol.ViewListing) bool { return l.Name != a.View })
		if len(listings) == 0 {
			return nil, notKept(a.View)
		}
	}
	for i := range listings {
		if listings[i].Shown, err = d.viewShownOn(profile.ID, listings[i].Name); err != nil {
			return nil, err
		}
		if a.View == "" || listings[i].BuiltIn {
			continue
		}
		versions, err := d.store.ViewVersions(profile.ID, listings[i].Name)
		if err != nil {
			return nil, err
		}
		listed := []protocol.ViewVersionListing{}
		for _, v := range versions {
			listing := protocol.ViewVersionListing{Version: v.Version, SavedAt: v.SavedAt.UTC().Format(time.RFC3339)}
			if v.Note != "" {
				listing.Note = protocol.Ptr(v.Note)
			}
			if v.RestoredFrom > 0 {
				listing.RestoredFrom = protocol.Ptr(v.RestoredFrom)
			}
			if v.SavedBy != "" {
				listing.SavedBy = protocol.Ptr(v.SavedBy)
			}
			listed = append(listed, listing)
		}
		listings[i].Versions = listed
	}
	return protocol.ViewListResult{Views: listings}, nil
}

func (d *Daemon) runViewExport(_ context.Context, c command.Caller, a command.ViewExportArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	view, err := d.keptView(profile.ID, a.View)
	if err != nil {
		return nil, err
	}
	files, err := d.store.ViewFiles(profile.ID, view.Name, view.Version)
	if err != nil {
		return nil, err
	}
	texts, other := views.Exported(view.Name, view.Version, files)
	result := protocol.ViewExportResult{View: view.Name, Version: view.Version, Files: recordOf(texts)}
	if len(other) > 0 {
		result.Kept = other
	}
	return result, nil
}

func (d *Daemon) runViewRead(ctx context.Context, c command.Caller, a command.ViewReadArgs) (any, error) {
	profile, err := d.viewsProfile(c)
	if err != nil {
		return nil, err
	}
	result := protocol.ViewReadResult{}
	var document tileDocument
	switch {
	case a.View != "":
		view, err := d.keptView(profile.ID, a.View)
		if err != nil {
			return nil, err
		}
		if err := d.placeKeptView(ctx, profile.ID, view.Name); err != nil {
			return nil, err
		}
		if document, err = d.keptViewDocument(profile.ID, view.Name); err != nil {
			return nil, err
		}
		result.View, result.Version = protocol.Ptr(view.Name), protocol.Ptr(view.Version)
	case a.Path != "":
		path, _, err := d.callerPath(ctx, c, callerFile("path", a.Path))
		if err != nil {
			return nil, err
		}
		if origin, remote := OriginOf(ctx); remote {
			document.host = origin.Host
		}
		document.path = path
	default:
		return nil, command.Refuse(command.CodeNoTarget, "pass a view's name or a view file's path")
	}
	read := protocol.TileDocumentReadMessage{Cmd: protocol.CmdTileDocumentRead, Path: document.path}
	if document.host != "" {
		read.Host = protocol.Ptr(document.host)
	}
	answer, err := Call[protocol.TileDocument](ctx, d.router(), read)
	if err != nil {
		return nil, d.tileDocumentError(document.host, err)
	}
	if answer.Error != nil {
		return nil, errors.New(*answer.Error)
	}
	if answer.View == nil {
		return nil, fmt.Errorf("%s is not a view: it opens no part of the view language", document.path)
	}
	result.Path, result.Text = document.path, answer.Content
	result.Language, result.Problems, result.Sources, result.Values, result.Commands = answer.View.Language, answer.View.Problems, answer.View.Sources, answer.View.Values, answer.View.Commands
	return result, nil
}
