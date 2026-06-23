package tap

import (
	"errors"
	"strings"

	"sogame/internal/nic"
)

type ResolveStatus string

const (
	ResolveNoKnownAdapter ResolveStatus = "NoKnownAdapter"
	ResolveFound          ResolveStatus = "Found"
	ResolveMissing        ResolveStatus = "Missing"
	ResolveInvalid        ResolveStatus = "Invalid"
	ResolveNameMismatch   ResolveStatus = "NameMismatch"
)

var (
	findByNetCfgID     = nic.FindByNetCfgID
	findByFriendlyName = nic.FindByFriendlyName
	findByLuid         = nic.FindByLuid
	netCfgIDFromLuid   = nic.NetCfgIDFromLuid
)

type ResolveResult struct {
	Status           ResolveStatus
	Known            *KnownAdapter
	Info             *nic.Info
	NetCfgInstanceID string
}

func ResolveKnownAdapter(expectedName string) (ResolveResult, error) {
	known, err := LoadKnownAdapter()
	if err != nil {
		return ResolveResult{}, err
	}
	if known == nil {
		return ResolveResult{Status: ResolveNoKnownAdapter}, nil
	}

	if netCfgID := strings.TrimSpace(known.NetCfgInstanceID); netCfgID != "" {
		info, err := findByNetCfgID(netCfgID)
		if err != nil {
			if errors.Is(err, nic.ErrNotFound) {
				return ResolveResult{Status: ResolveMissing, Known: known, NetCfgInstanceID: netCfgID}, nil
			}
			return ResolveResult{}, err
		}
		return classifyResolvedAdapter(known, info, netCfgID, expectedName), nil
	}

	if known.LUID == 0 {
		return ResolveResult{Status: ResolveNoKnownAdapter, Known: known}, nil
	}

	info, err := findByLuid(known.LUID)
	if err != nil {
		if errors.Is(err, nic.ErrNotFound) {
			return ResolveResult{Status: ResolveMissing, Known: known}, nil
		}
		return ResolveResult{}, err
	}
	netCfgID, err := netCfgIDFromLuid(info.Luid)
	if err != nil {
		if errors.Is(err, nic.ErrNotFound) {
			return ResolveResult{Status: ResolveInvalid, Known: known, Info: info}, nil
		}
		return ResolveResult{}, err
	}
	return classifyResolvedAdapter(known, info, netCfgID, expectedName), nil
}

func classifyResolvedAdapter(known *KnownAdapter, info *nic.Info, netCfgID, expectedName string) ResolveResult {
	result := ResolveResult{
		Status:           ResolveFound,
		Known:            known,
		Info:             info,
		NetCfgInstanceID: strings.TrimSpace(netCfgID),
	}
	if info == nil || !IsWindowsDescription(info.Description) {
		result.Status = ResolveInvalid
		return result
	}
	if expectedName != "" && !strings.EqualFold(info.FriendlyName, expectedName) {
		result.Status = ResolveNameMismatch
	}
	return result
}
