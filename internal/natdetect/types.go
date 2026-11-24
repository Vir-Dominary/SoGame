package natdetect

// NATType 是 RFC 3489 简化版两步探测可判定的 NAT 类型集合。
// 注意：不包含 Port Restricted Cone——区分它需要第三次"服务器换端口
// 主动回打"探测，公共 STUN 服务器不配合，两步算法原理上无法返回该值。
type NATType string

const (
	NATTypeUnknown    NATType = "Unknown"
	NATTypeOpen       NATType = "Open"
	NATTypeFullCone   NATType = "FullCone"
	NATTypeSymmetric  NATType = "Symmetric"
	NATTypeUDPBlocked NATType = "UDPBlocked"
)

type NATResult struct {
	Type       NATType `json:"type"`
	LocalIP    string  `json:"localIp"`
	LocalPort  int     `json:"localPort"`
	PublicIP   string  `json:"publicIp"`
	PublicPort int     `json:"publicPort"`
	// PrimaryServer/SecondaryServer 是两阶段实际使用的服务器地址，
	// 仅用于诊断日志，不参与前端契约（前端不消费）。
	PrimaryServer   string `json:"-"`
	SecondaryServer string `json:"-"`
	Suggestion      string `json:"suggestion"`
	// Error 是用户向友好文案（直接上屏）；原始错误见 RawError。
	Error string `json:"error,omitempty"`
	// RawError 携带探测过程中的原始错误（DNS/超时/协议），由调用方
	// 落日志，不序列化、不上屏。
	RawError error `json:"-"`
}

func (r NATResult) SuggestionText() string {
	switch r.Type {
	case NATTypeOpen:
		return "网络开放，P2P 直连成功率极高，两种模式均可使用"
	case NATTypeFullCone:
		// 共享 socket 两步对比的语义：同一内网端点对不同服务器映射
		// 一致（端口无关映射），涵盖 Full Cone 与 Restricted Cone——
		// 对 ICE/NetBird 直连等价友好，无需进一步区分。
		return "端口无关映射（Full/Restricted Cone），P2P 直连友好，直连成功率高"
	case NATTypeSymmetric:
		return "Symmetric NAT，P2P 直连几乎不可能，建议使用极速模式（中继）或经典模式 supernode 中继"
	case NATTypeUDPBlocked:
		return "UDP 被阻止，两种模式均不可用，请检查防火墙或路由器设置"
	default:
		return "未检测到 NAT 类型"
	}
}
