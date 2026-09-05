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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	pat     string
	http    *http.Client
}

type Group struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Peers          []PeerMinimum   `json:"peers"`
	PeersCount     int             `json:"peers_count"`
	Resources      json.RawMessage `json:"resources"`
	ResourcesCount int             `json:"resources_count"`
}

type PeerMinimum struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type GroupMinimum struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	PeersCount     int    `json:"peers_count"`
	ResourcesCount int    `json:"resources_count"`
}

type Peer struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	IP        string         `json:"ip"`
	Connected bool           `json:"connected"`
	Hostname  string         `json:"hostname"`
	LastLogin string         `json:"last_login"`
	Groups    []GroupMinimum `json:"groups"`
}

type SetupKey struct {
	ID                  string   `json:"id"`
	Key                 string   `json:"key"`
	Name                string   `json:"name"`
	Expires             string   `json:"expires"`
	Type                string   `json:"type"`
	Valid               bool     `json:"valid"`
	Revoked             bool     `json:"revoked"`
	UsedTimes           int      `json:"used_times"`
	LastUsed            string   `json:"last_used"`
	AutoGroups          []string `json:"auto_groups"`
	UsageLimit          int      `json:"usage_limit"`
	Ephemeral           bool     `json:"ephemeral"`
	AllowExtraDNSLabels bool     `json:"allow_extra_dns_labels"`
}

type SetupKeyClear struct {
	SetupKey
	Key string `json:"key"`
}

type Policy struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Enabled     bool         `json:"enabled"`
	Rules       []PolicyRule `json:"rules"`
}

type PolicyRule struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Enabled       bool       `json:"enabled"`
	Action        string     `json:"action"`
	Protocol      string     `json:"protocol"`
	Bidirectional bool       `json:"bidirectional"`
	Sources       []GroupRef `json:"sources"`
	Destinations  []GroupRef `json:"destinations"`
}

type GroupRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type policyRuleRequest struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Enabled       bool     `json:"enabled"`
	Action        string   `json:"action"`
	Protocol      string   `json:"protocol"`
	Bidirectional bool     `json:"bidirectional"`
	Sources       []string `json:"sources"`
	Destinations  []string `json:"destinations"`
}

type policyRequest struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Enabled     bool                `json:"enabled"`
	Rules       []policyRuleRequest `json:"rules"`
}

func New(baseURL, pat string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), pat: pat, http: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.pat)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("netbird API %s %s returned HTTP %d", method, path, resp.StatusCode)
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(output)
}

func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	var groups []Group
	err := c.do(ctx, http.MethodGet, "/api/groups", nil, &groups)
	return groups, err
}

func (c *Client) CreateGroup(ctx context.Context, name string) (Group, error) {
	var group Group
	err := c.do(ctx, http.MethodPost, "/api/groups", map[string]any{"name": name, "peers": []string{}}, &group)
	return group, err
}

func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/groups/"+id, nil, nil)
}

// DeletePeer 删除一个 NetBird peer（强制其客户端下线并从账号移除）。
// 房间解散时用于强制断开成员。
func (c *Client) DeletePeer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/peers/"+id, nil, nil)
}

func (c *Client) ListSetupKeys(ctx context.Context) ([]SetupKey, error) {
	var keys []SetupKey
	err := c.do(ctx, http.MethodGet, "/api/setup-keys", nil, &keys)
	return keys, err
}

func (c *Client) CreateSetupKey(ctx context.Context, name, groupID string) (SetupKeyClear, error) {
	var key SetupKeyClear
	body := map[string]any{
		"name": name, "type": "reusable", "expires_in": 0,
		"auto_groups": []string{groupID}, "usage_limit": 0,
		"ephemeral": false, "allow_extra_dns_labels": false,
	}
	err := c.do(ctx, http.MethodPost, "/api/setup-keys", body, &key)
	return key, err
}

func (c *Client) RevokeSetupKey(ctx context.Context, id string, groups []string) error {
	body := map[string]any{"revoked": true, "auto_groups": groups}
	return c.do(ctx, http.MethodPut, "/api/setup-keys/"+id, body, nil)
}

func (c *Client) DeleteSetupKey(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/setup-keys/"+id, nil, nil)
}

func (c *Client) ListPolicies(ctx context.Context) ([]Policy, error) {
	var policies []Policy
	err := c.do(ctx, http.MethodGet, "/api/policies", nil, &policies)
	return policies, err
}

func (c *Client) CreateRoomPolicy(ctx context.Context, name, groupID string) (Policy, error) {
	var policy Policy
	body := policyRequest{
		Name: name, Description: "Room-internal policy managed by Room API", Enabled: true,
		Rules: []policyRuleRequest{{
			Name: name + "-rule", Description: "Allow Peers in the same room",
			Enabled: true, Action: "accept", Protocol: "all", Bidirectional: true,
			Sources: []string{groupID}, Destinations: []string{groupID},
		}},
	}
	err := c.do(ctx, http.MethodPost, "/api/policies", body, &policy)
	return policy, err
}

func (c *Client) DeletePolicy(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/policies/"+id, nil, nil)
}

func (c *Client) DisablePolicy(ctx context.Context, policy Policy) error {
	body := policyRequest{Name: policy.Name, Description: policy.Description, Enabled: false}
	for _, rule := range policy.Rules {
		sources, destinations := make([]string, 0, len(rule.Sources)), make([]string, 0, len(rule.Destinations))
		for _, group := range rule.Sources {
			sources = append(sources, group.ID)
		}
		for _, group := range rule.Destinations {
			destinations = append(destinations, group.ID)
		}
		body.Rules = append(body.Rules, policyRuleRequest{
			Name: rule.Name, Description: rule.Description, Enabled: rule.Enabled,
			Action: rule.Action, Protocol: rule.Protocol, Bidirectional: rule.Bidirectional,
			Sources: sources, Destinations: destinations,
		})
	}
	return c.do(ctx, http.MethodPut, "/api/policies/"+policy.ID, body, nil)
}

func (c *Client) ListPeers(ctx context.Context) ([]Peer, error) {
	var peers []Peer
	err := c.do(ctx, http.MethodGet, "/api/peers", nil, &peers)
	return peers, err
}