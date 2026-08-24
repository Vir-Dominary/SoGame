// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 SoGame Contributors
//
// This file is part of SoGame.
//
// SoGame is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// SoGame is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with SoGame. If not, see <https://www.gnu.org/licenses/>.

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
