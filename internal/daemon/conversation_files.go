package daemon

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

func conversationFileAllowed(agent, resumeID, name string) bool {
	id, err := uuid.Parse(resumeID)
	if agent != "claude" || err != nil || id.String() != resumeID || !fs.ValidPath(name) {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) >= 3 && parts[0] == ".claude" && parts[1] == "file-history" && parts[2] == resumeID {
		return true
	}
	return len(parts) >= 4 && parts[0] == ".claude" && parts[1] == "projects" &&
		(parts[3] == resumeID || (len(parts) == 4 && parts[3] == resumeID+".jsonl"))
}

type conversationFiles struct{ home *os.File }

func openConversationFiles(home string) (*conversationFiles, error) {
	f, err := os.Open(home)
	if err != nil {
		return nil, err
	}
	return &conversationFiles{f}, nil
}

// Pin each parent directory and refuse symlinks before opening the next component.
func (c *conversationFiles) parent(name string, create bool) (*os.File, string, error) {
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

func (c *conversationFiles) Open(name string) (fs.File, error) {
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

func (c *conversationFiles) restore(src io.Reader, name string, directory bool, now time.Time) error {
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
	if err := unix.Linkat(fd, temporary, fd, leaf, 0); err != nil && err != unix.EEXIST {
		return err
	}
	return nil
}
