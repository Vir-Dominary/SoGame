package securestore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validMetadata(profileID string) RoomMetadata {
	return RoomMetadata{
		Version:       CurrentMetadataVersion,
		RoomID:        "room-1",
		ManagementURL: "https://legengen.top",
		ProfileID:     profileID,
		CreatedAt:     time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}
}

func TestMetadataStoreSavesLoadsAndReplacesSingleRoom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "room.json")
	store, err := NewMetadataStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(validMetadata("profile-1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(validMetadata("profile-2")); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProfileID != "profile-2" || loaded.Version != CurrentMetadataVersion {
		t.Fatalf("metadata=%+v", loaded)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".room-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files=%v error=%v", matches, err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"roomCode", "setupKey", "setup_key"} {
		if strings.Contains(string(contents), forbidden) {
			t.Fatalf("metadata contains forbidden field %q: %s", forbidden, contents)
		}
	}
}

func TestMetadataStorePreservesPreviousFileWhenAtomicReplaceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "room.json")
	store, err := NewMetadataStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(validMetadata("profile-1")); err != nil {
		t.Fatal(err)
	}
	store.replace = func(string, string) error { return errors.New("replace failed") }
	if err := store.Save(validMetadata("profile-2")); err == nil {
		t.Fatal("replace failure was ignored")
	}
	loaded, err := loadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ProfileID != "profile-1" {
		t.Fatalf("previous metadata was corrupted: %+v", loaded)
	}
}

func TestMetadataStoreRejectsInvalidVersionShapeAndSize(t *testing.T) {
	directory := t.TempDir()
	for name, payload := range map[string]string{
		"version":   `{"version":2,"roomId":"room-1","managementUrl":"https://legengen.top","profileId":"profile-1","createdAt":"2026-07-19T12:00:00Z"}`,
		"unknown":   `{"version":1,"roomId":"room-1","managementUrl":"https://legengen.top","profileId":"profile-1","createdAt":"2026-07-19T12:00:00Z","roomCode":"AAAA-BBBB-CCCC"}`,
		"malformed": `{`,
		"oversized": strings.Repeat("x", metadataFileLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name+".json")
			if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := NewMetadataStore(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.Load()
			if err == nil {
				t.Fatal("invalid metadata was accepted")
			}
			if name == "version" {
				var versionError *MetadataVersionError
				if !errors.As(err, &versionError) {
					t.Fatalf("error=%v", err)
				}
			}
		})
	}
}

func TestMetadataStoreClearIsIdempotent(t *testing.T) {
	store, err := NewMetadataStore(filepath.Join(t.TempDir(), "room.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNoRoomMetadata) {
		t.Fatalf("missing metadata error=%v", err)
	}
	if err := store.Save(validMetadata("profile-1")); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNoRoomMetadata) {
		t.Fatalf("cleared metadata error=%v", err)
	}
}
