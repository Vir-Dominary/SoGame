// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors
//
// This file is part of SoGame.
//
// SoGame is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// SoGame is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with SoGame. If not, see <https://www.gnu.org/licenses/>.

//go:build windows

package nic

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var netDeviceClassGUID = &windows.GUID{Data1: 0x4d36e972, Data2: 0xe325, Data3: 0x11ce, Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18}}

// SetDeviceStatus 在设备层启用或禁用网卡。
func SetDeviceStatus(luid uint64, enable bool) error {
	netCfgID, err := NetCfgIDFromLuid(luid)
	if err != nil {
		return err
	}
	return SetDeviceStatusByNetCfgID(netCfgID, enable)
}

// SetDeviceStatusByNetCfgID 在设备层按 NetCfgInstanceId 启用或禁用网卡。
func SetDeviceStatusByNetCfgID(netCfgID string, enable bool) error {
	devInfo, devInfoData, err := findDeviceByNetCfgID(netCfgID)
	if err != nil {
		return err
	}
	defer devInfo.Close()

	state := windows.DICS_DISABLE
	if enable {
		state = windows.DICS_ENABLE
	}

	var errs []error
	for _, scope := range []windows.DICS_FLAG{
		windows.DICS_FLAG_GLOBAL,
		windows.DICS_FLAG_CONFIGSPECIFIC,
	} {
		if err := changeDeviceState(devInfo, devInfoData, state, scope); err != nil {
			errs = append(errs, err)
			continue
		}
		return nil
	}
	return fmt.Errorf("change device state: %w", errors.Join(errs...))
}

// SetDeviceStatusByName 按友好名称在设备层启用或禁用网卡。
func SetDeviceStatusByName(name string, enable bool) error {
	info, err := FindByFriendlyName(name)
	if err != nil {
		return err
	}
	return SetDeviceStatus(info.Luid, enable)
}

func changeDeviceState(devInfo windows.DevInfo, devInfoData *windows.DevInfoData, state windows.DICS_STATE, scope windows.DICS_FLAG) error {
	params := windows.PropChangeParams{
		ClassInstallHeader: *windows.MakeClassInstallHeader(windows.DIF_PROPERTYCHANGE),
		StateChange:        state,
		Scope:              scope,
	}
	if err := devInfo.SetClassInstallParams(devInfoData, &params.ClassInstallHeader, uint32(unsafe.Sizeof(params))); err != nil {
		return fmt.Errorf("SetupDiSetClassInstallParams scope=%s: %w", deviceStateScopeText(scope), err)
	}
	if err := devInfo.CallClassInstaller(windows.DIF_PROPERTYCHANGE, devInfoData); err != nil {
		return fmt.Errorf("SetupDiCallClassInstaller scope=%s: %w", deviceStateScopeText(scope), err)
	}
	return nil
}

func deviceStateScopeText(scope windows.DICS_FLAG) string {
	switch scope {
	case windows.DICS_FLAG_GLOBAL:
		return "global"
	case windows.DICS_FLAG_CONFIGSPECIFIC:
		return "config-specific"
	default:
		return fmt.Sprintf("unknown(%d)", scope)
	}
}

func findDeviceByNetCfgID(netCfgID string) (windows.DevInfo, *windows.DevInfoData, error) {
	target := normalizeNetCfgID(netCfgID)
	devInfo, err := windows.SetupDiGetClassDevsEx(netDeviceClassGUID, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return 0, nil, fmt.Errorf("SetupDiGetClassDevsEx: %w", err)
	}

	for i := 0; ; i++ {
		devInfoData, err := devInfo.EnumDeviceInfo(i)
		if err == windows.ERROR_NO_MORE_ITEMS {
			break
		}
		if err != nil {
			devInfo.Close()
			return 0, nil, fmt.Errorf("SetupDiEnumDeviceInfo: %w", err)
		}

		id, err := deviceNetCfgID(devInfo, devInfoData)
		if err != nil {
			continue
		}
		if normalizeNetCfgID(id) == target {
			return devInfo, devInfoData, nil
		}
	}

	devInfo.Close()
	return 0, nil, fmt.Errorf("%w: NetCfgInstanceId=%s", ErrNotFound, target)
}

func deviceNetCfgID(devInfo windows.DevInfo, devInfoData *windows.DevInfoData) (string, error) {
	keyHandle, err := devInfo.OpenDevRegKey(devInfoData, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DRV, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}

	key := registry.Key(keyHandle)
	defer key.Close()

	value, _, err := key.GetStringValue("NetCfgInstanceId")
	if err != nil {
		return "", err
	}
	return value, nil
}
