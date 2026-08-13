package nbdaemon

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const MSIRemove MSIAction = "remove"

var productCodePattern = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)

type RemovalRunner interface {
	Remove(ctx context.Context, productCode, logPath string) error
}

type DaemonRemover struct {
	runner RemovalRunner
}

func NewDaemonRemover(runner RemovalRunner) *DaemonRemover {
	return &DaemonRemover{runner: runner}
}

func (r *DaemonRemover) Remove(ctx context.Context, confirmed bool, productCode, logPath string) error {
	if !confirmed {
		return ErrRemovalNotConfirmed
	}
	if !productCodePattern.MatchString(productCode) {
		return fmt.Errorf("invalid NetBird MSI product code")
	}
	if !filepath.IsAbs(logPath) || !strings.EqualFold(filepath.Ext(logPath), ".log") {
		return fmt.Errorf("invalid uninstaller log path")
	}
	if err := r.runner.Remove(ctx, productCode, logPath); err != nil {
		return fmt.Errorf("remove official NetBird service: %w", err)
	}
	return nil
}

func BuildMSIRemovalArguments(productCode, logPath string) ([]string, error) {
	if !productCodePattern.MatchString(productCode) {
		return nil, fmt.Errorf("invalid NetBird MSI product code")
	}
	return []string{
		"/x", productCode,
		"/quiet", "/qn", "/norestart",
		"/l*v", logPath,
	}, nil
}
