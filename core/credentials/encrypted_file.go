package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/argon2"
)

type EncryptedFileStore struct {
	path string
	key  []byte
	mu   sync.RWMutex
}

type credentialData struct {
	Profiles map[string]map[string]string `json:"profiles"`
}

func NewEncryptedFileStore(dir string) (*EncryptedFileStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "credentials.enc")
	key, err := deriveEncryptionKey(dir)
	if err != nil {
		return nil, err
	}

	return &EncryptedFileStore{
		path: path,
		key:  key,
	}, nil
}

func (e *EncryptedFileStore) Get(profile, provider string) (string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	data, err := e.load()
	if err != nil {
		return "", err
	}

	if profileData, ok := data.Profiles[profile]; ok {
		if secret, ok := profileData[provider]; ok {
			return secret, nil
		}
	}

	return "", fmt.Errorf("credential not found")
}

func (e *EncryptedFileStore) Set(profile, provider, secret string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	data, err := e.load()
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if data.Profiles == nil {
		data.Profiles = make(map[string]map[string]string)
	}
	if data.Profiles[profile] == nil {
		data.Profiles[profile] = make(map[string]string)
	}
	data.Profiles[profile][provider] = secret

	return e.save(data)
}

func (e *EncryptedFileStore) Delete(profile, provider string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	data, err := e.load()
	if err != nil {
		return err
	}

	if profileData, ok := data.Profiles[profile]; ok {
		delete(profileData, provider)
	}

	return e.save(data)
}

func (e *EncryptedFileStore) ListKeys(profile string) ([]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	data, err := e.load()
	if err != nil {
		return nil, err
	}

	var keys []string
	if profileData, ok := data.Profiles[profile]; ok {
		for k := range profileData {
			keys = append(keys, k)
		}
	}

	return keys, nil
}

func (e *EncryptedFileStore) load() (*credentialData, error) {
	encrypted, err := os.ReadFile(e.path)
	if os.IsNotExist(err) {
		return &credentialData{Profiles: make(map[string]map[string]string)}, nil
	}
	if err != nil {
		return nil, err
	}

	plaintext, err := e.decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	var data credentialData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return &data, nil
}

func (e *EncryptedFileStore) save(data *credentialData) error {
	plaintext, err := json.Marshal(data)
	if err != nil {
		return err
	}

	encrypted, err := e.encrypt(plaintext)
	if err != nil {
		return err
	}

	tmpPath := e.path + ".tmp"
	if err := os.WriteFile(tmpPath, encrypted, 0600); err != nil {
		return err
	}

	return os.Rename(tmpPath, e.path)
}

func (e *EncryptedFileStore) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func (e *EncryptedFileStore) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func deriveEncryptionKey(dir string) ([]byte, error) {
	saltPath := filepath.Join(dir, ".salt")
	salt, err := getOrCreateSalt(saltPath)
	if err != nil {
		return nil, err
	}

	machineID := getMachineIdentifier()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}

	input := machineID + username
	key := argon2.IDKey([]byte(input), salt, 1, 64*1024, 4, 32)

	return key, nil
}

func getOrCreateSalt(path string) ([]byte, error) {
	salt, err := os.ReadFile(path)
	if err == nil && len(salt) == 32 {
		return salt, nil
	}

	salt = make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	if err := os.WriteFile(path, salt, 0600); err != nil {
		return nil, err
	}

	return salt, nil
}

func getMachineIdentifier() string {
	sources := []string{
		"/etc/machine-id",
		"/var/lib/dbus/machine-id",
	}

	for _, path := range sources {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return string(data)
		}
	}

	hostname, _ := os.Hostname()
	homeDir := os.Getenv("HOME")
	combined := hostname + homeDir + os.Getenv("USER")
	hash := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(hash[:])
}
