package tap

import (
	"context"
	"time"

	"sogame/internal/nic"
)

const (
	devicePollInterval = 200 * time.Millisecond
	deviceWaitTimeout  = 10 * time.Second
)

var (
	setDeviceStatus = nic.SetDeviceStatus
	waitAdminStatus = nic.WaitAdminStatus
)

func EnableAdapterByName(name string) error {
	info, err := findByFriendlyName(name)
	if err != nil {
		return err
	}
	return EnableAdapter(*info)
}

func EnableAdapter(info nic.Info) error {
	if info.AdminStatus == nic.AdminUp {
		return nil
	}
	if err := setDeviceStatus(info.Luid, true); err != nil {
		return err
	}
	return waitAdminStatus(context.Background(), info.Luid, nic.AdminUp, devicePollInterval, deviceWaitTimeout)
}

func RestartAdapterByName(name string) error {
	info, err := findByFriendlyName(name)
	if err != nil {
		return err
	}
	return RestartAdapter(*info)
}

func RestartAdapter(info nic.Info) error {
	if err := setDeviceStatus(info.Luid, false); err != nil {
		return err
	}
	if err := waitAdminStatus(context.Background(), info.Luid, nic.AdminDown, devicePollInterval, deviceWaitTimeout); err != nil {
		return err
	}
	if err := setDeviceStatus(info.Luid, true); err != nil {
		return err
	}
	return waitAdminStatus(context.Background(), info.Luid, nic.AdminUp, devicePollInterval, deviceWaitTimeout)
}
