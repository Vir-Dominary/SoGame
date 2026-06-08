package tap

import (
	"fmt"
	"strings"

	"sogame/internal/nic"
)

func RememberKnownAdapter(info nic.Info, netCfgID string) error {
	id := strings.TrimSpace(netCfgID)
	if id == "" {
		resolvedID, err := netCfgIDFromLuid(info.Luid)
		if err != nil {
			return fmt.Errorf("resolve TAP NetCfgInstanceId: %w", err)
		}
		id = resolvedID
	}

	return SaveKnownAdapter(KnownAdapter{
		NetCfgInstanceID: id,
		LUID:             info.Luid,
		FriendlyName:     info.FriendlyName,
		Description:      info.Description,
	})
}

func RememberKnownAdapterByFriendlyName(name string) (*nic.Info, error) {
	info, err := findByFriendlyName(name)
	if err != nil {
		return nil, err
	}
	if err := RememberKnownAdapter(*info, ""); err != nil {
		return nil, err
	}
	return info, nil
}
