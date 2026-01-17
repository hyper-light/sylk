package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVFS_CopyOnWrite_ExistingFile(t *testing.T) {
	workDir := t.TempDir()

	originalFile := filepath.Join(workDir, "original.txt")
	require.NoError(t, os.WriteFile(originalFile, []byte("original content"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	stagedPath, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)

	t.Logf("originalFile: %s", originalFile)
	t.Logf("stagedPath: %s", stagedPath)
	t.Logf("stagingDir: %s", vfs.GetStagingDir())

	assert.NotEqual(t, originalFile, stagedPath)
	assert.True(t, vfs.IsStagingActive())

	stagedContent, err := os.ReadFile(stagedPath)
	require.NoError(t, err, "reading staged file")
	assert.Equal(t, "original content", string(stagedContent))

	require.NoError(t, os.WriteFile(stagedPath, []byte("modified content"), 0644))

	originalContent, err := os.ReadFile(originalFile)
	require.NoError(t, err)
	assert.Equal(t, "original content", string(originalContent))
}

func TestVFS_CopyOnWrite_SameFileTwice(t *testing.T) {
	workDir := t.TempDir()

	originalFile := filepath.Join(workDir, "file.txt")
	require.NoError(t, os.WriteFile(originalFile, []byte("content"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	stagedPath1, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)

	stagedPath2, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)

	assert.Equal(t, stagedPath1, stagedPath2)
}

func TestVFS_StageNewFile(t *testing.T) {
	workDir := t.TempDir()

	vfs := NewVirtualFilesystem(workDir, nil)

	newFilePath := filepath.Join(workDir, "new_file.txt")
	stagedPath, err := vfs.StageNewFile(newFilePath)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(stagedPath, []byte("new content"), 0644))

	_, err = os.Stat(newFilePath)
	assert.True(t, os.IsNotExist(err))
}

func TestVFS_StageDelete(t *testing.T) {
	workDir := t.TempDir()

	fileToDelete := filepath.Join(workDir, "to_delete.txt")
	require.NoError(t, os.WriteFile(fileToDelete, []byte("delete me"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	require.NoError(t, vfs.StageDelete(fileToDelete))

	_, err := os.Stat(fileToDelete)
	assert.NoError(t, err)
}

func TestVFS_MergeChanges_ModifiedFile(t *testing.T) {
	workDir := t.TempDir()

	originalFile := filepath.Join(workDir, "file.txt")
	require.NoError(t, os.WriteFile(originalFile, []byte("original"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	stagedPath, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(stagedPath, []byte("modified"), 0644))

	result, err := vfs.MergeChanges()
	require.NoError(t, err)

	assert.Equal(t, 1, len(result.Merged))
	assert.Equal(t, 0, len(result.Conflicts))
	assert.False(t, result.HasConflicts())

	content, err := os.ReadFile(originalFile)
	require.NoError(t, err)
	assert.Equal(t, "modified", string(content))
}

func TestVFS_MergeChanges_NewFile(t *testing.T) {
	workDir := t.TempDir()

	vfs := NewVirtualFilesystem(workDir, nil)

	newFilePath := filepath.Join(workDir, "new.txt")
	stagedPath, err := vfs.StageNewFile(newFilePath)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(stagedPath, []byte("new content"), 0644))

	result, err := vfs.MergeChanges()
	require.NoError(t, err)

	assert.Equal(t, 1, len(result.Created))
	assert.False(t, result.HasConflicts())

	content, err := os.ReadFile(newFilePath)
	require.NoError(t, err)
	assert.Equal(t, "new content", string(content))
}

func TestVFS_MergeChanges_DeletedFile(t *testing.T) {
	workDir := t.TempDir()

	fileToDelete := filepath.Join(workDir, "delete_me.txt")
	require.NoError(t, os.WriteFile(fileToDelete, []byte("content"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	require.NoError(t, vfs.StageDelete(fileToDelete))

	result, err := vfs.MergeChanges()
	require.NoError(t, err)

	assert.Equal(t, 1, len(result.Deleted))
	assert.False(t, result.HasConflicts())

	_, err = os.Stat(fileToDelete)
	assert.True(t, os.IsNotExist(err))
}

func TestVFS_MergeChanges_Conflict_ModifiedOriginal(t *testing.T) {
	workDir := t.TempDir()

	originalFile := filepath.Join(workDir, "file.txt")
	require.NoError(t, os.WriteFile(originalFile, []byte("original"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	stagedPath, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(stagedPath, []byte("staged modification"), 0644))

	require.NoError(t, os.WriteFile(originalFile, []byte("concurrent modification"), 0644))

	result, err := vfs.MergeChanges()
	require.NoError(t, err)

	assert.True(t, result.HasConflicts())
	assert.Equal(t, 1, len(result.Conflicts))

	content, err := os.ReadFile(originalFile)
	require.NoError(t, err)
	assert.Equal(t, "concurrent modification", string(content))
}

func TestVFS_MergeChanges_Conflict_NewFileAlreadyExists(t *testing.T) {
	workDir := t.TempDir()

	vfs := NewVirtualFilesystem(workDir, nil)

	newFilePath := filepath.Join(workDir, "new.txt")
	stagedPath, err := vfs.StageNewFile(newFilePath)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(stagedPath, []byte("staged content"), 0644))

	require.NoError(t, os.WriteFile(newFilePath, []byte("someone else created this"), 0644))

	result, err := vfs.MergeChanges()
	require.NoError(t, err)

	assert.True(t, result.HasConflicts())
	assert.Equal(t, 1, len(result.Conflicts))
}

func TestVFS_DiscardChanges(t *testing.T) {
	workDir := t.TempDir()

	originalFile := filepath.Join(workDir, "file.txt")
	require.NoError(t, os.WriteFile(originalFile, []byte("original"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	stagedPath, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(stagedPath, []byte("modified"), 0644))

	assert.True(t, vfs.IsStagingActive())

	require.NoError(t, vfs.DiscardChanges())

	assert.False(t, vfs.IsStagingActive())

	content, err := os.ReadFile(originalFile)
	require.NoError(t, err)
	assert.Equal(t, "original", string(content))
}

func TestVFS_ListStagedFiles(t *testing.T) {
	workDir := t.TempDir()

	file1 := filepath.Join(workDir, "file1.txt")
	file2 := filepath.Join(workDir, "file2.txt")
	require.NoError(t, os.WriteFile(file1, []byte("content1"), 0644))
	require.NoError(t, os.WriteFile(file2, []byte("content2"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	_, err := vfs.CopyOnWrite(file1)
	require.NoError(t, err)

	_, err = vfs.StageNewFile(filepath.Join(workDir, "new.txt"))
	require.NoError(t, err)

	require.NoError(t, vfs.StageDelete(file2))

	staged := vfs.ListStagedFiles()
	assert.Equal(t, 3, len(staged))

	var hasNew, hasDeleted, hasModified bool
	for _, sf := range staged {
		if sf.IsNew {
			hasNew = true
		}
		if sf.IsDeleted {
			hasDeleted = true
		}
		if !sf.IsNew && !sf.IsDeleted {
			hasModified = true
		}
	}

	assert.True(t, hasNew)
	assert.True(t, hasDeleted)
	assert.True(t, hasModified)
}

func TestVFS_MergeResult_TotalChanges(t *testing.T) {
	result := &MergeResult{
		Merged:    []string{"a", "b"},
		Created:   []string{"c"},
		Deleted:   []string{"d", "e", "f"},
		Conflicts: []string{"g"},
	}

	assert.Equal(t, 6, result.TotalChanges())
	assert.True(t, result.HasConflicts())
}

func TestVFS_CopyOnWrite_SubDirectory(t *testing.T) {
	workDir := t.TempDir()

	subDir := filepath.Join(workDir, "subdir", "nested")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	originalFile := filepath.Join(subDir, "file.txt")
	require.NoError(t, os.WriteFile(originalFile, []byte("nested content"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	stagedPath, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)

	assert.Contains(t, stagedPath, "subdir")
	assert.Contains(t, stagedPath, "nested")

	content, err := os.ReadFile(stagedPath)
	require.NoError(t, err)
	assert.Equal(t, "nested content", string(content))
}

func TestVFS_MergeChanges_PreservesFilePermissions(t *testing.T) {
	workDir := t.TempDir()

	originalFile := filepath.Join(workDir, "executable.sh")
	require.NoError(t, os.WriteFile(originalFile, []byte("#!/bin/bash"), 0755))

	vfs := NewVirtualFilesystem(workDir, nil)

	stagedPath, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(stagedPath, []byte("#!/bin/bash\necho hello"), 0755))

	_, err = vfs.MergeChanges()
	require.NoError(t, err)

	info, err := os.Stat(originalFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
}

func TestVFS_MergeChanges_EmptyStaging(t *testing.T) {
	workDir := t.TempDir()

	vfs := NewVirtualFilesystem(workDir, nil)

	result, err := vfs.MergeChanges()
	require.NoError(t, err)

	assert.Equal(t, 0, result.TotalChanges())
	assert.False(t, result.HasConflicts())
}

func TestVFS_StagingCleanupAfterMerge(t *testing.T) {
	workDir := t.TempDir()

	originalFile := filepath.Join(workDir, "file.txt")
	require.NoError(t, os.WriteFile(originalFile, []byte("content"), 0644))

	vfs := NewVirtualFilesystem(workDir, nil)

	_, err := vfs.CopyOnWrite(originalFile)
	require.NoError(t, err)

	stagingDir := vfs.GetStagingDir()
	assert.NotEmpty(t, stagingDir)

	_, err = vfs.MergeChanges()
	require.NoError(t, err)

	_, err = os.Stat(stagingDir)
	assert.True(t, os.IsNotExist(err))
	assert.False(t, vfs.IsStagingActive())
}
