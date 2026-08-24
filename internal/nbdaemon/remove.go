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

package nbdaemon

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const MSIRemove MSIAction = "remove"

var productCodePattern = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)

type RemovalRunner interface {
	Remove(ctx context.Context, productCode, logPath string) error
}

type DaemonRemover struct {
	runner RemovalRunner
}

func NewDaemonRemover(runner RemovalRunner) *DaemonRemover {
	return &DaemonRemover{runner: runner}
}

func (r *DaemonRemover) Remove(ctx context.Context, confirmed bool, productCode, logPath string) error {
	if !confirmed {
		return ErrRemovalNotConfirmed
	}
	if !productCodePattern.MatchString(productCode) {
		return fmt.Errorf("invalid NetBird MSI product code")
	}
	if !filepath.IsAbs(logPath) || !strings.EqualFold(filepath.Ext(logPath), ".log") {
		return fmt.Errorf("invalid uninstaller log path")
	}
	if err := r.runner.Remove(ctx, productCode, logPath); err != nil {
		return fmt.Errorf("remove official NetBird service: %w", err)
	}
	return nil
}

func BuildMSIRemovalArguments(productCode, logPath string) ([]string, error) {
	if !productCodePattern.MatchString(productCode) {
		return nil, fmt.Errorf("invalid NetBird MSI product code")
	}
	return []string{
		"/x", productCode,
		"/quiet", "/qn", "/norestart",
		"/l*v", logPath,
	}, nil
}
