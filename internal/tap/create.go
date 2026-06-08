package tap

import (
	"fmt"

	"sogame/internal/nic"
)

func ListWindowsAdapters() ([]nic.Info, error) {
	list, err := nic.List()
	if err != nil {
		return nil, err
	}

	var taps []nic.Info
	for _, info := range list {
		if IsWindowsAdapterDescription(info.Description) {
			taps = append(taps, info)
		}
	}
	return taps, nil
}

func FindNewWindowsAdapter(before, after []nic.Info) (*nic.Info, error) {
	seen := make(map[uint64]bool, len(before))
	for _, info := range before {
		seen[info.Luid] = true
	}

	var candidates []nic.Info
	for _, info := range after {
		if seen[info.Luid] || !IsWindowsAdapterDescription(info.Description) {
			continue
		}
		candidates = append(candidates, info)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("未找到新建的 TAP-Windows 适配器")
	}
	if len(candidates) > 1 {
		return nil, fmt.Errorf("新建 TAP-Windows 适配器不唯一: %d", len(candidates))
	}
	return &candidates[0], nil
}

func RenameAdapter(luid uint64, newName string) error {
	return nic.RenameConnection(luid, newName)
}
