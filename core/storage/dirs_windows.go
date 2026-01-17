//go:build windows

package storage

import (
	"os"
	"path/filepath"
)

func platformConfigDefault() string {
	return filepath.Join(os.Getenv("APPDATA"), "sylk", "config")
}

func platformDataDefault() string {
	return filepath.Join(os.Getenv("APPDATA"), "sylk", "data")
}

func platformCacheDefault() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "sylk", "cache")
}

func platformStateDefault() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "sylk", "state")
}
