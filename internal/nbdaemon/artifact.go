package nbdaemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	releasebuild "sogame/internal/releasebuild"
)

type SignatureVerifier interface {
	Verify(ctx context.Context, path string, expected releasebuild.Publisher) error
}

type ArtifactVerifier struct {
	signatures SignatureVerifier
}

func NewArtifactVerifier(signatures SignatureVerifier) *ArtifactVerifier {
	return &ArtifactVerifier{signatures: signatures}
}

func (v *ArtifactVerifier) Verify(ctx context.Context, path string, expected releasebuild.WindowsArtifact) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrArtifactMissing
		}
		return fmt.Errorf("open NetBird artifact: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat NetBird artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return ErrArtifactMissing
	}
	if info.Size() != expected.Size {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrArtifactSize, expected.Size, info.Size())
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash NetBird artifact: %w", err)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, expected.SHA256) {
		return fmt.Errorf("%w: got %s", ErrArtifactDigest, got)
	}
	if v.signatures == nil {
		return ErrSignatureInvalid
	}
	if err := v.signatures.Verify(ctx, path, expected.Publisher); err != nil {
		return err
	}
	return nil
}
