//go:build windows

package nbdaemon

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const netBirdServiceName = "NetBird"

type windowsServiceBackend struct {
	productCode string
}

func newServiceBackend(productCode string) ServiceBackend {
	return &windowsServiceBackend{productCode: productCode}
}

func (b *windowsServiceBackend) Lookup(context.Context) (ServiceRecord, error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return ServiceRecord{}, ErrServiceAccess
		}
		return ServiceRecord{}, err
	}
	defer windows.CloseServiceHandle(manager)

	name, err := windows.UTF16PtrFromString(netBirdServiceName)
	if err != nil {
		return ServiceRecord{}, err
	}
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST):
			return ServiceRecord{}, ErrServiceMissing
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return ServiceRecord{}, ErrServiceAccess
		default:
			return ServiceRecord{}, err
		}
	}
	defer windows.CloseServiceHandle(service)

	status, err := queryServiceStatus(service)
	if err != nil {
		return ServiceRecord{}, err
	}
	binaryPath, err := queryServiceBinaryPath(service)
	if err != nil {
		return ServiceRecord{}, err
	}
	return ServiceRecord{
		Installed:  true,
		Running:    status.CurrentState == windows.SERVICE_RUNNING,
		BinaryPath: firstCommandArgument(binaryPath),
		Version:    readMSIDisplayVersion(b.productCode),
		ProcessID:  status.ProcessId,
	}, nil
}

func queryServiceStatus(service windows.Handle) (windows.SERVICE_STATUS_PROCESS, error) {
	var status windows.SERVICE_STATUS_PROCESS
	var needed uint32
	err := windows.QueryServiceStatusEx(
		service,
		windows.SC_STATUS_PROCESS_INFO,
		(*byte)(unsafe.Pointer(&status)),
		uint32(unsafe.Sizeof(status)),
		&needed,
	)
	return status, err
}

func queryServiceBinaryPath(service windows.Handle) (string, error) {
	n := uint32(1024)
	for attempt := 0; attempt < 4; attempt++ {
		if n > 64<<10 {
			return "", errors.New("service binary path exceeds maximum size")
		}
		buffer := make([]byte, n)
		config := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buffer[0]))
		err := windows.QueryServiceConfig(service, config, n, &n)
		if err == nil {
			return windows.UTF16PtrToString(config.BinaryPathName), nil
		}
		if !errors.Is(err, syscall.ERROR_INSUFFICIENT_BUFFER) || n <= uint32(len(buffer)) {
			return "", err
		}
	}
	return "", errors.New("service binary name lookup did not converge")
}

func firstCommandArgument(commandLine string) string {
	arguments, err := windows.DecomposeCommandLine(commandLine)
	if err != nil || len(arguments) == 0 {
		return commandLine
	}
	return arguments[0]
}

func readMSIDisplayVersion(productCode string) string {
	if productCode == "" {
		return ""
	}
	path := fmt.Sprintf(`SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\%s`, productCode)
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer key.Close()
	version, _, err := key.GetStringValue("DisplayVersion")
	if err != nil {
		return ""
	}
	return version
}
