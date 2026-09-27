package testworld

func (w *World) ExitAtNextBoot(code int, screen string) {
	w.kit.ExitAtNextBoot(code, screen)
}

func (w *World) RefusePiLaunches(reason string) (allow func()) {
	w.T.Helper()
	return w.kit.RefusePiLaunches(reason)
}
