//go:build darwin || freebsd || netbsd

package git

import (
	"io/fs"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// fillEntrySystemInfo populates platform-specific index entry fields from
// the filesystem stat data. Matches go-git's worktree_bsd.go exactly.
//
// Fields set: CreatedAt (from access time), Dev, Inode, GID, UID.
// BSD/macOS uses Atimespec (access time) rather than birth time,
// matching go-git's convention.
func fillEntrySystemInfo(e *index.Entry, info fs.FileInfo) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	e.CreatedAt = time.Unix(stat.Atimespec.Unix())
	e.Dev = uint32(stat.Dev)
	e.Inode = uint32(stat.Ino)
	e.GID = stat.Gid
	e.UID = stat.Uid
}
