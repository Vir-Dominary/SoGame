package config

const (
	AppName       = "SoGame"
	AppVersion    = "2.0"
	AppAuthor     = "vir_dominary"
	AppURL        = "https://github.com/vir-dominary"
	AppBilibili   = "https://space.bilibili.com/454851989"
	AppDesc       = "SoGame - 远程组网工具"
	AppSponsorURL = "https://www.ifdian.net/a/vir_dominary?utm_source=copylink&utm_medium=link"

	// DefaultRoomAPIURL 是极速模式（netbird）的默认 Room API 服务地址。
	// 指向 sogame-netbird 实际运行的控制平面（123.56.254.224）；
	// 本地开发可在 UI 设置或配置文件中临时指向本地 Mock（tools/room-api-mock）。
	// 注意：此处必须是所有客户端都能访问到的同一服务端，
	// 否则不同机器创建/加入的房间互不可见。
	DefaultRoomAPIURL = "http://123.56.254.224"
)
