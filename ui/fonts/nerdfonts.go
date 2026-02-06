// Package fonts provides detection and auto-installation of Nerd Font symbols.
package fonts

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// nerdFontFamily is the fc-list family name for Symbols Nerd Font.
const nerdFontFamily = "Symbols Nerd Font"

// symbolsURL is the release URL for the symbols-only Nerd Font package.
const symbolsURL = "https://github.com/ryanoasis/nerd-fonts/releases/download/v3.4.0/NerdFontsSymbolsOnly.zip"

// installSubdir is the font directory name under ~/.local/share/fonts/.
const installSubdir = "NerdFontsSymbolsOnly"

// fontconfigFile is the name of the fontconfig snippet that registers
// Symbols Nerd Font as a global symbol fallback.
const fontconfigFile = "10-sylk-nerd-symbols.conf"

// maxDownloadBytes caps the zip download to prevent unbounded reads.
// Derived from: NerdFontsSymbolsOnly.zip is ~4 MB; 16 MB is a safe ceiling.
const maxDownloadBytes = 16 << 20

// fontconfigSnippet tells fontconfig to append Symbols Nerd Font (and its
// Mono variant) as a fallback for every font family. VTE-based terminals
// (Terminator, gnome-terminal, xfce4-terminal) use Pango → fontconfig,
// so this makes PUA glyphs resolve to the symbols font automatically.
const fontconfigSnippet = `<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
<fontconfig>
  <description>Sylk: add Nerd Font Symbols as global fallback</description>

  <!-- Append symbol fonts so PUA codepoints resolve automatically. -->
  <match target="pattern">
    <edit name="family" mode="append">
      <string>Symbols Nerd Font</string>
    </edit>
  </match>
  <match target="pattern">
    <edit name="family" mode="append">
      <string>Symbols Nerd Font Mono</string>
    </edit>
  </match>
</fontconfig>
`

// Detected reports whether Nerd Font symbols are installed AND the
// fontconfig fallback snippet is in place.
func Detected() bool {
	return fontInstalled() && fontconfigInstalled()
}

// fontInstalled checks fc-list for the Symbols Nerd Font family.
func fontInstalled() bool {
	out, err := exec.Command("fc-list", ":", "family").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), nerdFontFamily)
}

// fontconfigInstalled checks whether the fontconfig snippet exists.
func fontconfigInstalled() bool {
	path, err := fontconfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Install downloads and installs the Nerd Fonts Symbols Only package
// (if not already present) and writes the fontconfig fallback snippet.
// Returns nil on success.
func Install() error {
	return InstallCtx(context.Background())
}

// InstallCtx is like Install but respects context cancellation for the
// network download, allowing clean shutdown when the app exits.
func InstallCtx(ctx context.Context) error {
	if !fontInstalled() {
		if err := installFontCtx(ctx); err != nil {
			return err
		}
	}
	if !fontconfigInstalled() {
		return installFontconfig()
	}
	return nil
}

// installFont downloads the zip, extracts fonts, and refreshes fc-cache.
func installFont() error {
	return installFontCtx(context.Background())
}

// installFontCtx is like installFont but supports context cancellation.
func installFontCtx(ctx context.Context) error {
	zipPath, err := downloadCtx(ctx)
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)
	return installFromZip(zipPath)
}

// installFontconfig writes the fontconfig snippet that registers the
// symbols font as a global fallback.
func installFontconfig() error {
	path, err := fontconfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fontconfigSnippet), 0o644)
}

// fontconfigPath returns the path to the fontconfig snippet.
func fontconfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "fontconfig", "conf.d", fontconfigFile), nil
}

// download fetches the symbols zip to a temporary file.
func download() (string, error) {
	return downloadCtx(context.Background())
}

// downloadCtx fetches the symbols zip with context cancellation support.
func downloadCtx(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, symbolsURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return writeToTemp(io.LimitReader(resp.Body, maxDownloadBytes))
}

// writeToTemp persists the reader content to a temp file and returns its path.
func writeToTemp(r io.Reader) (string, error) {
	tmp, err := os.CreateTemp("", "nerdfonts-*.zip")
	if err != nil {
		return "", err
	}
	if _, err = io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

// installFromZip extracts font files and refreshes the font cache.
func installFromZip(zipPath string) error {
	dir, err := fontDir()
	if err != nil {
		return err
	}
	if err := extractFonts(zipPath, dir); err != nil {
		return err
	}
	return refreshCache()
}

// fontDir returns the install directory, creating it if necessary.
func fontDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "share", "fonts", installSubdir)
	return dir, os.MkdirAll(dir, 0o755)
}

// extractFonts opens the zip and writes all .ttf/.otf files to destDir.
func extractFonts(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		if isFontFile(f.Name) {
			_ = extractOneFont(f, destDir)
		}
	}
	return nil
}

// isFontFile reports whether the filename has a font extension.
func isFontFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".ttf" || ext == ".otf"
}

// extractOneFont writes a single zip entry to destDir.
func extractOneFont(f *zip.File, destDir string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(filepath.Join(destDir, filepath.Base(f.Name)))
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// refreshCache runs fc-cache to make newly installed fonts available.
func refreshCache() error {
	return exec.Command("fc-cache", "-f").Run()
}
