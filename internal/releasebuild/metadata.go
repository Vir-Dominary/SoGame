package releasebuild

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed netbird-release.json
var releaseFS embed.FS

type Metadata struct {
	SchemaVersion int             `json:"schemaVersion"`
	Version       string          `json:"version"`
	ServerImage   string          `json:"serverImage"`
	PackagingMode string          `json:"packagingMode"`
	HelperSHA256  string          `json:"helperSha256,omitempty"`
	WindowsX64    WindowsArtifact `json:"windowsX64"`
}

type WindowsArtifact struct {
	Artifact  string            `json:"artifact"`
	URL       string            `json:"url"`
	Size      int64             `json:"size"`
	SHA256    string            `json:"sha256"`
	Publisher Publisher         `json:"publisher"`
	Install   InstallProperties `json:"install"`
}

type Publisher struct {
	SubjectCommonName              string `json:"subjectCommonName"`
	Organization                   string `json:"organization"`
	CertificateThumbprintAtRelease string `json:"certificateThumbprintAtRelease"`
}

type InstallProperties struct {
	ProductCode string   `json:"productCode"`
	Executable  string   `json:"executable"`
	Arguments   []string `json:"arguments"`
}

func Load() (Metadata, error) {
	data, err := releaseFS.ReadFile("netbird-release.json")
	if err != nil {
		return Metadata{}, fmt.Errorf("read embedded NetBird release metadata: %w", err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("parse embedded NetBird release metadata: %w", err)
	}
	if metadata.SchemaVersion != 1 || metadata.Version == "" || metadata.WindowsX64.SHA256 == "" {
		return Metadata{}, fmt.Errorf("invalid embedded NetBird release metadata")
	}
	return metadata, nil
}
