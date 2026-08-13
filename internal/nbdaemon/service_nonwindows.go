//go:build !windows

package nbdaemon

import "context"

type unsupportedServiceBackend struct{}

func newServiceBackend(string) ServiceBackend { return unsupportedServiceBackend{} }

func (unsupportedServiceBackend) Lookup(context.Context) (ServiceRecord, error) {
	return ServiceRecord{}, ErrServiceUnavailable
}
