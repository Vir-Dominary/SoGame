package netbird

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

type Adapter interface {
	DaemonVersion(ctx context.Context) (string, error)
	Status(ctx context.Context) (Snapshot, error)
	ListProfiles(ctx context.Context) ([]Profile, error)
	ActiveProfile(ctx context.Context) (Profile, error)
	CreateProfile(ctx context.Context, displayName string) (Profile, error)
	SelectProfile(ctx context.Context, profileID string) error
	RemoveProfile(ctx context.Context, profileID string) error
	Enroll(ctx context.Context, request EnrollmentRequest) error
	Connect(ctx context.Context, profileID string) error
	Disconnect(ctx context.Context, profileID string) error
	Deregister(ctx context.Context, profileID string) error
	Subscribe(ctx context.Context) (<-chan Event, <-chan error)
}

type EnrollmentRequest struct {
	ManagementURL string
	ProfileID     string
	Hostname      string
	SetupKey      *SetupKey
}

type SetupKey struct {
	value []byte
}

func NewSetupKey(value []byte) *SetupKey {
	secret := &SetupKey{value: make([]byte, len(value))}
	copy(secret.value, value)
	return secret
}

func (s *SetupKey) String() string { return "[REDACTED]" }

func (s *SetupKey) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[REDACTED]")
}

func (s *SetupKey) LogValue() slog.Value {
	return slog.StringValue("[REDACTED]")
}

func (s *SetupKey) MarshalJSON() ([]byte, error) {
	return nil, errors.New("Setup Key serialization is forbidden")
}

func (s *SetupKey) Clear() {
	if s == nil {
		return
	}
	for i := range s.value {
		s.value[i] = 0
	}
	s.value = nil
}

type DaemonState string

const (
	DaemonIdle           DaemonState = "idle"
	DaemonConnecting     DaemonState = "connecting"
	DaemonConnected      DaemonState = "connected"
	DaemonNeedsLogin     DaemonState = "needs_login"
	DaemonLoginFailed    DaemonState = "login_failed"
	DaemonSessionExpired DaemonState = "session_expired"
	DaemonUnknown        DaemonState = "unknown"
)

type PeerState string

const (
	PeerIdle       PeerState = "idle"
	PeerConnecting PeerState = "connecting"
	PeerConnected  PeerState = "connected"
	PeerUnknown    PeerState = "unknown"
)

type PathType string

const (
	PathNone  PathType = "none"
	PathP2P   PathType = "p2p"
	PathRelay PathType = "relay"
)

type Profile struct {
	ID       string
	Name     string
	IsActive bool
	Username string
}

type Peer struct {
	PublicKey     string
	FQDN          string
	NetBirdIP     string
	NetBirdIPv6   string
	State         PeerState
	Path          PathType
	RelayAddress  string
	LastHandshake time.Time
	BytesReceived int64
	BytesSent     int64
}

type Snapshot struct {
	DaemonVersion       string
	DaemonState         DaemonState
	ManagementURL       string
	ManagementConnected bool
	SignalURL           string
	SignalConnected     bool
	LocalNetBirdIP      string
	LocalNetBirdIPv6    string
	Peers               []Peer
}

type Event struct {
	ID          string
	Severity    string
	Category    string
	UserMessage string
	OccurredAt  time.Time
}
