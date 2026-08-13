package netbird

import (
	"context"
	"errors"
	"testing"

	daemonpb "sogame/internal/netbird/rpc"
	"google.golang.org/grpc"
)

type fakeProfileClient struct {
	profiles []*daemonpb.Profile
	activeID string
	addedID  string
	selected []string
	removed  []string
	removeErr error
}

func (f *fakeProfileClient) ListProfiles(context.Context, *daemonpb.ListProfilesRequest, ...grpc.CallOption) (*daemonpb.ListProfilesResponse, error) {
	return &daemonpb.ListProfilesResponse{Profiles: f.profiles}, nil
}
func (f *fakeProfileClient) GetActiveProfile(context.Context, *daemonpb.GetActiveProfileRequest, ...grpc.CallOption) (*daemonpb.GetActiveProfileResponse, error) {
	return &daemonpb.GetActiveProfileResponse{Id: f.activeID}, nil
}
func (f *fakeProfileClient) AddProfile(_ context.Context, request *daemonpb.AddProfileRequest, _ ...grpc.CallOption) (*daemonpb.AddProfileResponse, error) {
	if request.ProfileName != ManagedProfileName {
		return nil, errors.New("unexpected profile name")
	}
	f.profiles = append(f.profiles, &daemonpb.Profile{Id: f.addedID, Name: ManagedProfileName})
	return &daemonpb.AddProfileResponse{Id: f.addedID}, nil
}
func (f *fakeProfileClient) SwitchProfile(_ context.Context, request *daemonpb.SwitchProfileRequest, _ ...grpc.CallOption) (*daemonpb.SwitchProfileResponse, error) {
	id := request.GetProfileName()
	f.selected = append(f.selected, id)
	return &daemonpb.SwitchProfileResponse{Id: id}, nil
}
func (f *fakeProfileClient) RemoveProfile(_ context.Context, request *daemonpb.RemoveProfileRequest, _ ...grpc.CallOption) (*daemonpb.RemoveProfileResponse, error) {
	if f.removeErr != nil {
		return nil, f.removeErr
	}
	f.removed = append(f.removed, request.ProfileName)
	for index, profile := range f.profiles {
		if profile.Id == request.ProfileName {
			f.profiles = append(f.profiles[:index], f.profiles[index+1:]...)
			break
		}
	}
	return &daemonpb.RemoveProfileResponse{Id: request.ProfileName}, nil
}

func TestManagedProfileCreatesAndReturnsConcreteID(t *testing.T) {
	client := &fakeProfileClient{profiles: []*daemonpb.Profile{{Id: "other-id", Name: "personal"}}, addedID: "managed-id"}
	profile, err := NewManagedProfileStore(client, `DESKTOP\user`).Ensure(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != "managed-id" || profile.Name != ManagedProfileName {
		t.Fatalf("profile=%+v", profile)
	}
}

func TestManagedProfileNeverAdoptsUnownedSameName(t *testing.T) {
	client := &fakeProfileClient{profiles: []*daemonpb.Profile{{Id: "unknown-id", Name: ManagedProfileName}}}
	_, err := NewManagedProfileStore(client, "user").Ensure(context.Background(), "")
	if !errors.Is(err, ErrManagedProfileConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestManagedProfileValidatesExactIDAfterDaemonRenamesDisplayName(t *testing.T) {
	client := &fakeProfileClient{profiles: []*daemonpb.Profile{{Id: "managed-id", Name: "renamed"}}}
	profile, err := NewManagedProfileStore(client, "user").Validate(context.Background(), "managed-id")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID != "managed-id" || profile.Name != "renamed" {
		t.Fatalf("profile=%+v", profile)
	}
}

func TestManagedProfileRejectsUnknownConcreteID(t *testing.T) {
	client := &fakeProfileClient{profiles: []*daemonpb.Profile{{Id: "other-id", Name: ManagedProfileName}}}
	_, err := NewManagedProfileStore(client, "user").Validate(context.Background(), "managed-id")
	if !errors.Is(err, ErrManagedProfileInconsistent) {
		t.Fatalf("error=%v", err)
	}
}

func TestRemoveActiveManagedProfileSelectsDefaultOnly(t *testing.T) {
	client := &fakeProfileClient{
		profiles: []*daemonpb.Profile{
			{Id: "managed-id", Name: ManagedProfileName, IsActive: true},
			{Id: "personal-id", Name: "personal"},
		},
		activeID: "managed-id",
	}
	if err := NewManagedProfileStore(client, "user").Remove(context.Background(), "managed-id"); err != nil {
		t.Fatal(err)
	}
	if len(client.selected) != 1 || client.selected[0] != "default" {
		t.Fatalf("selected=%v", client.selected)
	}
	if len(client.removed) != 1 || client.removed[0] != "managed-id" {
		t.Fatalf("removed=%v", client.removed)
	}
}

func TestRemoveInactiveManagedProfileLeavesUnrelatedActive(t *testing.T) {
	client := &fakeProfileClient{
		profiles: []*daemonpb.Profile{
			{Id: "managed-id", Name: ManagedProfileName},
			{Id: "personal-id", Name: "personal", IsActive: true},
		},
		activeID: "personal-id",
	}
	if err := NewManagedProfileStore(client, "user").Remove(context.Background(), "managed-id"); err != nil {
		t.Fatal(err)
	}
	if len(client.selected) != 0 {
		t.Fatalf("unrelated active profile was changed: %v", client.selected)
	}
}
