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

package platform

import (
	"fmt"
	"net"
	"time"
)

// NetworkCheckResult 网络检查结果
type NetworkCheckResult struct {
	HasInternetConnection bool   // 是否有互联网连接
	Message               string // 诊断消息
}

// CheckNetworkConnection 检查网络连接状态
// 通过尝试连接到公共DNS服务器来判断
func CheckNetworkConnection() NetworkCheckResult {
	// 尝试连接到多个常见的DNS服务器
	dnsServers := []string{
		"8.8.8.8:53",      // Google DNS
		"1.1.1.1:53",      // Cloudflare DNS
		"114.114.114.114:53", // 114 DNS (CN)
	}

	for _, server := range dnsServers {
		// 设置超时时间
		conn, err := net.DialTimeout("tcp", server, 2*time.Second)
		if err == nil {
			conn.Close()
			return NetworkCheckResult{
				HasInternetConnection: true,
				Message:               "网络连接正常",
			}
		}
	}

	return NetworkCheckResult{
		HasInternetConnection: false,
		Message:               "无网络连接或DNS不可用，应用可能无法正常工作",
	}
}

// CheckSupernodeReachable 检查Supernode是否可达
func CheckSupernodeReachable(supernodeAddr string) bool {
	addr, err := net.ResolveUDPAddr("udp", supernodeAddr)
	if err != nil {
		return false
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	return true
}

// GetLocalIPAddress 获取本机IP地址
func GetLocalIPAddress() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("failed to determine local IP: %w", err)
	}
	defer conn.Close()

	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", fmt.Errorf("failed to determine local IP: unexpected address type %T", conn.LocalAddr())
	}
	return localAddr.IP.String(), nil
}
