//go:build linux

package git

import (
	"io/fs"
	"syscall"
	"time"

	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// fillEntrySystemInfo populates platform-specific index entry fields from
// the filesystem stat data. Matches go-git's worktree_linux.go exactly.
//
// Fields set: CreatedAt (from ctime), Dev, Inode, GID, UID.
// These are used by git's stat-dirty optimization to detect changes without
// hashing file content.
func fillEntrySystemInfo(e *index.Entry, info fs.FileInfo) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	e.CreatedAt = time.Unix(stat.Ctim.Unix())
	e.Dev = uint32(stat.Dev)
	e.Inode = uint32(stat.Ino)
	e.GID = stat.Gid
	e.UID = stat.Uid
}
