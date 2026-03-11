package sylkdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// writeFileSync writes a file and fsyncs its contents before returning.
func writeFileSync(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// durableWriteFile publishes a file using temp write + rename + parent dir fsync.
func durableWriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := durableRename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// durableRename renames a file and fsyncs the destination directory.
func durableRename(oldPath, newPath string) error {
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	return syncPathDir(newPath)
}

// syncPathDir fsyncs the parent directory of a path.
func syncPathDir(path string) error {
	return syncDir(filepath.Dir(path))
}

// syncCreatedDir fsyncs a created directory and the parent directory entry that references it.
func syncCreatedDir(dir string) error {
	if err := syncDir(dir); err != nil {
		return err
	}
	return syncDir(filepath.Dir(dir))
}

// syncDir fsyncs a directory on platforms that support it.
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	if err := f.Sync(); err != nil && !ignorableDirSyncError(err) {
		return fmt.Errorf("sync dir %s: %w", path, err)
	}
	return nil
}

func ignorableDirSyncError(err error) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP)
}
