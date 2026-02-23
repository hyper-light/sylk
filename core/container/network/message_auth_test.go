package network

import (
	"errors"
	"testing"
)

func TestMessageAuth_SignAndVerify(t *testing.T) {
	ma := NewMessageAuthenticator()
	ma.RegisterKey("container-1", []byte("secret-key"))

	payload := []byte("hello world")
	sig, err := ma.Sign("container-1", payload)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	if sig == "" {
		t.Fatal("signature should not be empty")
	}

	if err := ma.Verify("container-1", payload, sig); err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
}

func TestMessageAuth_TamperedPayload(t *testing.T) {
	ma := NewMessageAuthenticator()
	ma.RegisterKey("container-1", []byte("secret-key"))

	payload := []byte("original")
	sig, _ := ma.Sign("container-1", payload)

	tampered := []byte("modified")
	err := ma.Verify("container-1", tampered, sig)
	if !errors.Is(err, ErrMessageTampered) {
		t.Fatalf("expected ErrMessageTampered, got %v", err)
	}
}

func TestMessageAuth_UnknownContainer(t *testing.T) {
	ma := NewMessageAuthenticator()

	_, err := ma.Sign("unknown", []byte("data"))
	if !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("expected ErrNoSigningKey, got %v", err)
	}

	err = ma.Verify("unknown", []byte("data"), "sig")
	if !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("expected ErrNoSigningKey, got %v", err)
	}
}

func TestMessageAuth_RegisterUnregister(t *testing.T) {
	ma := NewMessageAuthenticator()
	ma.RegisterKey("c1", []byte("key1"))
	ma.RegisterKey("c2", []byte("key2"))

	if ma.KeyCount() != 2 {
		t.Fatalf("expected 2 keys, got %d", ma.KeyCount())
	}

	ma.UnregisterKey("c1")
	if ma.KeyCount() != 1 {
		t.Fatalf("expected 1 key, got %d", ma.KeyCount())
	}

	_, err := ma.Sign("c1", []byte("data"))
	if !errors.Is(err, ErrNoSigningKey) {
		t.Fatal("should not sign after unregister")
	}
}

func TestMessageAuth_DifferentKeysDifferentSigs(t *testing.T) {
	ma := NewMessageAuthenticator()
	ma.RegisterKey("c1", []byte("key1"))
	ma.RegisterKey("c2", []byte("key2"))

	payload := []byte("same data")
	sig1, _ := ma.Sign("c1", payload)
	sig2, _ := ma.Sign("c2", payload)

	if sig1 == sig2 {
		t.Fatal("different keys should produce different signatures")
	}
}
