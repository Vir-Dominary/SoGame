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
