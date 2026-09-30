//go:build !unix

package git

import "os"

func gitPathOwnedByCurrentUser(info os.FileInfo) bool {
	return false
}
