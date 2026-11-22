//go:build darwin

package mcp

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func renameExclusiveAt(dir *os.File, from, to string) error {
	if dir == nil || from == "" || to == "" || from == "." || from == ".." || to == "." || to == ".." || filepath.Base(from) != from || filepath.Base(to) != to {
		return errors.New("unsafe file")
	}
	err := unix.RenameatxNp(int(dir.Fd()), from, int(dir.Fd()), to, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) {
		err = unix.RenameatxNp(int(dir.Fd()), from, int(dir.Fd()), to, unix.RENAME_EXCL)
	}
	return err
}
