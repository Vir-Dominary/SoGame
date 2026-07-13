package ipam

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"sogame/wireguard/server/internal/db"
)

const (
	roomSubnetPrefix = "10.88"     // 房间子网前缀
	subnetStartIndex = 0           // 房间子网起始索引
	clientIPStart    = 2           // 客户端 IP 起始（.1 保留给网关）
	clientIPEnd      = 254         // 客户端 IP 结束
)

// IPAM 管理 WireGuard 虚拟网络的 IP 分配
type IPAM struct {
	db *db.Database
}

// New 创建 IPAM 实例
func New(database *db.Database) *IPAM {
	return &IPAM{db: database}
}

// AllocateRoomSubnet 为新房间分配子网 10.88.X.0/24
// 遍历已有房间，找到第一个未使用的子网
func (i *IPAM) AllocateRoomSubnet() (string, error) {
	rooms, err := i.db.ListRooms()
	if err != nil {
		return "", fmt.Errorf("list rooms: %w", err)
	}

	used := make(map[int]bool)
	for _, r := range rooms {
		idx, err := extractSubnetIndex(r.Subnet)
		if err == nil {
			used[idx] = true
		}
	}

	for idx := subnetStartIndex; idx <= 255; idx++ {
		if !used[idx] {
			return fmt.Sprintf("%s.%d.0/24", roomSubnetPrefix, idx), nil
		}
	}

	return "", fmt.Errorf("no available subnet")
}

// AllocatePeerIP 在指定房间子网中分配客户端 IP
// 从 .2 开始，跳过已使用的 IP
func (i *IPAM) AllocatePeerIP(roomID, subnet string) (string, error) {
	usedIPs, err := i.db.GetUsedIPsInRoom(roomID)
	if err != nil {
		return "", fmt.Errorf("get used ips: %w", err)
	}

	used := make(map[int]bool)
	for _, ip := range usedIPs {
		host := extractLastOctet(ip)
		if host >= 0 {
			used[host] = true
		}
	}

	subnetIdx, err := extractSubnetIndex(subnet)
	if err != nil {
		return "", fmt.Errorf("invalid subnet: %w", err)
	}

	for host := clientIPStart; host <= clientIPEnd; host++ {
		if !used[host] {
			return fmt.Sprintf("%s.%d.%d", roomSubnetPrefix, subnetIdx, host), nil
		}
	}

	return "", fmt.Errorf("no available IP in subnet %s", subnet)
}

// extractSubnetIndex 从 "10.88.X.0/24" 中提取 X
func extractSubnetIndex(subnet string) (int, error) {
	// 去掉 /24
	ipPart := strings.SplitN(subnet, "/", 2)[0]
	parts := strings.Split(ipPart, ".")
	if len(parts) != 4 {
		return 0, fmt.Errorf("invalid subnet format: %s", subnet)
	}
	return strconv.Atoi(parts[2])
}

// extractLastOctet 从 "10.88.X.Y" 中提取 Y
func extractLastOctet(ip string) int {
	ip = strings.SplitN(ip, "/", 2)[0]
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return -1
	}
	host, err := strconv.Atoi(parts[3])
	if err != nil {
		return -1
	}
	return host
}

// GatewayIP 返回子网的网关地址（.1）
func GatewayIP(subnet string) string {
	subnetIdx, err := extractSubnetIndex(subnet)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s.%d.1", roomSubnetPrefix, subnetIdx)
}

// ValidateIP 检查 IP 是否在子网范围内
func ValidateIP(ip, subnet string) bool {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return false
	}
	return ipNet.Contains(net.ParseIP(ip))
}
