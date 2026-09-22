package natdetect

type NATType string

const (
	NATTypeUnknown         NATType = "Unknown"
	NATTypeOpen            NATType = "Open"
	NATTypeFullCone        NATType = "FullCone"
	NATTypeRestrictedCone  NATType = "RestrictedCone"
	NATTypePortRestricted  NATType = "PortRestricted"
	NATTypeSymmetric       NATType = "Symmetric"
	NATTypeUDPBlocked      NATType = "UDPBlocked"
)

type NATResult struct {
	Type         NATType `json:"type"`
	LocalIP      string  `json:"localIp"`
	LocalPort    int     `json:"localPort"`
	PublicIP     string  `json:"publicIp"`
	PublicPort   int     `json:"publicPort"`
	STUNServerA  string  `json:"stunServerA"`
	STUNServerB  string  `json:"stunServerB"`
	Suggestion   string  `json:"suggestion"`
	Error        string  `json:"error,omitempty"`
}

func (r NATResult) SuggestionText() string {
	switch r.Type {
	case NATTypeOpen:
		return "网络开放，P2P 直连成功率极高，两种模式均可使用"
	case NATTypeFullCone:
		return "Full Cone NAT，P2P 直连成功率极高，极速模式优先 P2P 直连"
	case NATTypeRestrictedCone:
		return "Restricted Cone NAT，P2P 直连可行，首次连接可能需要数秒"
	case NATTypePortRestricted:
		return "Port Restricted Cone NAT，P2P 直连可能需多次尝试，部分场景需中继"
	case NATTypeSymmetric:
		return "Symmetric NAT，P2P 直连几乎不可能，建议使用极速模式（中继）或经典模式 supernode 中继"
	case NATTypeUDPBlocked:
		return "UDP 被阻止，两种模式均不可用，请检查防火墙或路由器设置"
	default:
		return "未检测到 NAT 类型"
	}
}
