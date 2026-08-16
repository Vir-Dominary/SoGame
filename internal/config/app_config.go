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
	// 指向 sogame-netbird 部署在 legengen.top 的真实控制平面；
	// 本地开发可在 UI 设置或配置文件中临时指向本地 Mock（tools/room-api-mock）。
	DefaultRoomAPIURL = "https://legengen.top"
)
