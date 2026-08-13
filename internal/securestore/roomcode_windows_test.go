//go:build windows

package securestore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRoomCodeStoreRoundTripsCurrentUserDPAPIWithoutPlaintext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "room.code")
	store, err := NewRoomCodeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	roomCode := []byte("7X4K-329B-YY95")
	if err := store.Save(roomCode); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, roomCode) {
		t.Fatal("protected file contains the plaintext room code")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	defer clearBytes(loaded)
	if !bytes.Equal(loaded, roomCode) {
		t.Fatalf("loaded room code=%q", loaded)
	}
}

func TestRoomCodeStoreRejectsTamperedCiphertext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "room.code")
	store, err := NewRoomCodeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save([]byte("AAAA-BBBB-CCCC")); err != nil {
		t.Fatal(err)
	}
	envelope, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	envelope[len(envelope)-1] ^= 0xff
	if err := os.WriteFile(path, envelope, 0o600); err != nil {
		t.Fatal(err)
	}
	if cleartext, err := store.Load(); err == nil {
		clearBytes(cleartext)
		t.Fatal("tampered DPAPI ciphertext was accepted")
	}
}

func TestRoomCodeStorePreservesPreviousCiphertextWhenReplaceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "room.code")
	store, err := NewRoomCodeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save([]byte("AAAA-BBBB-CCCC")); err != nil {
		t.Fatal(err)
	}
	store.replace = func(string, string) error { return errors.New("replace failed") }
	if err := store.Save([]byte("DDDD-EEEE-FFFF")); err == nil {
		t.Fatal("replace failure was ignored")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	defer clearBytes(loaded)
	if string(loaded) != "AAAA-BBBB-CCCC" {
		t.Fatalf("previous protected value was corrupted: %q", loaded)
	}
}

func TestRoomCodeStoreValidatesEnvelopeAndClearsIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "room.code")
	store, err := NewRoomCodeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNoProtectedRoomCode) {
		t.Fatalf("missing room code error=%v", err)
	}
	if err := os.WriteFile(path, []byte("not-a-dpapi-envelope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("invalid room code envelope was accepted")
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
}

func TestRoomCodeStoreRejectsOversizedEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "room.code")
	store, err := NewRoomCodeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	oversized := bytes.Repeat([]byte{'x'}, roomCodeCiphertextLimit+len(roomCodeEnvelopeMagic)+2)
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("oversized room code envelope was accepted")
	}
}
