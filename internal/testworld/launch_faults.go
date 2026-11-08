package testworld

func (w *World) ExitAtNextBoot(code int, screen string) {
	w.kit.ExitAtNextBoot(code, screen)
}

func (w *World) RequireEnterAtNextBoot(screen string) {
	w.kit.RequireEnterAtNextBoot(screen)
}

func (w *World) RefusePlugLaunches(reason string) (allow func()) {
	w.T.Helper()
	return w.kit.RefusePlugLaunches(reason)
}

func (w *World) AskFolderTrustAtNextBoot() {
	w.kit.AskFolderTrustAtNextBoot()
}

func (w *World) SetClaudeFolderTrust(dir string) {
	w.kit.SetClaudeFolderTrust(dir)
}

func (w *World) ClaudeFolderTrusted(dir string) bool {
	return w.kit.ClaudeFolderTrusted(dir)
}
