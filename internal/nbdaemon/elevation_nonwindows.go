//go:build !windows

package nbdaemon

func RequestInstallerElevation(string, MSIAction, string, string, string) error {
	return ErrServiceUnavailable
}

func RequestDaemonRemovalElevation(string, bool, string, string) error {
	return ErrServiceUnavailable
}