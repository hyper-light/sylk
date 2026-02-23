//go:build !linux && !darwin && !freebsd && !netbsd

package git

import (
	"io/fs"

	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// fillEntrySystemInfo is a no-op on platforms without accessible stat fields.
// Dev, Inode, UID, GID remain zero. CreatedAt is not set.
// This is safe: these fields are only used for stat-dirty optimization and
// their absence simply forces content hashing on next status check.
func fillEntrySystemInfo(_ *index.Entry, _ fs.FileInfo) {}
