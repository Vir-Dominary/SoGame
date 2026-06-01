package platform

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const SoGameAdapterName = "SoGame-VPN"

type TapInstallStatus int

const (
	TapInstallSuccess TapInstallStatus = iota
	TapAlreadyInstalled
	TapInstallFailed
)

func IsWindows() bool {
	return runtime.GOOS == "windows"
}

func CheckAdminPrivileges() bool {
	if !IsWindows() {
		return true
	}

	var token windows.Token
	currentProcess, _ := windows.GetCurrentProcess()
	err := windows.OpenProcessToken(currentProcess, windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()

	var elevation uint32
	var returnedLen uint32
	err = windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevation)), uint32(unsafe.Sizeof(elevation)), &returnedLen)
	if err != nil {
		return false
	}

	return elevation != 0
}
