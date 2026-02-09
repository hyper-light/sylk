package sylkdir

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestSharedDataFile_AppendReadAt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")

	sdf, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sdf.Close()

	// Append first record.
	data1 := []byte("hello")
	offset1, err := sdf.Append(data1)
	if err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if offset1 != 0 {
		t.Errorf("offset1 = %d, want 0", offset1)
	}

	// Append second record.
	data2 := []byte("world!")
	offset2, err := sdf.Append(data2)
	if err != nil {
		t.Fatalf("Append 2: %v", err)
	}
	if offset2 != 5 {
		t.Errorf("offset2 = %d, want 5", offset2)
	}

	// Read back first record.
	buf := make([]byte, 5)
	n, err := sdf.ReadAt(buf, offset1)
	if err != nil || n != 5 {
		t.Fatalf("ReadAt 1: n=%d, err=%v", n, err)
	}
	if string(buf) != "hello" {
		t.Errorf("ReadAt 1 = %q, want %q", buf, "hello")
	}

	// Read back second record.
	buf = make([]byte, 6)
	n, err = sdf.ReadAt(buf, offset2)
	if err != nil || n != 6 {
		t.Fatalf("ReadAt 2: n=%d, err=%v", n, err)
	}
	if string(buf) != "world!" {
		t.Errorf("ReadAt 2 = %q, want %q", buf, "world!")
	}
}

func TestSharedDataFile_Size(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")

	sdf, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sdf.Close()

	if sdf.Size() != 0 {
		t.Errorf("initial size = %d, want 0", sdf.Size())
	}

	sdf.Append([]byte("12345"))
	if sdf.Size() != 5 {
		t.Errorf("size after append = %d, want 5", sdf.Size())
	}

	sdf.Append([]byte("67890"))
	if sdf.Size() != 10 {
		t.Errorf("size after second append = %d, want 10", sdf.Size())
	}
}

func TestSharedDataFile_ReopenPreservesData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")

	// Write data.
	sdf, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	sdf.Append([]byte("persistent"))
	sdf.Close()

	// Reopen and verify.
	sdf2, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer sdf2.Close()

	if sdf2.Size() != 10 {
		t.Errorf("reopened size = %d, want 10", sdf2.Size())
	}

	buf := make([]byte, 10)
	n, err := sdf2.ReadAt(buf, 0)
	if err != nil || n != 10 {
		t.Fatalf("ReadAt after reopen: n=%d, err=%v", n, err)
	}
	if string(buf) != "persistent" {
		t.Errorf("data = %q, want %q", buf, "persistent")
	}

	// Append after reopen continues from end.
	offset, err := sdf2.Append([]byte("_more"))
	if err != nil {
		t.Fatalf("Append after reopen: %v", err)
	}
	if offset != 10 {
		t.Errorf("offset after reopen append = %d, want 10", offset)
	}
}

func TestSharedDataFile_ConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.bin")

	sdf, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sdf.Close()

	writers := 8
	writesPerWriter := 100
	recordSize := 16
	record := make([]byte, recordSize)

	var wg sync.WaitGroup
	offsets := make([][]int64, writers)

	for w := range writers {
		offsets[w] = make([]int64, writesPerWriter)
		wg.Add(1)
		go func(wIdx int) {
			defer wg.Done()
			for i := range writesPerWriter {
				offset, err := sdf.Append(record)
				if err != nil {
					t.Errorf("writer %d append %d: %v", wIdx, i, err)
					return
				}
				offsets[wIdx][i] = offset
			}
		}(w)
	}
	wg.Wait()

	expectedSize := int64(writers * writesPerWriter * recordSize)
	if sdf.Size() != expectedSize {
		t.Errorf("size = %d, want %d", sdf.Size(), expectedSize)
	}

	// Verify no overlapping offsets.
	seen := make(map[int64]bool)
	for _, writerOffsets := range offsets {
		for _, offset := range writerOffsets {
			if seen[offset] {
				t.Errorf("duplicate offset %d", offset)
			}
			seen[offset] = true
		}
	}
}

func TestSharedDataFile_ConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reads.bin")

	sdf, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sdf.Close()

	// Write known data.
	records := 100
	offsets := make([]int64, records)
	for i := range records {
		data := []byte{byte(i), byte(i + 1), byte(i + 2), byte(i + 3)}
		offset, err := sdf.Append(data)
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		offsets[i] = offset
	}

	// Concurrent reads.
	var wg sync.WaitGroup
	for i := range records {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			buf := make([]byte, 4)
			_, err := sdf.ReadAt(buf, offsets[idx])
			if err != nil {
				t.Errorf("ReadAt %d: %v", idx, err)
				return
			}
			if buf[0] != byte(idx) {
				t.Errorf("ReadAt %d: got %d, want %d", idx, buf[0], idx)
			}
		}(i)
	}
	wg.Wait()
}

