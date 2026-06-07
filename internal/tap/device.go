package tap

import (
	"context"
	"time"

	"netjoin/internal/nic"
)

const (
	devicePollInterval = 200 * time.Millisecond
	deviceWaitTimeout  = 10 * time.Second
)

var (
	setDeviceStatusByNetCfgID = nic.SetDeviceStatusByNetCfgID
	waitAdminStatusByNetCfgID = nic.WaitAdminStatusByNetCfgID
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
	netCfgID, err := netCfgIDFromLuid(info.Luid)
	if err != nil {
		return err
	}
	if err := setDeviceStatusByNetCfgID(netCfgID, true); err != nil {
		return err
	}
	return waitAdminStatusByNetCfgID(context.Background(), netCfgID, nic.AdminUp, devicePollInterval, deviceWaitTimeout)
}

func RestartAdapterByName(name string) error {
	info, err := findByFriendlyName(name)
	if err != nil {
		return err
	}
	return RestartAdapter(*info)
}

func RestartAdapter(info nic.Info) error {
	netCfgID, err := netCfgIDFromLuid(info.Luid)
	if err != nil {
		return err
	}
	if err := setDeviceStatusByNetCfgID(netCfgID, false); err != nil {
		return err
	}
	if err := waitAdminStatusByNetCfgID(context.Background(), netCfgID, nic.AdminDown, devicePollInterval, deviceWaitTimeout); err != nil {
		return err
	}
	if err := setDeviceStatusByNetCfgID(netCfgID, true); err != nil {
		return err
	}
	return waitAdminStatusByNetCfgID(context.Background(), netCfgID, nic.AdminUp, devicePollInterval, deviceWaitTimeout)
}
