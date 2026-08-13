package rooms

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sogame/server/room-api/internal/audit"
	roomcrypto "sogame/server/room-api/internal/crypto"
	"sogame/server/room-api/internal/netbird"
	"sogame/server/room-api/internal/store"
	"strings"
	"time"
)

var ErrInvalidRoom = errors.New("room not found")
var ErrOperationInProgress = errors.New("room operation is already in progress")

type NetBirdAPI interface {
	ListGroups(context.Context) ([]netbird.Group, error)
	CreateGroup(context.Context, string) (netbird.Group, error)
	DeleteGroup(context.Context, string) error
	ListSetupKeys(context.Context) ([]netbird.SetupKey, error)
	CreateSetupKey(context.Context, string, string) (netbird.SetupKeyClear, error)
	RevokeSetupKey(context.Context, string, []string) error
	DeleteSetupKey(context.Context, string) error
	ListPolicies(context.Context) ([]netbird.Policy, error)
	CreateRoomPolicy(context.Context, string, string) (netbird.Policy, error)
	DeletePolicy(context.Context, string) error
	DisablePolicy(context.Context, netbird.Policy) error
	ListPeers(context.Context) ([]netbird.Peer, error)
}

type Config struct {
	ManagementURL string
	EncryptionKey []byte
}

type Service struct {
	store *store.Store
	nb    NetBirdAPI
	cfg   Config
}

type RoomResponse struct {
	RoomID        string `json:"room_id"`
	RoomCode      string `json:"room_code,omitempty"`
	ManagementURL string `json:"management_url"`
	SetupKey      string `json:"setup_key"`
}

type PeerView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IP        string `json:"netbird_ip"`
	Connected bool   `json:"connected"`
	Hostname  string `json:"hostname,omitempty"`
}

type PeerResponse struct {
	RoomID string     `json:"room_id"`
	Peers  []PeerView `json:"peers"`
}

func New(s *store.Store, nb NetBirdAPI, cfg Config) *Service {
	return &Service{store: s, nb: nb, cfg: cfg}
}

