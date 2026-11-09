package main

func TopLevelCommandNames() []string {
	var names []string
	for _, command := range topLevelCommands() {
		names = append(names, command.names[0])
	}
	return names
}
