package nbdaemon

import "errors"

var (
	ErrArtifactMissing     = errors.New("NetBird artifact is missing")
	ErrArtifactSize        = errors.New("NetBird artifact size mismatch")
	ErrArtifactDigest      = errors.New("NetBird artifact digest mismatch")
	ErrSignatureInvalid    = errors.New("NetBird artifact signature is invalid")
	ErrPublisherMismatch   = errors.New("NetBird artifact publisher mismatch")
	ErrServiceMissing      = errors.New("NetBird service is missing")
	ErrServiceAccess       = errors.New("NetBird service status access denied")
	ErrServiceUnavailable  = errors.New("NetBird service is unavailable")
	ErrElevationRequired   = errors.New("administrator elevation is required")
	ErrElevationCancelled  = errors.New("administrator elevation was cancelled by the user")
	ErrElevationTimedOut   = errors.New("elevated helper did not complete in time")
	ErrUnsupportedAction   = errors.New("unsupported privileged action")
	ErrRemovalNotConfirmed = errors.New("NetBird service removal was not confirmed")
)
