//go:build linux

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
	return unix.Renameat2(int(dir.Fd()), from, int(dir.Fd()), to, unix.RENAME_NOREPLACE)
}
