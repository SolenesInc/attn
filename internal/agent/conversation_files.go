package agent

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type ConversationFileSystem struct{ home *os.File }

func OpenConversationFiles(home string) (*ConversationFileSystem, error) {
	f, err := os.Open(home)
	if err != nil {
		return nil, err
	}
	return &ConversationFileSystem{f}, nil
}

// Pin each parent directory and refuse symlinks before opening the next component.
func (c *ConversationFileSystem) parent(name string, create bool) (*os.File, string, error) {
	if !fs.ValidPath(name) || name == "." {
		return nil, "", fmt.Errorf("invalid conversation path: %s", name)
	}
	parts := strings.Split(name, "/")
	fd, err := unix.FcntlInt(c.home.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr == unix.ENOENT && create {
			if err := unix.Mkdirat(fd, part, 0700); err != nil && err != unix.EEXIST {
				unix.Close(fd)
				return nil, "", err
			}
			next, openErr = unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		unix.Close(fd)
		if openErr != nil {
			return nil, "", &os.PathError{Op: "open conversation directory", Path: name, Err: openErr}
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Dir(name)), parts[len(parts)-1], nil
}

func (c *ConversationFileSystem) Open(name string) (fs.File, error) {
	parent, leaf, err := c.parent(name, false)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), leaf, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open conversation file", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (c *ConversationFileSystem) Restore(src io.Reader, name string, directory bool, now time.Time) error {
	parent, leaf, err := c.parent(name, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	fd := int(parent.Fd())
	if directory {
		if err := unix.Mkdirat(fd, leaf, 0700); err != nil && err != unix.EEXIST {
			return err
		}
		check, err := unix.Openat(fd, leaf, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == nil {
			unix.Close(check)
		}
		return err
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, leaf, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if stat.Mode&unix.S_IFMT != unix.S_IFREG {
			return fmt.Errorf("conversation destination is not a regular file: %s", name)
		}
		return nil
	} else if err != unix.ENOENT {
		return err
	}
	temporary := ".restore-" + uuid.NewString()
	output, err := unix.Openat(fd, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(output), temporary)
	defer f.Close()
	defer unix.Unlinkat(fd, temporary, 0)
	if _, err := io.Copy(f, src); err != nil {
		return err
	}
	stamp := unix.NsecToTimeval(now.UnixNano())
	if err := unix.Futimes(output, []unix.Timeval{stamp, stamp}); err != nil {
		return err
	}
	// Install without replacing a harness file, even if one appeared during the copy.
	if err := unix.Linkat(fd, temporary, fd, leaf, 0); err != nil {
		if err != unix.EEXIST {
			return err
		}
		if err := unix.Fstatat(fd, leaf, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG {
			return fmt.Errorf("conversation destination is not a regular file: %s", name)
		}
	}
	return nil
}

func (c *ConversationFileSystem) Close() error { return c.home.Close() }
