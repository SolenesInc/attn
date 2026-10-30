package garden

import "fmt"

func MoveEntryBody(words, detail string) string {
	switch {
	case words == "":
		return detail
	case detail == "":
		return words
	default:
		return words + ": " + detail
	}
}

func LinkEntryBodies(kind, from, to string, linked bool) (onFrom, onTo string) {
	switch {
	case kind == EdgeBlocks && linked:
		return "now blocks " + to, "now waits for " + from
	case kind == EdgeBlocks:
		return "no longer blocks " + to, "no longer waits for " + from
	case kind == EdgePartOf && linked:
		return "joined plot " + to, from + " joined this plot"
	case kind == EdgePartOf:
		return "left plot " + to, from + " left this plot"
	case kind == EdgeDiscoveredFrom && linked:
		return "discovered from " + to, from + " was discovered here"
	case kind == EdgeDiscoveredFrom:
		return "no longer discovered from " + to, from + " no longer discovered here"
	case linked:
		return fmt.Sprintf("now %s %s", kind, to), fmt.Sprintf("%s now %s this seed", from, kind)
	default:
		return fmt.Sprintf("no longer %s %s", kind, to), fmt.Sprintf("%s no longer %s this seed", from, kind)
	}
}
