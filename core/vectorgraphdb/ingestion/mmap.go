package ingestion

import (
	"bytes"
	"cmp"
	"context"
	"os"
	"slices"
	"sync"
)

// =============================================================================
// File Reading (Optimized for Speed)
// =============================================================================

// ReadFiles reads all files in parallel and returns MappedFile slices.
// Uses os.ReadFile which is highly optimized with minimal syscalls.
// Memory-mapping is not used as Go's file reading is already efficient
// and avoids mmap overhead for files < 100KB (majority of source files).
func ReadFiles(ctx context.Context, files []FileInfo, workers int) ([]MappedFile, error) {
	r := &fileReader{
		files:   files,
		workers: workers,
		results: NewAccumulator[MappedFile](len(files)),
		errors:  NewAccumulator[FileParseError](0),
	}

	return r.readAll(ctx)
}

// fileReader manages parallel file reading.
type fileReader struct {
	files   []FileInfo
	workers int
	results *Accumulator[MappedFile]
	errors  *Accumulator[FileParseError]
}

// readAll reads all files in parallel.
func (r *fileReader) readAll(ctx context.Context) ([]MappedFile, error) {
	workChan := make(chan FileInfo, len(r.files))

	var wg sync.WaitGroup
	wg.Add(r.workers + 1)

	go r.sendWork(ctx, workChan, &wg)

	for range r.workers {
		go r.worker(ctx, workChan, &wg)
	}

	wg.Wait()
	return r.results.Items(), nil
}

func (r *fileReader) sendWork(ctx context.Context, workChan chan<- FileInfo, wg *sync.WaitGroup) {
	defer wg.Done()
	defer close(workChan)

	for _, f := range r.files {
		select {
		case <-ctx.Done():
			return
		case workChan <- f:
		}
	}
}

// worker reads files from the work channel.
func (r *fileReader) worker(ctx context.Context, workChan <-chan FileInfo, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case f, ok := <-workChan:
			if !ok {
				return
			}
			r.readSingleFile(f)
		}
	}
}

// readSingleFile reads a single file and adds it to results.
func (r *fileReader) readSingleFile(f FileInfo) {
	mapped, err := readFileToMapped(f)
	if err != nil {
		r.errors.Append(FileParseError{Path: f.Path, Error: err.Error()})
		return
	}
	r.results.Append(mapped)
}

// readFileToMapped reads a file and returns a MappedFile.
func readFileToMapped(f FileInfo) (MappedFile, error) {
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return MappedFile{}, err
	}

	ext := extractExtension(f.Path)

	return MappedFile{
		Path: f.Path,
		Data: data,
		Lang: GetLanguage(ext),
		Size: f.Size,
	}, nil
}

func PartitionFiles(files []MappedFile, n int) [][]MappedFile {
	if n <= 0 {
		n = 1
	}
	if len(files) == 0 {
		return make([][]MappedFile, n)
	}

	partitions := make([][]MappedFile, n)
	partitionSizes := make([]int64, n)

	avgPerPartition := (len(files) + n - 1) / n
	for i := range partitions {
		partitions[i] = make([]MappedFile, 0, avgPerPartition)
	}

	for _, f := range files {
		minIdx := findMinPartition(partitionSizes)
		partitions[minIdx] = append(partitions[minIdx], f)
		partitionSizes[minIdx] += f.Size
	}

	for i := range partitions {
		sortByLanguage(partitions[i])
	}

	return partitions
}

func findMinPartition(sizes []int64) int {
	minIdx := 0
	minSize := sizes[0]
	for i, size := range sizes[1:] {
		if size < minSize {
			minIdx = i + 1
			minSize = size
		}
	}
	return minIdx
}

func sortByLanguage(files []MappedFile) {
	slices.SortFunc(files, func(a, b MappedFile) int {
		return cmp.Compare(a.Lang, b.Lang)
	})
}

func CountLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}

	count := bytes.Count(data, []byte{'\n'})

	if data[len(data)-1] != '\n' {
		count++
	}

	return count
}
