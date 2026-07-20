package tap

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"sogame/internal/logger"
	"sogame/internal/nic"
)

// IsLikeDescription reports whether an interface description looks like a TAP/TUN adapter.
func IsLikeDescription(description string) bool {
	desc := strings.ToLower(description)
	return strings.Contains(desc, "tap-windows") ||
		strings.Contains(desc, "tap0901") ||
		strings.Contains(desc, "tap") ||
		strings.Contains(desc, "wintun") ||
		strings.Contains(desc, "tun")
}

// IsWindowsDescription reports whether an interface description is a TAP-Windows adapter.
func IsWindowsDescription(description string) bool {
	desc := strings.ToLower(description)
	return strings.Contains(desc, "tap-windows") ||
		strings.Contains(desc, "tap0901")
}

// FindAdapter returns the named adapter first, then any TAP-Windows adapter.
// Note: fallback uses IsWindowsDescription (not IsLikeDescription) to avoid
// matching WinTUN or other non-TAP-Windows adapters that n2n edge cannot use.
// 过滤 miniport（IsFilterInterface）被自动排除，避免选中 cFosSpeed 等过滤驱动
// 生成的虚拟接口。
func FindAdapter(name string) (*nic.Info, error) {
	list, err := nic.List()
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(name)
	if target != "" {
		for i := range list {
			if list[i].IsFilterInterface {
				continue
			}
			if strings.EqualFold(list[i].FriendlyName, target) {
				return &list[i], nil
			}
		}
	}
	for i := range list {
		if list[i].IsFilterInterface {
			continue
		}
		if IsWindowsDescription(list[i].Description) {
			return &list[i], nil
		}
	}

	return nil, fmt.Errorf("%w: TAP adapter", nic.ErrNotFound)
}

// HasWindowsAdapter reports whether any TAP-Windows adapter instance exists.
// 过滤 miniport 不计入，因为它们不是可用的 TAP-Windows 实例。
func HasWindowsAdapter() (bool, error) {
	list, err := nic.List()
	if err != nil {
		return false, err
	}
	for i := range list {
		if list[i].IsFilterInterface {
			continue
		}
		if IsWindowsDescription(list[i].Description) {
			return true, nil
		}
	}
	return false, nil
}

// FindRenameCandidate returns a TAP-Windows adapter that is not already named newName.
// 通过 IsFilterInterface 标志从系统层面排除过滤 miniport，无需维护关键词黑名单。
func FindRenameCandidate(newName string) (*nic.Info, error) {
	list, err := nic.List()
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(newName)
	for i := range list {
		if list[i].IsFilterInterface {
			continue
		}
		if target != "" && strings.EqualFold(list[i].FriendlyName, target) {
			continue
		}
		if IsWindowsDescription(list[i].Description) {
			return &list[i], nil
		}
	}

	return nil, fmt.Errorf("%w: renameable TAP adapter", nic.ErrNotFound)
}

// RenameCandidate 遍历所有 TAP-Windows 适配器，逐个尝试重命名为 newName。
// 第一个重命名成功的即为可用适配器。
//
// 这种方式不依赖关键词黑名单即可自动跳过被任何过滤驱动（cFosSpeed、Lightweight Filter
// 或未来出现的新过滤驱动）锁定名称的适配器：HrRenameConnection 对被锁定的适配器会返回
// "Incorrect function"，此时自动跳到下一个候选继续尝试。
//
// 过滤 miniport（IsFilterInterface=true）会被直接跳过，不参与改名尝试。
func RenameCandidate(newName string, timeout time.Duration) (*nic.Info, error) {
	target := strings.TrimSpace(newName)
	if target == "" {
		return nil, fmt.Errorf("adapter name is empty")
	}

	list, err := nic.List()
	if err != nil {
		return nil, err
	}

	var skipped []string
	for i := range list {
		info := &list[i]
		// 跳过已命名为目标名的
		if strings.EqualFold(info.FriendlyName, target) {
			continue
		}
		// 跳过过滤 miniport（系统层面排除）
		if info.IsFilterInterface {
			continue
		}
		// 只认 TAP-Windows 适配器
		if !IsWindowsDescription(info.Description) {
			continue
		}
		// 尝试重命名：失败可能是适配器被过滤驱动锁定，也可能是适配器刚创建尚未完成 PnP 初始化。
		// 对 "Incorrect function" 错误进行重试，等待 PnP 初始化完成后再试。
		if err := renameWithRetry(info.Luid, target, 3, 2*time.Second); err != nil {
			logger.Warnf("重命名 TAP 适配器 %q 失败（可能被过滤驱动锁定），跳过: %v", info.FriendlyName, err)
			skipped = append(skipped, info.FriendlyName)
			continue
		}
		// 验证改名已生效
		if err := waitFriendlyName(target, timeout); err != nil {
			// 验证超时：尝试改回原名，避免留下被改名但未验证的适配器
			_ = nic.RenameConnection(info.Luid, info.FriendlyName)
			logger.Warnf("验证 TAP 适配器 %q 改名超时，已改回原名", info.FriendlyName)
			skipped = append(skipped, info.FriendlyName+"(验证超时)")
			continue
		}
		info.FriendlyName = target
		return info, nil
	}

	if len(skipped) > 0 {
		return nil, fmt.Errorf("%w: renameable TAP adapter (跳过被锁定的适配器: %s)", nic.ErrNotFound, strings.Join(skipped, ", "))
	}
	return nil, fmt.Errorf("%w: renameable TAP adapter", nic.ErrNotFound)
}

// renameWithRetry 对 "Incorrect function" (ERROR_INVALID_FUNCTION) 错误进行重试。
// 该错误在新创建的 TAP 适配器尚未完成 PnP 初始化时会短暂出现，
// 与被过滤驱动永久锁定无法区分，因此通过重试 + 延迟来区分：
//   - 瞬态（PnP 未就绪）：重试后成功
//   - 永久（过滤驱动锁定）：重试仍失败，返回错误由调用方跳过
func renameWithRetry(luid uint64, name string, maxRetries int, delay time.Duration) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = nic.RenameConnection(luid, name)
		if lastErr == nil {
			return nil
		}
		if !isIncorrectFunctionError(lastErr) {
			return lastErr
		}
		if attempt < maxRetries {
			logger.Debugf("重命名返回 Incorrect function (尝试 %d/%d)，等待 PnP 初始化后重试...", attempt+1, maxRetries)
			time.Sleep(delay)
		}
	}
	return lastErr
}

func isIncorrectFunctionError(err error) bool {
	return errors.Is(err, syscall.Errno(1)) ||
		strings.Contains(err.Error(), "Incorrect function")
}

func waitFriendlyName(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if _, err := nic.FindByFriendlyName(name); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(200 * time.Millisecond)
	}
}
