package platform

// IsSoGameAdapterExists 检查 SoGame 专属 TAP 适配器是否存在
func IsSoGameAdapterExists() bool {
	if !IsWindows() {
		return true
	}
	return AdapterExists(SoGameAdapterName)
}

// isTapAdapterInstalled 检查是否存在任何 TAP 适配器
func isTapAdapterInstalled() bool {
	if !IsWindows() {
		return true
	}
	if AdapterExists(SoGameAdapterName) {
		return true
	}
	return TapAdapterExists()
}

// FindTapInterfaceName 查找 TAP 接口名，优先返回 SoGame 专属适配器
func FindTapInterfaceName() string {
	if !IsWindows() {
		return ""
	}
	return FindTapAdapterName()
}

// IsNetworkAdapterReady 检查网卡是否就绪
func IsNetworkAdapterReady() bool {
	if !IsWindows() {
		return true
	}
	return isTapAdapterInstalled()
}
