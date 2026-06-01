package platform

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"

	"netjoin/internal/logger"
)

// AdapterInfo 网卡基础信息
type AdapterInfo struct {
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	GUID   string `json:"guid"`
	MAC    string `json:"mac"`
	Status string `json:"status"`
}

// GetAllAdapters 通过 Win32 API GetAdaptersAddresses 枚举所有网卡
func GetAllAdapters() ([]AdapterInfo, error) {
	if !IsWindows() {
		return nil, fmt.Errorf("not supported on this platform")
	}

	iphlpapi := syscall.NewLazyDLL("iphlpapi.dll")
	proc := iphlpapi.NewProc("GetAdaptersAddresses")

	const (
		afUnspec         = 0
		gaaSkipUnicast   = 0x0001
		gaaSkipAnycast   = 0x0002
		gaaSkipMulticast = 0x0004
		gaaSkipDNSServer = 0x0008
	)
	flags := uintptr(gaaSkipUnicast | gaaSkipAnycast | gaaSkipMulticast | gaaSkipDNSServer)

	var bufSize uint32
	proc.Call(uintptr(afUnspec), flags, 0, 0, uintptr(unsafe.Pointer(&bufSize)))

	buf := make([]byte, bufSize)
	ret, _, _ := proc.Call(
		uintptr(afUnspec), flags, 0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bufSize)),
	)
	if ret != 0 {
		return nil, syscall.Errno(ret)
	}

	var adapters []AdapterInfo
	ptr := (*rawAdapter)(unsafe.Pointer(&buf[0]))
	for ptr != nil {
		info := AdapterInfo{
			Name:   utf16PtrToString(ptr.FriendlyName),
			Desc:   utf16PtrToString(ptr.Description),
			GUID:   bytePtrToString(ptr.AdapterName),
			MAC:    formatMAC(ptr.PhysicalAddress[:ptr.PhysicalAddressLength]),
			Status: operStatusString(ptr.OperStatus),
		}
		adapters = append(adapters, info)
		ptr = ptr.Next
	}
	return adapters, nil
}

// x64 下 IP_ADAPTER_ADDRESSES 结构体关键字段偏移
type rawAdapter struct {
	_                     [8]byte // union Alignment / Length+IfIndex
	Next                  *rawAdapter
	AdapterName           *byte   // PCHAR, GUID string
	_                     [40]byte // FirstUnicast → DnsSuffix
	Description           *uint16
	FriendlyName          *uint16
	PhysicalAddress       [8]byte
	PhysicalAddressLength uint32
	_                     [8]byte // Flags + Mtu
	_                     uint32  // IfType
	OperStatus            uint32
}

// ---- 查找函数 ----

func getAllAdapters() []AdapterInfo {
	adapters, err := GetAllAdapters()
	if err != nil {
		logger.Warnf("Win32 GetAdaptersAddresses 失败: %v", err)
		return nil
	}
	return adapters
}

// AdapterExists 检查指定名称的网卡是否存在
func AdapterExists(name string) bool {
	for _, a := range getAllAdapters() {
		if name == a.Name {
			return true
		}
	}
	return false
}

// FindAdapterByDesc 通过描述模糊匹配网卡，返回名称。支持通配符 *
func FindAdapterByDesc(pattern string) string {
	for _, a := range getAllAdapters() {
		if matchSimplePattern(pattern, a.Desc) {
			return a.Name
		}
	}
	return ""
}

// isTapDesc 检查描述是否为 TAP 类型网卡
func isTapDesc(desc string) bool {
	return strContains(desc, "tap-windows") || strContains(desc, "tap0901")
}

// TapAdapterExists 检查是否存在描述含 tap-windows 或 tap0901 的网卡
func TapAdapterExists() bool {
	for _, a := range getAllAdapters() {
		if isTapDesc(toLowerASCII(a.Desc)) {
			return true
		}
	}
	return false
}

// FindTapAdapterName 返回 TAP 网卡名称，优先 SoGame-VPN
func FindTapAdapterName() string {
	for _, a := range getAllAdapters() {
		if a.Name == SoGameAdapterName {
			return a.Name
		}
	}
	for _, a := range getAllAdapters() {
		if isTapDesc(toLowerASCII(a.Desc)) {
			return a.Name
		}
	}
	return ""
}

// AdapterStatus 返回网卡运行状态（Up/Down/Unknown...）
func AdapterStatus(name string) string {
	for _, a := range getAllAdapters() {
		if a.Name == name {
			return a.Status
		}
	}
	return "NotPresent"
}

// IsAdapterUp 检查网卡是否处于启用状态
func IsAdapterUp(name string) bool {
	return AdapterStatus(name) == "Up"
}

// ---- 内部工具函数 ----

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	return syscall.UTF16ToString((*[1 << 10]uint16)(unsafe.Pointer(p))[:])
}

func bytePtrToString(p *byte) string {
	if p == nil {
		return ""
	}
	// AdapterName 是 narrow string（字节串），不是 UTF-16
	b := (*[1 << 10]byte)(unsafe.Pointer(p))
	n := 0
	for b[n] != 0 {
		n++
	}
	return string(b[:n])
}

func formatMAC(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return net.HardwareAddr(b).String()
}

func operStatusString(s uint32) string {
	switch s {
	case 1:
		return "Up"
	case 2:
		return "Down"
	case 3:
		return "Testing"
	case 4:
		return "Unknown"
	case 5:
		return "Dormant"
	case 6:
		return "NotPresent"
	case 7:
		return "LowerLayerDown"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

func matchSimplePattern(pattern, s string) bool {
	pl, sl := len(pattern), len(s)
	if pl == 0 {
		return sl == 0
	}
	pi, si := 0, 0
	starIdx, matchIdx := -1, 0
	for si < sl {
		if pi < pl && pattern[pi] == '*' {
			starIdx = pi
			matchIdx = si
			pi++
		} else if pi < pl && (pattern[pi] == s[si] || toLowerByte(pattern[pi]) == toLowerByte(s[si])) {
			pi++
			si++
		} else if starIdx != -1 {
			pi = starIdx + 1
			matchIdx++
			si = matchIdx
		} else {
			return false
		}
	}
	for pi < pl && pattern[pi] == '*' {
		pi++
	}
	return pi == pl
}

func toLowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func toLowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
