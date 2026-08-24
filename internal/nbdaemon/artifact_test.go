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
	"os"
	"path/filepath"
	"testing"

	releasebuild "sogame/internal/releasebuild"
)

type fakeSignatureVerifier struct{ err error }

func (f fakeSignatureVerifier) Verify(context.Context, string, releasebuild.Publisher) error {
	return f.err
}

func TestArtifactVerifier(t *testing.T) {
	content := []byte("official artifact fixture")
	digest := sha256.Sum256(content)
	expected := releasebuild.WindowsArtifact{Size: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	path := filepath.Join(t.TempDir(), "netbird.msi")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		path      string
		expected  releasebuild.WindowsArtifact
		signature SignatureVerifier
		want      error
	}{
		{name: "valid", path: path, expected: expected, signature: fakeSignatureVerifier{}},
		{name: "missing", path: filepath.Join(t.TempDir(), "missing.msi"), expected: expected, signature: fakeSignatureVerifier{}, want: ErrArtifactMissing},
		{name: "size mismatch", path: path, expected: releasebuild.WindowsArtifact{Size: 1, SHA256: expected.SHA256}, signature: fakeSignatureVerifier{}, want: ErrArtifactSize},
		{name: "digest mismatch", path: path, expected: releasebuild.WindowsArtifact{Size: expected.Size, SHA256: "00"}, signature: fakeSignatureVerifier{}, want: ErrArtifactDigest},
		{name: "unsigned", path: path, expected: expected, signature: fakeSignatureVerifier{err: ErrSignatureInvalid}, want: ErrSignatureInvalid},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewArtifactVerifier(test.signature).Verify(context.Background(), test.path, test.expected)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestArtifactVerifierRejectsTamperedBytes(t *testing.T) {
	original := []byte("official artifact fixture")
	digest := sha256.Sum256(original)
	expected := releasebuild.WindowsArtifact{Size: int64(len(original)), SHA256: hex.EncodeToString(digest[:])}

	tampered := append([]byte(nil), original...)
	tampered[len(tampered)/2] ^= 0xff
	path := filepath.Join(t.TempDir(), "tampered.msi")
	if err := os.WriteFile(path, tampered, 0600); err != nil {
		t.Fatal(err)
	}

	err := NewArtifactVerifier(fakeSignatureVerifier{}).Verify(context.Background(), path, expected)
	if !errors.Is(err, ErrArtifactDigest) {
		t.Fatalf("tampered artifact returned %v, want digest error", err)
	}
}
