//go:build !windows

package nbdaemon

import (
	"context"
	"fmt"

	releasebuild "sogame/internal/releasebuild"
)

type WindowsSignatureVerifier struct{}

func (WindowsSignatureVerifier) Verify(context.Context, string, releasebuild.Publisher) error {
	return fmt.Errorf("%w: Authenticode verification requires Windows", ErrSignatureInvalid)
}
