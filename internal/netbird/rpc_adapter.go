package netbird

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	daemonpb "sogame/internal/netbird/rpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	LocalDaemonAddress = "127.0.0.1:41731"
	localDialTimeout   = 3 * time.Second
)

type daemonRPCClient interface {
	daemonProfileClient
	Login(context.Context, *daemonpb.LoginRequest, ...grpc.CallOption) (*daemonpb.LoginResponse, error)
	Up(context.Context, *daemonpb.UpRequest, ...grpc.CallOption) (*daemonpb.UpResponse, error)
	Status(context.Context, *daemonpb.StatusRequest, ...grpc.CallOption) (*daemonpb.StatusResponse, error)
	Down(context.Context, *daemonpb.DownRequest, ...grpc.CallOption) (*daemonpb.DownResponse, error)
	Logout(context.Context, *daemonpb.LogoutRequest, ...grpc.CallOption) (*daemonpb.LogoutResponse, error)
	SubscribeEvents(context.Context, *daemonpb.SubscribeRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.SystemEvent], error)
}

type RPCError struct {
	Operation string
	Code      codes.Code
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("NetBird daemon RPC %s failed (%s)", e.Operation, e.Code)
}

type RPCAdapter struct {
	client   daemonRPCClient
	profiles *ManagedProfileStore
	username string
	closer   io.Closer
}

func DialLocalRPCAdapter(ctx context.Context, username string) (*RPCAdapter, error) {
	dialCtx, cancel := context.WithTimeout(ctx, localDialTimeout)
	defer cancel()

	connection, err := grpc.DialContext(
		dialCtx,
		LocalDaemonAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to local NetBird daemon: %w", err)
	}
	return newRPCAdapter(daemonpb.NewDaemonServiceClient(connection), username, connection), nil
}

func newRPCAdapter(client daemonRPCClient, username string, closer io.Closer) *RPCAdapter {
	return &RPCAdapter{
		client:   client,
		profiles: NewManagedProfileStore(client, username),
		username: username,
		closer:   closer,
	}
}

func (a *RPCAdapter) Close() error {
	if a.closer == nil {
		return nil
	}
	return a.closer.Close()
}

func (a *RPCAdapter) DaemonVersion(ctx context.Context) (string, error) {
	response, err := a.client.Status(ctx, &daemonpb.StatusRequest{})
	if err != nil {
		return "", sanitizeRPCError("read version", err)
	}
	return response.GetDaemonVersion(), nil
}

func (a *RPCAdapter) Status(ctx context.Context) (Snapshot, error) {
	response, err := a.client.Status(ctx, &daemonpb.StatusRequest{GetFullPeerStatus: true})
	if err != nil {
		return Snapshot{}, sanitizeRPCError("read status", err)
	}
	return NormalizeStatus(response), nil
}

func (a *RPCAdapter) ListProfiles(ctx context.Context) ([]Profile, error) {
	return a.profiles.list(ctx)
}

func (a *RPCAdapter) ActiveProfile(ctx context.Context) (Profile, error) {
	response, err := a.client.GetActiveProfile(ctx, &daemonpb.GetActiveProfileRequest{})
	if err != nil {
		return Profile{}, sanitizeRPCError("read active profile", err)
	}
	return Profile{
		ID:       response.GetId(),
		Name:     response.GetProfileName(),
		IsActive: true,
		Username: response.GetUsername(),
	}, nil
}

func (a *RPCAdapter) CreateProfile(ctx context.Context, displayName string) (Profile, error) {
	if displayName != ManagedProfileName {
		return Profile{}, fmt.Errorf("only the %q managed profile can be created", ManagedProfileName)
	}
	profile, err := a.profiles.Ensure(ctx, "")
	if err == nil {
		return profile, nil
	}
	if !errors.Is(err, ErrManagedProfileConflict) {
		return Profile{}, err
	}
	// 清理由失败事务遗留的孤儿 managed profile(本地无房间记录,它不受任何事务管理),然后重建。
	if err := a.removeManagedProfileByName(ctx); err != nil {
		return Profile{}, fmt.Errorf("%w: orphan cleanup failed: %v", ErrManagedProfileConflict, err)
	}
	return a.profiles.Ensure(ctx, "")
}

