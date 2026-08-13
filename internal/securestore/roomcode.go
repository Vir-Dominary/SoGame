package securestore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	roomCodeMaximumSize     = 64
	roomCodeCiphertextLimit = 4 << 10
	roomCodeEnvelopeVersion = byte(1)
)

var (
	roomCodeEnvelopeMagic    = []byte{'S', 'G', 'N', 'B', 'R', 'C'}
	ErrNoProtectedRoomCode   = errors.New("no protected room code")
	ErrUnsupportedProtection = errors.New("current-user room code protection is unavailable on this platform")
)

type RoomCodeStore struct {
	path    string
	replace func(string, string) error
	mu      sync.Mutex
}

func NewRoomCodeStore(path string) (*RoomCodeStore, error) {
	if path == "" || filepath.Base(path) == "." {
		return nil, errors.New("protected room code path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("resolve protected room code path")
	}
	return &RoomCodeStore{path: absolute, replace: replaceFile}, nil
}

func DefaultRoomCodePath() (string, error) {
	metadataPath, err := DefaultMetadataPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(metadataPath), "room.code"), nil
}

func (s *RoomCodeStore) Path() string { return s.path }

func (s *RoomCodeStore) Save(roomCode []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateRoomCode(roomCode); err != nil {
		return err
	}
	ciphertext, err := protectForCurrentUser(roomCode)
	if err != nil {
		return fmt.Errorf("protect room code for current Windows user: %w", err)
	}
	defer clearBytes(ciphertext)
	if len(ciphertext) == 0 || len(ciphertext) > roomCodeCiphertextLimit {
		return errors.New("protected room code has an invalid size")
	}
	envelope := make([]byte, 0, len(roomCodeEnvelopeMagic)+1+len(ciphertext))
	envelope = append(envelope, roomCodeEnvelopeMagic...)
	envelope = append(envelope, roomCodeEnvelopeVersion)
	envelope = append(envelope, ciphertext...)
	defer clearBytes(envelope)

	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create protected room code directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".room-code-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary protected room code: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		_ = temporary.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict protected room code permissions: %w", err)
	}
	if _, err := temporary.Write(envelope); err != nil {
		return fmt.Errorf("write protected room code: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("flush protected room code: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close protected room code: %w", err)
	}
	if err := s.replace(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace protected room code: %w", err)
	}
	keepTemporary = false
	return nil
}

func (s *RoomCodeStore) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoProtectedRoomCode
	}
	if err != nil {
		return nil, fmt.Errorf("read protected room code: %w", err)
	}
	defer file.Close()
	headerSize := len(roomCodeEnvelopeMagic) + 1
	envelope, err := io.ReadAll(io.LimitReader(file, int64(roomCodeCiphertextLimit+headerSize+1)))
	if err != nil {
		return nil, fmt.Errorf("read protected room code: %w", err)
	}
	defer clearBytes(envelope)
	if len(envelope) <= headerSize || len(envelope) > roomCodeCiphertextLimit+headerSize {
		return nil, errors.New("protected room code envelope has an invalid size")
	}
	if !bytes.Equal(envelope[:len(roomCodeEnvelopeMagic)], roomCodeEnvelopeMagic) || envelope[len(roomCodeEnvelopeMagic)] != roomCodeEnvelopeVersion {
		return nil, errors.New("protected room code envelope is unsupported")
	}
	cleartext, err := unprotectForCurrentUser(envelope[headerSize:])
	if err != nil {
		return nil, fmt.Errorf("unprotect room code for current Windows user: %w", err)
	}
	if err := validateRoomCode(cleartext); err != nil {
		clearBytes(cleartext)
		return nil, errors.New("protected room code content is invalid")
	}
	return cleartext, nil
}

func (s *RoomCodeStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear protected room code: %w", err)
	}
	return nil
}

func validateRoomCode(roomCode []byte) error {
	if len(roomCode) == 0 || len(roomCode) > roomCodeMaximumSize {
		return errors.New("room code has an invalid size")
	}
	for _, character := range roomCode {
		if character < 0x21 || character > 0x7e {
			return errors.New("room code contains invalid characters")
		}
	}
	return nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
