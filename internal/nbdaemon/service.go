package nbdaemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	clientnetbird "sogame/internal/netbird"
)

type ServiceHealth string

const (
	ServiceMissing         ServiceHealth = "missing"
	ServiceAccessDenied    ServiceHealth = "access_denied"
	ServiceStopped         ServiceHealth = "stopped"
	ServiceUnhealthy       ServiceHealth = "unhealthy"
	ServiceVersionMismatch ServiceHealth = "version_mismatch"
	ServiceReady           ServiceHealth = "ready"
)

type ServiceRecord struct {
	Installed  bool
	Running    bool
	BinaryPath string
	Version    string
	ProcessID  uint32
}

type ServiceInspection struct {
	Health          ServiceHealth
	Installed       bool
	Running         bool
	BinaryPath      string
	Version         string
	ExpectedVersion string
	ProcessID       uint32
}

type ServiceBackend interface {
	Lookup(ctx context.Context) (ServiceRecord, error)
}

type VersionProbe interface {
	DaemonVersion(ctx context.Context) (string, error)
}

type ServiceInspector struct {
	backend         ServiceBackend
	probe           VersionProbe
	expectedVersion string
}

func NewServiceInspector(expectedVersion, productCode string, probe VersionProbe) *ServiceInspector {
	return NewServiceInspectorWithBackend(expectedVersion, probe, newServiceBackend(productCode))
}

func NewServiceInspectorWithBackend(expectedVersion string, probe VersionProbe, backend ServiceBackend) *ServiceInspector {
	return &ServiceInspector{backend: backend, probe: probe, expectedVersion: expectedVersion}
}

func (i *ServiceInspector) Inspect(ctx context.Context) (ServiceInspection, error) {
	result := ServiceInspection{ExpectedVersion: i.expectedVersion}
	record, err := i.backend.Lookup(ctx)
	if err != nil {
		switch {
		case errors.Is(err, ErrServiceMissing):
			result.Health = ServiceMissing
			return result, nil
		case errors.Is(err, ErrServiceAccess):
			result.Health = ServiceAccessDenied
			return result, err
		default:
			result.Health = ServiceUnhealthy
			return result, fmt.Errorf("inspect NetBird service: %w", err)
		}
	}

	result.Installed = record.Installed
	result.Running = record.Running
	result.BinaryPath = record.BinaryPath
	result.Version = record.Version
	result.ProcessID = record.ProcessID

	if !record.Running {
		if record.Version != "" && clientnetbird.NormalizeVersion(record.Version) != clientnetbird.NormalizeVersion(i.expectedVersion) {
			result.Health = ServiceVersionMismatch
		} else {
			result.Health = ServiceStopped
		}
		return result, nil
	}

	if i.probe == nil {
		result.Health = ServiceUnhealthy
		return result, ErrServiceUnavailable
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	version, err := i.probe.DaemonVersion(probeCtx)
	if err != nil {
		result.Health = ServiceUnhealthy
		return result, fmt.Errorf("%w: %v", ErrServiceUnavailable, err)
	}
	result.Version = version
	if clientnetbird.NormalizeVersion(version) != clientnetbird.NormalizeVersion(i.expectedVersion) {
		result.Health = ServiceVersionMismatch
		return result, nil
	}
	result.Health = ServiceReady
	return result, nil
}
