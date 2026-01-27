package sylkdir

import (
	"testing"
)

func BenchmarkSylkDirInit(b *testing.B) {
	for i := 0; i < b.N; i++ {
		tmpDir := b.TempDir()
		sd := New(tmpDir)
		if err := sd.Init(); err != nil {
			b.Fatalf("Init failed: %v", err)
		}
	}
}

func BenchmarkSylkDirValidate(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sd.Validate(); err != nil {
			b.Fatalf("Validate failed: %v", err)
		}
	}
}

func BenchmarkSylkDirLockUnlock(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sd.Lock(); err != nil {
			b.Fatalf("Lock failed: %v", err)
		}
		if err := sd.Unlock(); err != nil {
			b.Fatalf("Unlock failed: %v", err)
		}
	}
}

func BenchmarkSylkDirExists(b *testing.B) {
	tmpDir := b.TempDir()
	sd := New(tmpDir)
	if err := sd.Init(); err != nil {
		b.Fatalf("Init failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sd.Exists()
	}
}

func BenchmarkSylkDirPathAccessors(b *testing.B) {
	sd := New("/project/root")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sd.RootPath()
		_ = sd.KnowledgePath()
		_ = sd.NodesPath()
		_ = sd.EdgesPath()
		_ = sd.VectorsPath()
		_ = sd.SessionsPath()
	}
}
