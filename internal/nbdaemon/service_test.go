package nbdaemon

import (
	"context"
	"errors"
	"testing"
)

type fakeServiceBackend struct {
	record ServiceRecord
	err    error
}

func (f fakeServiceBackend) Lookup(context.Context) (ServiceRecord, error) { return f.record, f.err }

type fakeVersionProbe struct {
	version string
	err     error
}

func (f fakeVersionProbe) DaemonVersion(context.Context) (string, error) { return f.version, f.err }

func TestServiceInspectorClassification(t *testing.T) {
	tests := []struct {
		name    string
		backend ServiceBackend
		probe   VersionProbe
		health  ServiceHealth
		wantErr error
	}{
		{name: "ready", backend: fakeServiceBackend{record: ServiceRecord{Installed: true, Running: true}}, probe: fakeVersionProbe{version: "0.74.7"}, health: ServiceReady},
		{name: "missing", backend: fakeServiceBackend{err: ErrServiceMissing}, health: ServiceMissing},
		{name: "stopped", backend: fakeServiceBackend{record: ServiceRecord{Installed: true, Version: "0.74.7"}}, health: ServiceStopped},
		{name: "version mismatch while stopped", backend: fakeServiceBackend{record: ServiceRecord{Installed: true, Version: "0.74.6"}}, health: ServiceVersionMismatch},
		{name: "version mismatch from RPC", backend: fakeServiceBackend{record: ServiceRecord{Installed: true, Running: true}}, probe: fakeVersionProbe{version: "0.74.6"}, health: ServiceVersionMismatch},
		{name: "RPC unavailable", backend: fakeServiceBackend{record: ServiceRecord{Installed: true, Running: true}}, probe: fakeVersionProbe{err: errors.New("dial failed")}, health: ServiceUnhealthy, wantErr: ErrServiceUnavailable},
		{name: "access denied", backend: fakeServiceBackend{err: ErrServiceAccess}, health: ServiceAccessDenied, wantErr: ErrServiceAccess},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inspector := NewServiceInspectorWithBackend("0.74.7", test.probe, test.backend)
			result, err := inspector.Inspect(context.Background())
			if result.Health != test.health {
				t.Fatalf("health = %s, want %s", result.Health, test.health)
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