func (s *Service) Create(ctx context.Context, idempotencyKey string) (RoomResponse, error) {
	retryOperation := false
	if idempotencyKey != "" {
		op, err := s.store.GetOperation(ctx, idempotencyKey)
		if err == nil {
			if op.Status == "creating" {
				return RoomResponse{}, ErrOperationInProgress
			}
			if op.Status == "error" {
				retryOperation = true
			} else {
				var response RoomResponse
				clear, openErr := roomcrypto.Open(s.cfg.EncryptionKey, op.Response)
				if openErr != nil {
					return RoomResponse{}, fmt.Errorf("open idempotent response: %w", openErr)
				}
				if err := json.Unmarshal(clear, &response); err != nil {
					return RoomResponse{}, fmt.Errorf("decode idempotent response: %w", err)
				}
				return response, nil
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return RoomResponse{}, err
		}
	}

	roomID, err := randomID()
	if err != nil {
		return RoomResponse{}, err
	}
	code, err := randomRoomCode()
	if err != nil {
		return RoomResponse{}, err
	}
	if idempotencyKey != "" {
		if retryOperation {
			if err := s.store.ResetOperation(ctx, idempotencyKey, roomID); err != nil {
				return RoomResponse{}, err
			}
		}
		created, err := s.store.BeginOperation(ctx, idempotencyKey, roomID)
		if err != nil {
			return RoomResponse{}, err
		}
		if !created && !retryOperation {
			return RoomResponse{}, ErrOperationInProgress
		}
	}
	codeCiphertext, err := roomcrypto.Seal(s.cfg.EncryptionKey, []byte(code))
	if err != nil {
		return RoomResponse{}, err
	}
	if err := s.store.CreateRoom(ctx, store.Room{
		ID: roomID, CodeHash: roomcrypto.Hash(code), CodeCiphertext: codeCiphertext,
		Status: "creating", CreatedAt: time.Now().UTC(),
	}); err != nil {
		return RoomResponse{}, err
	}

	resourceName := "room-" + roomID
	group, found, err := s.findGroup(ctx, resourceName)
	createdGroup := false
	if err == nil && !found {
		group, err = s.nb.CreateGroup(ctx, resourceName)
		createdGroup = err == nil
	}
	if err != nil {
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create group: %w", err))
	}
	key, err := s.nb.CreateSetupKey(ctx, resourceName, group.ID)
	if err != nil {
		if createdGroup {
			_ = s.nb.DeleteGroup(ctx, group.ID)
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create setup key: %w", err))
	}
	keyCiphertext, err := roomcrypto.Seal(s.cfg.EncryptionKey, []byte(key.Key))
	if err != nil {
		_ = s.nb.RevokeSetupKey(ctx, key.ID, []string{group.ID})
		if createdGroup {
			_ = s.nb.DeleteGroup(ctx, group.ID)
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("encrypt setup key: %w", err))
	}
	if err := s.store.UpdateExternalIDs(ctx, roomID, group.ID, key.ID, "", keyCiphertext); err != nil {
		_ = s.nb.RevokeSetupKey(ctx, key.ID, []string{group.ID})
		if createdGroup {
			_ = s.nb.DeleteGroup(ctx, group.ID)
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("save resources: %w", err))
	}
	policy, err := s.nb.CreateRoomPolicy(ctx, resourceName+"-internal", group.ID)
	if err != nil {
		_ = s.nb.RevokeSetupKey(ctx, key.ID, []string{group.ID})
		if createdGroup {
			_ = s.nb.DeleteGroup(ctx, group.ID)
		}
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("create policy: %w", err))
	}
	if err := s.store.UpdateExternalIDs(ctx, roomID, group.ID, key.ID, policy.ID, keyCiphertext); err != nil {
		_ = s.nb.DeletePolicy(ctx, policy.ID)
		_ = s.nb.RevokeSetupKey(ctx, key.ID, []string{group.ID})
		_ = s.nb.DeleteGroup(ctx, group.ID)
		return s.fail(ctx, roomID, idempotencyKey, fmt.Errorf("save policy: %w", err))
	}
	if err := s.store.SetStatus(ctx, roomID, "active", ""); err != nil {
		return RoomResponse{}, err
	}
	response := RoomResponse{RoomID: roomID, RoomCode: code, ManagementURL: s.cfg.ManagementURL, SetupKey: key.Key}
	if idempotencyKey != "" {
		clear, _ := json.Marshal(response)
		ciphertext, sealErr := roomcrypto.Seal(s.cfg.EncryptionKey, clear)
		if sealErr != nil {
			return RoomResponse{}, sealErr
		}
		if err := s.store.SaveOperation(ctx, idempotencyKey, ciphertext, "active"); err != nil {
			return RoomResponse{}, err
		}
	}
	audit.Event("room_created", map[string]any{"room_id": roomID, "group_id": group.ID})
	return response, nil
}

func (s *Service) findGroup(ctx context.Context, name string) (netbird.Group, bool, error) {
	groups, err := s.nb.ListGroups(ctx)
	if err != nil {
		return netbird.Group{}, false, err
	}
	for _, group := range groups {
		if group.Name == name {
			return group, true, nil
		}
	}
	return netbird.Group{}, false, nil
}

func (s *Service) Reconcile(ctx context.Context) error {
	operations, err := s.store.ListOperationsByStatus(ctx, "creating")
	if err != nil {
		return err
	}
	for _, operation := range operations {
		room, err := s.store.GetRoom(ctx, operation.RoomID)
		if errors.Is(err, store.ErrNotFound) {
			if err := s.store.SaveOperation(ctx, operation.IdempotencyKey, nil, "error"); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if room.PolicyID != "" {
			_ = s.nb.DeletePolicy(ctx, room.PolicyID)
		}
		if room.SetupKeyID != "" {
			_ = s.nb.RevokeSetupKey(ctx, room.SetupKeyID, []string{room.GroupID})
			_ = s.nb.DeleteSetupKey(ctx, room.SetupKeyID)
		}
		if room.GroupID != "" {
			_ = s.nb.DeleteGroup(ctx, room.GroupID)
		}
		if err := s.store.SetStatus(ctx, room.ID, "error", "provisioning interrupted and reconciled"); err != nil {
			return err
		}
		if err := s.store.SaveOperation(ctx, operation.IdempotencyKey, nil, "error"); err != nil {
			return err
		}
		audit.Event("room_provision_reconciled", map[string]any{"room_id": room.ID})
	}
	return nil
}

func (s *Service) fail(ctx context.Context, roomID, idempotencyKey string, err error) (RoomResponse, error) {
	_ = s.store.SetStatus(ctx, roomID, "error", err.Error())
	if idempotencyKey != "" {
		_ = s.store.SaveOperation(ctx, idempotencyKey, nil, "error")
	}
	audit.Event("room_provision_failed", map[string]any{"room_id": roomID, "error": err.Error()})
	return RoomResponse{}, err
}

func (s *Service) Join(ctx context.Context, code string) (RoomResponse, error) {
	room, err := s.store.GetRoomByCodeHash(ctx, roomcrypto.Hash(strings.TrimSpace(code)))
	if errors.Is(err, store.ErrNotFound) || err != nil || room.Status != "active" {
		return RoomResponse{}, ErrInvalidRoom
	}
	key, err := roomcrypto.Open(s.cfg.EncryptionKey, room.SetupKeyCiphertext)
	if err != nil {
		return RoomResponse{}, err
	}
	audit.Event("room_joined", map[string]any{"room_id": room.ID})
	return RoomResponse{RoomID: room.ID, ManagementURL: s.cfg.ManagementURL, SetupKey: string(key)}, nil
}

func (s *Service) Peers(ctx context.Context, code string) (PeerResponse, error) {
	room, err := s.store.GetRoomByCodeHash(ctx, roomcrypto.Hash(strings.TrimSpace(code)))
	if errors.Is(err, store.ErrNotFound) || err != nil || room.Status != "active" {
		return PeerResponse{}, ErrInvalidRoom
	}
	peers, err := s.nb.ListPeers(ctx)
	if err != nil {
		return PeerResponse{}, err
	}
	response := PeerResponse{RoomID: room.ID, Peers: []PeerView{}}
	for _, peer := range peers {
		inRoom := false
		for _, group := range peer.Groups {
			if group.ID == room.GroupID {
				inRoom = true
				break
			}
		}
		if inRoom {
			response.Peers = append(response.Peers, PeerView{
				ID: peer.ID, Name: peer.Name, IP: peer.IP,
				Connected: peer.Connected, Hostname: peer.Hostname,
			})
		}
	}
	return response, nil
}

func (s *Service) Disable(ctx context.Context, code string) error {
	room, err := s.store.GetRoomByCodeHash(ctx, roomcrypto.Hash(strings.TrimSpace(code)))
	if errors.Is(err, store.ErrNotFound) || err != nil {
		return ErrInvalidRoom
	}
	if room.Status == "disabled" {
		return nil
	}
	if err := s.nb.RevokeSetupKey(ctx, room.SetupKeyID, []string{room.GroupID}); err != nil {
		return err
	}
	if room.PolicyID != "" {
		if err := s.nb.DeletePolicy(ctx, room.PolicyID); err != nil {
			return err
		}
	}
	if err := s.store.Disable(ctx, room.ID); err != nil {
		return err
	}
	audit.Event("room_disabled", map[string]any{"room_id": room.ID})
	return nil
}

func (s *Service) DisableDefaultPolicy(ctx context.Context) error {
	policies, err := s.nb.ListPolicies(ctx)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		if policy.Name == "Default" && policy.Enabled {
			if err := s.nb.DisablePolicy(ctx, policy); err != nil {
				return err
			}
			audit.Event("default_policy_disabled", map[string]any{"policy_id": policy.ID})
			return nil
		}
	}
	return nil
}

func randomID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func randomRoomCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return fmt.Sprintf("%s-%s-%s", buf[:4], buf[4:8], buf[8:]), nil
}

func decrypt(key []byte, ciphertext []byte) ([]byte, error) {
	return roomcrypto.Open(key, ciphertext)
}