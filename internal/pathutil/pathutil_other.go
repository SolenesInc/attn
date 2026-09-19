//go:build !darwin

package pathutil

func EnsureGUIPath() error {
	return nil
}
