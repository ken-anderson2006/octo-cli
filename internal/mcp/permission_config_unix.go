//go:build linux || darwin

package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
	"golang.org/x/sys/unix"
)

type readinessPublishObserver struct {
	afterTempCreate   func(dir *os.File, tmpName, finalName string) error
	afterPartialWrite func(dir *os.File, tmpName, finalName string) error
	beforeRename      func(dir *os.File, tmpName, finalName string) error
}

func openPrivatePermissionConfig(dir *os.File, name string) (*os.File, error) {
	return secureFileAt(dir, name, unix.O_RDONLY)
}

func writePermissionReadiness(path, incarnation string) (func(), error) {
	return writePermissionReadinessWithObserver(path, incarnation, nil)
}

func writePermissionReadinessWithObserver(path, incarnation string, obs *readinessPublishObserver) (func(), error) { //nolint:gocyclo // atomic staging, validation, non-clobbering rename, and cleanup stay in one security boundary
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || filepath.Clean(path) != path {
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	name := filepath.Base(path)
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || len(name) > 180 {
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	b, err := permissionReadiness(incarnation)
	if err != nil {
		return nil, err
	}
	dir, err := secureDirectory(filepath.Dir(path), false)
	if err != nil {
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	var existing unix.Stat_t
	if unix.Fstatat(int(dir.Fd()), name, &existing, unix.AT_SYMLINK_NOFOLLOW) == nil {
		closePrivateFile(dir)
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	tmpName := "." + name + ".tmp." + permission.NewID()
	f, err := secureFileAt(dir, tmpName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		closePrivateFile(dir)
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	abortTemp := func() {
		closePrivateFile(f)
		_ = removeLogFile(dir, tmpName)
		_ = dir.Sync()
		closePrivateFile(dir)
	}
	if obs != nil && obs.afterTempCreate != nil {
		if err = obs.afterTempCreate(dir, tmpName, name); err != nil {
			abortTemp()
			return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
		}
	}
	if obs != nil && obs.afterPartialWrite != nil && len(b) > 1 {
		mid := len(b) / 2
		n, werr := f.Write(b[:mid])
		if werr != nil || n != mid {
			abortTemp()
			return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
		}
		if err = obs.afterPartialWrite(dir, tmpName, name); err != nil {
			abortTemp()
			return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
		}
		n2, werr := f.Write(b[mid:])
		if werr != nil || n2 != len(b)-mid {
			abortTemp()
			return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
		}
	} else {
		n, werr := f.Write(b)
		if werr != nil || n != len(b) {
			abortTemp()
			return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
		}
	}
	if err = secureStat(f, false, false); err != nil {
		abortTemp()
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	var tmpStat unix.Stat_t
	if unix.Fstat(int(f.Fd()), &tmpStat) != nil || tmpStat.Size != int64(len(b)) {
		abortTemp()
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	if err = f.Sync(); err != nil {
		abortTemp()
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	if err = f.Close(); err != nil {
		_ = removeLogFile(dir, tmpName)
		_ = dir.Sync()
		closePrivateFile(dir)
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	if obs != nil && obs.beforeRename != nil {
		if err = obs.beforeRename(dir, tmpName, name); err != nil {
			_ = removeLogFile(dir, tmpName)
			_ = dir.Sync()
			closePrivateFile(dir)
			return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
		}
	}
	if err = renameExclusiveAt(dir, tmpName, name); err != nil {
		_ = removeLogFile(dir, tmpName)
		_ = dir.Sync()
		closePrivateFile(dir)
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	published, err := secureFileAt(dir, name, unix.O_RDONLY)
	if err != nil {
		_ = removeLogFile(dir, name)
		_ = dir.Sync()
		closePrivateFile(dir)
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	var pubStat unix.Stat_t
	statErr := unix.Fstat(int(published.Fd()), &pubStat)
	closePrivateFile(published)
	if statErr != nil || pubStat.Dev != tmpStat.Dev || pubStat.Ino != tmpStat.Ino || pubStat.Size != int64(len(b)) {
		_ = removeLogFile(dir, name)
		_ = dir.Sync()
		closePrivateFile(dir)
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	if err = dir.Sync(); err != nil {
		_ = removeLogFile(dir, name)
		closePrivateFile(dir)
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			defer closePrivateFile(dir)
			var cur unix.Stat_t
			if unix.Fstatat(int(dir.Fd()), name, &cur, unix.AT_SYMLINK_NOFOLLOW) == nil && cur.Dev == tmpStat.Dev && cur.Ino == tmpStat.Ino {
				_ = removeLogFile(dir, name)
				_ = dir.Sync()
			}
		})
	}
	return cleanup, nil
}
