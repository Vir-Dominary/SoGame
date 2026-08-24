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

package netbird

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const ExpectedVersion = "0.74.7"

// NormalizeVersion 将守护进程报告的版本归一化为可比较形式:
// 去除 v 前缀、截断构建段,但保留 -dev 之类的 pre-release 后缀。
// 例如 "v0.74.7" 与 "0.74.7.0" 均视为与 "0.74.7" 相同;
// "0.74.7-dev" 仍与 "0.74.7" 不同。
func NormalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "v")
	value = strings.TrimPrefix(value, "V")
	core, preRelease, hasPrerelease := strings.Cut(value, "-")
	if !hasPrerelease {
		core, _, _ = strings.Cut(core, "+")
	}
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	result := strings.Join(parts, ".")
	if hasPrerelease {
		result = result + "-" + preRelease
	}
	return result
}

func versionsEqual(actual, expected string) bool {
	return NormalizeVersion(actual) == NormalizeVersion(expected)
}

type RepairReason string

const (
	RepairVersionMismatch    RepairReason = "version_mismatch"
	RepairServiceUnavailable RepairReason = "service_unavailable"
)

type RepairResult struct {
	Required        bool
	Reason          RepairReason
	ExpectedVersion string
	DetectedVersion string
}

type VersionMismatchError struct {
	Expected string
	Detected string
}

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("NetBird daemon version mismatch: expected %s, detected %s", e.Expected, e.Detected)
}

func RepairResultFor(err error) RepairResult {
	var mismatch *VersionMismatchError
	if errors.As(err, &mismatch) {
		return RepairResult{
			Required:        true,
			Reason:          RepairVersionMismatch,
			ExpectedVersion: mismatch.Expected,
			DetectedVersion: mismatch.Detected,
		}
	}
	if err != nil {
		return RepairResult{Required: true, Reason: RepairServiceUnavailable, ExpectedVersion: ExpectedVersion}
	}
	return RepairResult{}
}

type VersionedAdapter struct {
	inner    Adapter
	expected string
}

func EnforceExactVersion(inner Adapter, expected string) *VersionedAdapter {
	return &VersionedAdapter{inner: inner, expected: expected}
}

func (a *VersionedAdapter) requireCompatible(ctx context.Context) error {
	version, err := a.inner.DaemonVersion(ctx)
	if err != nil {
		return err
	}
	if !versionsEqual(version, a.expected) {
		return &VersionMismatchError{Expected: a.expected, Detected: version}
	}
	return nil
}

func (a *VersionedAdapter) DaemonVersion(ctx context.Context) (string, error) {
	return a.inner.DaemonVersion(ctx)
}

func (a *VersionedAdapter) Status(ctx context.Context) (Snapshot, error) {
	snapshot, err := a.inner.Status(ctx)
	if err != nil {
		return snapshot, err
	}
	if !versionsEqual(snapshot.DaemonVersion, a.expected) {
		return snapshot, &VersionMismatchError{Expected: a.expected, Detected: snapshot.DaemonVersion}
	}
	return snapshot, nil
}

func (a *VersionedAdapter) ListProfiles(ctx context.Context) ([]Profile, error) {
	if err := a.requireCompatible(ctx); err != nil {
		return nil, err
	}
	return a.inner.ListProfiles(ctx)
}

func (a *VersionedAdapter) ActiveProfile(ctx context.Context) (Profile, error) {
	if err := a.requireCompatible(ctx); err != nil {
		return Profile{}, err
	}
	return a.inner.ActiveProfile(ctx)
}

func (a *VersionedAdapter) CreateProfile(ctx context.Context, displayName string) (Profile, error) {
	if err := a.requireCompatible(ctx); err != nil {
		return Profile{}, err
	}
	return a.inner.CreateProfile(ctx, displayName)
}

func (a *VersionedAdapter) SelectProfile(ctx context.Context, profileID string) error {
	if err := a.requireCompatible(ctx); err != nil {
		return err
	}
	return a.inner.SelectProfile(ctx, profileID)
}

func (a *VersionedAdapter) RemoveProfile(ctx context.Context, profileID string) error {
	if err := a.requireCompatible(ctx); err != nil {
		return err
	}
	return a.inner.RemoveProfile(ctx, profileID)
}

func (a *VersionedAdapter) Enroll(ctx context.Context, request EnrollmentRequest) error {
	if err := a.requireCompatible(ctx); err != nil {
		return err
	}
	return a.inner.Enroll(ctx, request)
}

func (a *VersionedAdapter) Connect(ctx context.Context, profileID string) error {
	if err := a.requireCompatible(ctx); err != nil {
		return err
	}
	return a.inner.Connect(ctx, profileID)
}

func (a *VersionedAdapter) Disconnect(ctx context.Context, profileID string) error {
	if err := a.requireCompatible(ctx); err != nil {
		return err
	}
	return a.inner.Disconnect(ctx, profileID)
}

func (a *VersionedAdapter) Deregister(ctx context.Context, profileID string) error {
	if err := a.requireCompatible(ctx); err != nil {
		return err
	}
	return a.inner.Deregister(ctx, profileID)
}

func (a *VersionedAdapter) Subscribe(ctx context.Context) (<-chan Event, <-chan error) {
	if err := a.requireCompatible(ctx); err != nil {
		events := make(chan Event)
		errors := make(chan error, 1)
		errors <- err
		close(events)
		close(errors)
		return events, errors
	}
	return a.inner.Subscribe(ctx)
}

var _ Adapter = (*VersionedAdapter)(nil)
