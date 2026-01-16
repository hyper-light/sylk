package archivalist

import "testing"

type mockSessionStore struct{}

type mockQueryCache struct{}

type mockWorkflowStore struct{}

type mockCrossSessionIndex struct{}

type mockTokenSavings struct{}

type mockStore struct{}

func (m *mockStore) GetCurrentSession() *Session                          { return nil }
func (m *mockStore) EndSession(summary string, primaryFocus string) error { return nil }
func (m *mockStore) InsertEntry(entry *Entry) (string, error)             { return "", nil }
func (m *mockStore) InsertEntryInSession(sessionID string, entry *Entry) (string, error) {
	return "", nil
}
func (m *mockStore) UpdateEntry(id string, updates func(*Entry)) error     { return nil }
func (m *mockStore) GetEntry(id string) (*Entry, bool)                     { return nil, false }
func (m *mockStore) Query(q ArchiveQuery) ([]*Entry, error)                { return nil, nil }
func (m *mockStore) QueryByCategory(category Category, limit int) []*Entry { return nil }
func (m *mockStore) SearchText(text string, includeArchived bool, limit int) ([]*Entry, error) {
	return nil, nil
}
func (m *mockStore) RestoreFromArchive(ids []string) error { return nil }
func (m *mockStore) Stats() StorageStats                   { return StorageStats{} }

func TestInterfaces_Compile(t *testing.T) {
	var _ StoreService = (*Store)(nil)
	var _ StoreService = (*mockStore)(nil)
}