func TestSharedDataFile_CompactTo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	destPath := filepath.Join(dir, "compacted.bin")

	sdf, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sdf.Close()

	// Write 4 records: [size:4][data:N]
	offsets := make([]int64, 4)
	for i := range 4 {
		rec := make([]byte, 8)
		rec[0] = byte(4) // size prefix = 4
		rec[4] = byte(i) // payload marker
		off, err := sdf.Append(rec)
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		offsets[i] = off
	}

	// Compact keeping records 0 and 2 only
	liveOffsets := []int64{offsets[0], offsets[2]}
	sizeFn := func(_ *SharedDataFile, _ int64) (int, error) {
		return 8, nil // fixed record size
	}

	remap, err := sdf.CompactTo(destPath, liveOffsets, sizeFn)
	if err != nil {
		t.Fatalf("CompactTo: %v", err)
	}

	if len(remap) != 2 {
		t.Fatalf("remap has %d entries, want 2", len(remap))
	}

	// Verify remap: record 0 → offset 0, record 2 → offset 8
	if remap[offsets[0]] != 0 {
		t.Errorf("remap[%d] = %d, want 0", offsets[0], remap[offsets[0]])
	}
	if remap[offsets[2]] != 8 {
		t.Errorf("remap[%d] = %d, want 8", offsets[2], remap[offsets[2]])
	}

	// Verify compacted file has correct content
	compacted, err := OpenSharedDataFile(destPath)
	if err != nil {
		t.Fatalf("Open compacted: %v", err)
	}
	defer compacted.Close()

	if compacted.Size() != 16 {
		t.Errorf("compacted size = %d, want 16", compacted.Size())
	}

	// First record should have marker 0
	buf := make([]byte, 8)
	compacted.ReadAt(buf, 0)
	if buf[4] != 0 {
		t.Errorf("first compacted record marker = %d, want 0", buf[4])
	}

	// Second record should have marker 2
	compacted.ReadAt(buf, 8)
	if buf[4] != 2 {
		t.Errorf("second compacted record marker = %d, want 2", buf[4])
	}
}

func TestSharedDataFile_ReplaceFile(t *testing.T) {
	dir := t.TempDir()
	origPath := filepath.Join(dir, "data.bin")
	newPath := filepath.Join(dir, "new.bin")

	// Create original with some data
	sdf, err := OpenSharedDataFile(origPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sdf.Append([]byte("original_data_longer"))

	// Create replacement file with different, shorter data
	newFile, err := OpenSharedDataFile(newPath)
	if err != nil {
		t.Fatalf("Open new: %v", err)
	}
	newFile.Append([]byte("new"))
	newFile.Close()

	// Replace
	if err := sdf.ReplaceFile(newPath); err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}
	defer sdf.Close()

	if sdf.Size() != 3 {
		t.Errorf("size after replace = %d, want 3", sdf.Size())
	}

	buf := make([]byte, 3)
	sdf.ReadAt(buf, 0)
	if string(buf) != "new" {
		t.Errorf("after replace: got %q, want %q", buf, "new")
	}

	// Path should still be original
	if sdf.Path() != origPath {
		t.Errorf("path = %q, want %q", sdf.Path(), origPath)
	}
}

func TestSharedDataFile_CreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "data.bin")

	sdf, err := OpenSharedDataFile(path)
	if err != nil {
		t.Fatalf("Open with nested dirs: %v", err)
	}
	defer sdf.Close()

	_, err = sdf.Append([]byte("test"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestAppendBatch(t *testing.T) {
	dir := t.TempDir()
	sdf, err := OpenSharedDataFile(filepath.Join(dir, "batch.dat"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sdf.Close()

	// Write a prefix via regular Append to test interleaving.
	prefix := []byte("prefix")
	prefixOff, err := sdf.Append(prefix)
	if err != nil {
		t.Fatalf("Append prefix: %v", err)
	}
	if prefixOff != 0 {
		t.Fatalf("prefix offset = %d, want 0", prefixOff)
	}

	// Batch append three records.
	records := [][]byte{
		[]byte("aaaa"),
		[]byte("bb"),
		[]byte("cccccc"),
	}
	offsets, err := sdf.AppendBatch(records)
	if err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	if len(offsets) != 3 {
		t.Fatalf("got %d offsets, want 3", len(offsets))
	}

	// Verify offsets are contiguous after prefix.
	wantBase := int64(len(prefix))
	if offsets[0] != wantBase {
		t.Errorf("offsets[0] = %d, want %d", offsets[0], wantBase)
	}
	if offsets[1] != wantBase+4 {
		t.Errorf("offsets[1] = %d, want %d", offsets[1], wantBase+4)
	}
	if offsets[2] != wantBase+6 {
		t.Errorf("offsets[2] = %d, want %d", offsets[2], wantBase+6)
	}

	// Verify each record is readable at its offset.
	for i, rec := range records {
		buf := make([]byte, len(rec))
		n, readErr := sdf.ReadAt(buf, offsets[i])
		if readErr != nil {
			t.Errorf("ReadAt record %d: %v", i, readErr)
			continue
		}
		if n != len(rec) || string(buf) != string(rec) {
			t.Errorf("record %d: got %q, want %q", i, buf, rec)
		}
	}

	// Size should reflect all writes.
	wantSize := int64(len(prefix) + 4 + 2 + 6)
	if sdf.Size() != wantSize {
		t.Errorf("Size = %d, want %d", sdf.Size(), wantSize)
	}
}

func TestAppendBatchEmpty(t *testing.T) {
	dir := t.TempDir()
	sdf, err := OpenSharedDataFile(filepath.Join(dir, "empty_batch.dat"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sdf.Close()

	offsets, err := sdf.AppendBatch(nil)
	if err != nil {
		t.Fatalf("AppendBatch nil: %v", err)
	}
	if len(offsets) != 0 {
		t.Errorf("expected 0 offsets, got %d", len(offsets))
	}
	if sdf.Size() != 0 {
		t.Errorf("Size = %d after empty batch, want 0", sdf.Size())
	}
}
