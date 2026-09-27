package testworld

func (w *World) AwaitHeldBoot() {
	w.T.Helper()
	w.kit.AwaitHeldBoot()
}
