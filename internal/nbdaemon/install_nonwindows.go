//go:build !windows

package nbdaemon

import "context"

type WindowsMSIRunner struct{}

func NewWindowsMSIRunner() WindowsMSIRunner { return WindowsMSIRunner{} }

func (WindowsMSIRunner) Run(context.Context, MSIAction, string, string) error {
	return ErrServiceUnavailable
}

func (WindowsMSIRunner) Remove(context.Context, string, string) error {
	return ErrServiceUnavailable
}
