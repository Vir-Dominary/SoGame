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

import "errors"

var (
	ErrArtifactMissing     = errors.New("NetBird artifact is missing")
	ErrArtifactSize        = errors.New("NetBird artifact size mismatch")
	ErrArtifactDigest      = errors.New("NetBird artifact digest mismatch")
	ErrSignatureInvalid    = errors.New("NetBird artifact signature is invalid")
	ErrPublisherMismatch   = errors.New("NetBird artifact publisher mismatch")
	ErrServiceMissing      = errors.New("NetBird service is missing")
	ErrServiceAccess       = errors.New("NetBird service status access denied")
	ErrServiceUnavailable  = errors.New("NetBird service is unavailable")
	ErrElevationRequired   = errors.New("administrator elevation is required")
	ErrElevationCancelled  = errors.New("administrator elevation was cancelled by the user")
	ErrElevationTimedOut   = errors.New("elevated helper did not complete in time")
	ErrUnsupportedAction   = errors.New("unsupported privileged action")
	ErrRemovalNotConfirmed = errors.New("NetBird service removal was not confirmed")
)
