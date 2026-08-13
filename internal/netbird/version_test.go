package netbird

import (
	"context"
	"errors"
	"testing"
)

type fakeAdapter struct {
	version      string
	versionError error
	connectCalls int
}

func (f *fakeAdapter) DaemonVersion(context.Context) (string, error) {
	return f.version, f.versionError
}
func (f *fakeAdapter) Status(context.Context) (Snapshot, error) {
	return Snapshot{DaemonVersion: f.version}, f.versionError
}
func (f *fakeAdapter) ListProfiles(context.Context) ([]Profile, error)        { return nil, nil }
func (f *fakeAdapter) ActiveProfile(context.Context) (Profile, error)         { return Profile{}, nil }
func (f *fakeAdapter) CreateProfile(context.Context, string) (Profile, error) { return Profile{}, nil }
func (f *fakeAdapter) SelectProfile(context.Context, string) error            { return nil }
func (f *fakeAdapter) RemoveProfile(context.Context, string) error            { return nil }
func (f *fakeAdapter) Enroll(context.Context, EnrollmentRequest) error        { return nil }
func (f *fakeAdapter) Connect(context.Context, string) error {
	f.connectCalls++
	return nil
}
func (f *fakeAdapter) Disconnect(context.Context, string) error { return nil }
func (f *fakeAdapter) Deregister(context.Context, string) error { return nil }
func (f *fakeAdapter) Subscribe(context.Context) (<-chan Event, <-chan error) {
	return make(chan Event), make(chan error)
}

func TestExactVersionAllowsOperation(t *testing.T) {
	inner := &fakeAdapter{version: ExpectedVersion}
	if err := EnforceExactVersion(inner, ExpectedVersion).Connect(context.Background(), "profile-id"); err != nil {
		t.Fatal(err)
	}
	if inner.connectCalls != 1 {
		t.Fatalf("connect calls=%d", inner.connectCalls)
	}
}

func TestVersionMismatchBlocksOperationAndProvidesRepair(t *testing.T) {
	for _, version := range []string{"0.74.6", "0.74.7-dev", ""} {
		t.Run(version, func(t *testing.T) {
			inner := &fakeAdapter{version: version}
			err := EnforceExactVersion(inner, ExpectedVersion).Connect(context.Background(), "profile-id")
			var mismatch *VersionMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("error=%v", err)
			}
			if inner.connectCalls != 0 {
				t.Fatal("incompatible adapter performed the operation")
			}
			repair := RepairResultFor(err)
			if !repair.Required || repair.Reason != RepairVersionMismatch || repair.DetectedVersion != version {
				t.Fatalf("repair=%+v", repair)
			}
		})
	}
}

func TestVersionVariantFormatsAreAccepted(t *testing.T) {
	for _, version := range []string{"v0.74.7", "V0.74.7", "0.74.7.0", "0.74.7+build5"} {
		t.Run(version, func(t *testing.T) {
			inner := &fakeAdapter{version: version}
			if err := EnforceExactVersion(inner, ExpectedVersion).Connect(context.Background(), "profile-id"); err != nil {
				t.Fatalf("normalized version %q rejected: %v", version, err)
			}
			if inner.connectCalls != 1 {
				t.Fatalf("connect calls=%d", inner.connectCalls)
			}
		})
	}
}

func TestNormalizeVersionPreservesPrereleaseDistinction(t *testing.T) {
	if NormalizeVersion("0.74.7") == NormalizeVersion("0.74.7-dev") {
		t.Fatal("prerelease versions must not normalize to the same value")
	}
	if NormalizeVersion("0.74.7") != NormalizeVersion("v0.74.7") {
		t.Fatal("v-prefixed version must match")
	}
}

func TestStatusReturnsDetectedVersionWithMismatch(t *testing.T) {
	adapter := EnforceExactVersion(&fakeAdapter{version: "0.74.6"}, ExpectedVersion)
	snapshot, err := adapter.Status(context.Background())
	if snapshot.DaemonVersion != "0.74.6" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	var mismatch *VersionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error=%v", err)
	}
}

func TestUnavailableVersionProducesServiceRepairResult(t *testing.T) {
	inner := &fakeAdapter{versionError: errors.New("dial refused")}
	err := EnforceExactVersion(inner, ExpectedVersion).Connect(context.Background(), "profile-id")
	repair := RepairResultFor(err)
	if !repair.Required || repair.Reason != RepairServiceUnavailable {
		t.Fatalf("repair=%+v", repair)
	}
}