func (a *RPCAdapter) removeManagedProfileByName(ctx context.Context) error {
	profiles, err := a.profiles.list(ctx)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.Name == ManagedProfileName {
			return a.profiles.Remove(ctx, profile.ID)
		}
	}
	return nil
}

func (a *RPCAdapter) SelectProfile(ctx context.Context, profileID string) error {
	return a.profiles.Select(ctx, profileID)
}

func (a *RPCAdapter) RemoveProfile(ctx context.Context, profileID string) error {
	return a.profiles.Remove(ctx, profileID)
}

func (a *RPCAdapter) Enroll(ctx context.Context, request EnrollmentRequest) error {
	if request.SetupKey == nil {
		return errors.New("Setup Key is required for NetBird enrollment")
	}
	defer request.SetupKey.Clear()
	if len(request.SetupKey.value) == 0 {
		return errors.New("Setup Key is empty")
	}
	if _, err := a.profiles.Validate(ctx, request.ProfileID); err != nil {
		return err
	}

	loginRequest := &daemonpb.LoginRequest{
		SetupKey:      string(request.SetupKey.value),
		ManagementUrl: request.ManagementURL,
		Hostname:      request.Hostname,
		ProfileName:   stringPointer(request.ProfileID),
		Username:      stringPointer(a.username),
	}
	_, err := a.client.Login(ctx, loginRequest)
	loginRequest.SetupKey = ""
	if err != nil {
		return sanitizeRPCError("enroll", err)
	}
	return nil
}

func (a *RPCAdapter) Connect(ctx context.Context, profileID string) error {
	if _, err := a.profiles.Validate(ctx, profileID); err != nil {
		return err
	}
	_, err := a.client.Up(ctx, &daemonpb.UpRequest{
		ProfileName: stringPointer(profileID),
		Username:    stringPointer(a.username),
	})
	return sanitizeRPCError("connect", err)
}

func (a *RPCAdapter) Disconnect(ctx context.Context, profileID string) error {
	if err := a.profiles.Select(ctx, profileID); err != nil {
		return err
	}
	_, err := a.client.Down(ctx, &daemonpb.DownRequest{})
	return sanitizeRPCError("disconnect", err)
}

func (a *RPCAdapter) Deregister(ctx context.Context, profileID string) error {
	if _, err := a.profiles.Validate(ctx, profileID); err != nil {
		return err
	}
	_, err := a.client.Logout(ctx, &daemonpb.LogoutRequest{
		ProfileName: stringPointer(profileID),
		Username:    stringPointer(a.username),
	})
	return sanitizeRPCError("deregister", err)
}

func (a *RPCAdapter) Subscribe(ctx context.Context) (<-chan Event, <-chan error) {
	events := make(chan Event)
	failures := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(failures)

		stream, err := a.client.SubscribeEvents(ctx, &daemonpb.SubscribeRequest{})
		if err != nil {
			sendSubscriptionError(ctx, failures, sanitizeRPCError("subscribe events", err))
			return
		}
		for {
			source, err := stream.Recv()
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			if err != nil {
				sendSubscriptionError(ctx, failures, sanitizeRPCError("receive event", err))
				return
			}
			select {
			case events <- NormalizeEvent(source):
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, failures
}

func sanitizeRPCError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &RPCError{Operation: operation, Code: status.Code(err)}
}

func sendSubscriptionError(ctx context.Context, target chan<- error, err error) {
	select {
	case target <- err:
	case <-ctx.Done():
	}
}

var _ Adapter = (*RPCAdapter)(nil)
