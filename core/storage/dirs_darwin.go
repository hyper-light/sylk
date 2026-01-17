//go:build darwin

package storage

import (
	"os"
	"path/filepath"
)

func platformConfigDefault() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Preferences", "sylk")
}

func platformDataDefault() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "sylk")
}

func platformCacheDefault() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Caches", "sylk")
}

func platformStateDefault() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Logs", "sylk")
}
