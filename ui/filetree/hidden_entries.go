package filetree

// hiddenVCSDirs are internal repository metadata directories that should not
// appear in the file tree or file-tree search results.
var hiddenVCSDirs = map[string]struct{}{
	".git": {},
	".hg":  {},
	".svn": {},
	".bzr": {},
	".jj":  {},
}

func shouldSkipTreeEntry(name string, isDir bool) bool {
	if !isDir {
		return false
	}
	_, ok := hiddenVCSDirs[name]
	return ok
}
