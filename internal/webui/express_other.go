//go:build !windows

package app

import (
	"errors"

	"sogame/internal/session"
)

// NewWindowsExpressController 在非 Windows 平台返回一个携带错误状态的控制器。
// 极速模式依赖 Windows 上的 NetBird 守护进程，在其他平台上不可用。
func NewWindowsExpressController(roomAPIBaseURL string) *ExpressController {
	controller := NewExpressController()
	controller.mu.Lock()
	controller.state.State = string(session.StateRecoverableError)
	controller.state.Error = &ExpressError{
		Code:      expressErrServiceUnavailable,
		Message:   "极速模式仅在 Windows 上可用",
		Retryable: false,
		Action:    "请在 Windows 上使用极速模式",
	}
	controller.mu.Unlock()
	return controller
}

// errExpressUnsupported 在非 Windows 平台上调用极速模式命令时返回。
var errExpressUnsupported = errors.New("极速模式仅在 Windows 上可用")
