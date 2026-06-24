package tap

import (
	"fmt"

	"sogame/internal/nic"
)

// ListWindowsAdapters 返回所有真实的 TAP-Windows 适配器（排除过滤 miniport）。
// 通过 nic.Info.IsFilterInterface 标志从系统层面排除 cFosSpeed、Lightweight Filter
// 等过滤驱动生成的 miniport，避免它们被误判为 TAP 实例。
func ListWindowsAdapters() ([]nic.Info, error) {
	list, err := nic.List()
	if err != nil {
		return nil, err
	}

	var taps []nic.Info
	for _, info := range list {
		if info.IsFilterInterface {
			continue
		}
		if IsWindowsDescription(info.Description) {
			taps = append(taps, info)
		}
	}
	return taps, nil
}

// FindNewWindowsAdapter 在 after 快照中找出 before 不存在的新建 TAP-Windows 适配器。
// 由于 ListWindowsAdapters 已在系统层面过滤了过滤 miniport，这里通常只会找到
// 一个新建的 TAP 适配器。若仍出现多个，说明 tapinstall 创建了多个实例（异常情况）。
func FindNewWindowsAdapter(before, after []nic.Info) (*nic.Info, error) {
	seen := make(map[uint64]bool, len(before))
	for _, info := range before {
		seen[info.Luid] = true
	}

	var candidates []nic.Info
	for _, info := range after {
		if seen[info.Luid] || !IsWindowsDescription(info.Description) {
			continue
		}
		candidates = append(candidates, info)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("未找到新建的 TAP-Windows 适配器")
	}
	if len(candidates) > 1 {
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.FriendlyName
		}
		return nil, fmt.Errorf("新建 TAP-Windows 适配器不唯一: %d %v", len(candidates), names)
	}
	return &candidates[0], nil
}

func RenameAdapter(luid uint64, newName string) error {
	return nic.RenameConnection(luid, newName)
}
