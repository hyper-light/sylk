//go:build linux

package storage

import (
	"os"
	"path/filepath"
)

func platformConfigDefault() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "sylk")
}

func platformDataDefault() string {
	return filepath.Join(os.Getenv("HOME"), ".local", "share", "sylk")
}

func platformCacheDefault() string {
	return filepath.Join(os.Getenv("HOME"), ".cache", "sylk")
}

func platformStateDefault() string {
	return filepath.Join(os.Getenv("HOME"), ".local", "state", "sylk")
}
