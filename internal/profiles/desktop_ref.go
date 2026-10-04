package profiles

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// DesktopLabel is the name a desktop shows; an unnamed one reads "Desktop N"
// like the app's desktopLabel (app/src/utils/desktops.ts).
func DesktopLabel(desktop Desktop, desktops []Desktop) string {
	if name := strings.TrimSpace(desktop.Name); name != "" {
		return name
	}
	return fmt.Sprintf("Desktop %d", desktopNumber(desktop, desktops))
}

func desktopNumber(desktop Desktop, desktops []Desktop) int {
	if desktop.ShortcutSlot != 0 {
		return desktop.ShortcutSlot
	}
	extra := 0
	for _, entry := range orderedDesktops(desktops) {
		if entry.ShortcutSlot != 0 {
			continue
		}
		extra++
		if entry.ID == desktop.ID {
			break
		}
	}
	return LastShortcutSlot + extra
}

func orderedDesktops(desktops []Desktop) []Desktop {
	ordered := append([]Desktop(nil), desktops...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].OrderKey < ordered[j].OrderKey })
	return ordered
}

// ResolveDesktopRef finds a profile's desktop the way the user addresses it: a
// shortcut digit, a desktop id, or its label (case-insensitive, exact).
func ResolveDesktopRef(profile Profile, desktops []Desktop, ref string) (Desktop, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Desktop{}, Errorf(CodeInvalid, "empty desktop; pass a shortcut digit, a desktop name or a desktop id (%s)", DesktopDirectory(profile, desktops))
	}
	if slot, err := strconv.Atoi(ref); err == nil && slot >= FirstShortcutSlot && slot <= LastShortcutSlot {
		for _, desktop := range desktops {
			if desktop.ShortcutSlot == slot {
				return desktop, nil
			}
		}
		return Desktop{}, Errorf(CodeNotFound, "no desktop holds shortcut %d in profile %q; %s", slot, profile.Name, DesktopDirectory(profile, desktops))
	}
	for _, desktop := range desktops {
		if desktop.ID == ref {
			return desktop, nil
		}
	}
	var matches []Desktop
	for _, desktop := range desktops {
		if strings.EqualFold(DesktopLabel(desktop, desktops), ref) {
			matches = append(matches, desktop)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Desktop{}, Errorf(CodeNotFound, "unknown desktop %q in profile %q; %s", ref, profile.Name, DesktopDirectory(profile, desktops))
	}
	named := make([]string, len(matches))
	for i, desktop := range matches {
		named[i] = desktopEntry(desktop, desktops)
	}
	return Desktop{}, Errorf(CodeInvalid, "desktop %q is ambiguous in profile %q: %s; pass its digit or id", ref, profile.Name, strings.Join(named, ", "))
}

// DesktopDirectory lists a profile's desktops as the refs that address them.
func DesktopDirectory(profile Profile, desktops []Desktop) string {
	ordered := orderedDesktops(desktops)
	entries := make([]string, len(ordered))
	for i, desktop := range ordered {
		entries[i] = desktopEntry(desktop, desktops)
	}
	return fmt.Sprintf("desktops in profile %q: %s", profile.Name, strings.Join(entries, ", "))
}

func desktopEntry(desktop Desktop, desktops []Desktop) string {
	slot := "-"
	if desktop.ShortcutSlot != 0 {
		slot = strconv.Itoa(desktop.ShortcutSlot)
	}
	return fmt.Sprintf("%s %s (%s)", slot, DesktopLabel(desktop, desktops), desktop.ID)
}
