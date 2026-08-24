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

	daemonpb "sogame/internal/netbird/rpc"
	"google.golang.org/grpc"
)

const ManagedProfileName = "sogame-room"

var (
	ErrManagedProfileConflict     = errors.New("managed NetBird profile name already exists without ownership metadata")
	ErrManagedProfileInconsistent = errors.New("managed NetBird profile is missing or inconsistent")
)

type daemonProfileClient interface {
	ListProfiles(ctx context.Context, in *daemonpb.ListProfilesRequest, opts ...grpc.CallOption) (*daemonpb.ListProfilesResponse, error)
	GetActiveProfile(ctx context.Context, in *daemonpb.GetActiveProfileRequest, opts ...grpc.CallOption) (*daemonpb.GetActiveProfileResponse, error)
	AddProfile(ctx context.Context, in *daemonpb.AddProfileRequest, opts ...grpc.CallOption) (*daemonpb.AddProfileResponse, error)
	SwitchProfile(ctx context.Context, in *daemonpb.SwitchProfileRequest, opts ...grpc.CallOption) (*daemonpb.SwitchProfileResponse, error)
	RemoveProfile(ctx context.Context, in *daemonpb.RemoveProfileRequest, opts ...grpc.CallOption) (*daemonpb.RemoveProfileResponse, error)
}

type ManagedProfileStore struct {
	client   daemonProfileClient
	username string
}

func NewManagedProfileStore(client daemonProfileClient, username string) *ManagedProfileStore {
	return &ManagedProfileStore{client: client, username: username}
}

func (s *ManagedProfileStore) Ensure(ctx context.Context, expectedID string) (Profile, error) {
	profiles, err := s.list(ctx)
	if err != nil {
		return Profile{}, err
	}
	if expectedID != "" {
		for _, profile := range profiles {
			if profile.ID == expectedID {
				return profile, nil
			}
		}
		return Profile{}, fmt.Errorf("%w: expected profile %s", ErrManagedProfileInconsistent, expectedID)
	}

	for _, profile := range profiles {
		if profile.Name == ManagedProfileName {
			return Profile{}, ErrManagedProfileConflict
		}
	}
	response, err := s.client.AddProfile(ctx, &daemonpb.AddProfileRequest{
		Username:    s.username,
		ProfileName: ManagedProfileName,
	})
	if err != nil {
		return Profile{}, fmt.Errorf("add managed NetBird profile: %w", err)
	}
	if response.GetId() == "" {
		return Profile{}, fmt.Errorf("%w: daemon returned an empty profile ID", ErrManagedProfileInconsistent)
	}
	return Profile{ID: response.GetId(), Name: ManagedProfileName, Username: s.username}, nil
}

func (s *ManagedProfileStore) Validate(ctx context.Context, profileID string) (Profile, error) {
	if profileID == "" {
		return Profile{}, ErrManagedProfileInconsistent
	}
	return s.Ensure(ctx, profileID)
}

func (s *ManagedProfileStore) Select(ctx context.Context, profileID string) error {
	if _, err := s.Validate(ctx, profileID); err != nil {
		return err
	}
	response, err := s.client.SwitchProfile(ctx, &daemonpb.SwitchProfileRequest{
		ProfileName: stringPointer(profileID),
		Username:    stringPointer(s.username),
	})
	if err != nil {
		return fmt.Errorf("select managed NetBird profile: %w", err)
	}
	if response.GetId() != profileID {
		return fmt.Errorf("%w: daemon selected profile %s", ErrManagedProfileInconsistent, response.GetId())
	}
	return nil
}

func (s *ManagedProfileStore) Remove(ctx context.Context, profileID string) error {
	if _, err := s.Validate(ctx, profileID); err != nil {
		return err
	}
	active, err := s.client.GetActiveProfile(ctx, &daemonpb.GetActiveProfileRequest{})
	if err != nil {
		return fmt.Errorf("read active NetBird profile: %w", err)
	}
	if active.GetId() == profileID {
		response, err := s.client.SwitchProfile(ctx, &daemonpb.SwitchProfileRequest{
			ProfileName: stringPointer("default"),
			Username:    stringPointer(s.username),
		})
		if err != nil {
			return fmt.Errorf("select default profile before managed removal: %w", err)
		}
		if response.GetId() != "default" {
			return fmt.Errorf("%w: daemon did not select default before removal", ErrManagedProfileInconsistent)
		}
	}
	response, err := s.client.RemoveProfile(ctx, &daemonpb.RemoveProfileRequest{
		Username:    s.username,
		ProfileName: profileID,
	})
	if err != nil {
		return fmt.Errorf("remove managed NetBird profile: %w", err)
	}
	if response.GetId() != profileID {
		return fmt.Errorf("%w: daemon removed profile %s", ErrManagedProfileInconsistent, response.GetId())
	}
	return nil
}

func (s *ManagedProfileStore) list(ctx context.Context) ([]Profile, error) {
	response, err := s.client.ListProfiles(ctx, &daemonpb.ListProfilesRequest{Username: s.username})
	if err != nil {
		return nil, fmt.Errorf("list NetBird profiles: %w", err)
	}
	profiles := NormalizeProfiles(response)
	for index := range profiles {
		profiles[index].Username = s.username
	}
	return profiles, nil
}

func stringPointer(value string) *string { return &value }
