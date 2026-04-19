package register

import "testing"

// TestStore_ClipboardRegister_RoutesToProviderOnSet is the UI-11b
// acceptance test: writing to the "+ register must push content to
// the installed clipboard provider. Before UI-11b, "+ went to the
// in-memory `special` map and never touched the OS clipboard,
// breaking copy-to-system-clipboard from vim.
func TestStore_ClipboardRegister_RoutesToProviderOnSet(t *testing.T) {
	store := NewStore()
	cb := &InternalClipboard{}
	store.SetClipboard(cb)

	store.Set('+', "shared across processes", false)

	got, err := cb.Get()
	if err != nil {
		t.Fatalf("clipboard.Get: %v", err)
	}
	if got != "shared across processes" {
		t.Errorf("clipboard provider content = %q, want shared across processes", got)
	}
}

// TestStore_ClipboardRegister_ReadsFromProviderOnGet covers the
// paste direction: reading "+ pulls from the provider, not the
// in-memory cache. Mirrors the Set test.
func TestStore_ClipboardRegister_ReadsFromProviderOnGet(t *testing.T) {
	store := NewStore()
	cb := &InternalClipboard{}
	if err := cb.Set("from another pane"); err != nil {
		t.Fatalf("seed clipboard: %v", err)
	}
	store.SetClipboard(cb)

	got, _ := store.Get('+')
	if got != "from another pane" {
		t.Errorf("register Get(+) = %q, want from another pane", got)
	}
}

// TestStore_ClipboardRegister_FallsBackToCacheWhenNoProvider
// protects the headless / CI configuration. Without a provider the
// register must behave like a normal special register.
func TestStore_ClipboardRegister_FallsBackToCacheWhenNoProvider(t *testing.T) {
	store := NewStore()
	store.Set('*', "no-os-clipboard", false)

	got, _ := store.Get('*')
	if got != "no-os-clipboard" {
		t.Errorf("cache fallback content = %q, want no-os-clipboard", got)
	}
}

// TestStore_ClipboardRegister_ProviderGetErrorFallsBackToCache
// covers the error path. A real OS clipboard command can fail
// transiently (no DISPLAY, wl-copy killed, etc.); the editor must
// not surface the error — it degrades to the local cache instead.
func TestStore_ClipboardRegister_ProviderGetErrorFallsBackToCache(t *testing.T) {
	store := NewStore()
	store.Set('+', "cached-value", false)
	store.SetClipboard(&failingClipboard{err: errFailingClipboard})

	got, _ := store.Get('+')
	if got != "cached-value" {
		t.Errorf("provider error fallback = %q, want cached-value", got)
	}
}

// TestStore_ClipboardRegister_BothStarAndPlusRouteToProvider
// verifies both clipboard register names use the provider. Vim
// users type either interchangeably; splitting them would produce
// unpredictable behavior for yank-then-paste in quick succession.
func TestStore_ClipboardRegister_BothStarAndPlusRouteToProvider(t *testing.T) {
	store := NewStore()
	cb := &InternalClipboard{}
	store.SetClipboard(cb)

	store.Set('*', "via-star", false)
	if got, _ := store.Get('+'); got != "via-star" {
		t.Errorf("Get(+) after Set(*) = %q, want via-star", got)
	}
	store.Set('+', "via-plus", false)
	if got, _ := store.Get('*'); got != "via-plus" {
		t.Errorf("Get(*) after Set(+) = %q, want via-plus", got)
	}
}

// -----------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------

var errFailingClipboard = clipboardError("forced clipboard failure for test")

type clipboardError string

func (e clipboardError) Error() string { return string(e) }

type failingClipboard struct{ err error }

func (f *failingClipboard) Get() (string, error)      { return "", f.err }
func (f *failingClipboard) Set(_ string) error        { return f.err }
