package protocol

// Available is whether a launch may try the harness: installed, or still being checked.
func (h Harness) Available() bool { return h.Installation != HarnessInstallationMissing }
