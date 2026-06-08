package platform

import "sogame/internal/diagnostics"

type DependencyCheckResult = diagnostics.DependencyCheckResult

func CheckAllDependencies() []DependencyCheckResult {
	return diagnostics.CheckAllDependencies(IsNetworkAdapterReady(), CheckAdminPrivileges())
}
