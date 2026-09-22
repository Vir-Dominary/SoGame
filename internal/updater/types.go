package updater

type UpdateInfo struct {
	HasUpdate      bool   `json:"hasUpdate"`
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion"`
	DownloadURL    string `json:"downloadUrl,omitempty"`
	Sha256         string `json:"sha256,omitempty"`
	Size           int64  `json:"size,omitempty"`
	ReleaseNotes   string `json:"releaseNotes,omitempty"`
	MinVersion     string `json:"minVersion,omitempty"`
	Error          string `json:"error,omitempty"`
}

type remoteManifest struct {
	Version       string `json:"version"`
	DownloadURL   string `json:"downloadUrl"`
	Sha256        string `json:"sha256"`
	Size          int64  `json:"size"`
	ReleaseNotes  string `json:"releaseNotes"`
	MinVersion    string `json:"minVersion"`
}
