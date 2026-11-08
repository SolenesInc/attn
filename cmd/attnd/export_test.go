package main

func ListedCommands() []string {
	var names []string
	for _, command := range topLevelCommands() {
		if command.listing != unlisted {
			names = append(names, command.names[0])
		}
	}
	return names
}
