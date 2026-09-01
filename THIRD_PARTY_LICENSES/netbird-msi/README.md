# NetBird — Official Windows Installer (MSI)

| Field | Value |
|---|---|
| Component | Official NetBird Windows installer: `netbird_installer_0.74.7_windows_amd64.msi` |
| Source | https://github.com/netbirdio/netbird/releases/tag/v0.74.7 |
| Version | v0.74.7 |
| License | **AGPLv3** (NetBird upstream license; NetBird announced its switch to AGPLv3 in August 2025) |
| Copyright | NetBird GmbH (all upstream copyright and license notices preserved) |
| Location | `bin/` (git-ignored, downloaded at build time by `scripts/build-all.ps1`; packaged into the SoGame installer by `installer/sogame.iss`) |
| Usage | Distributed alongside SoGame; installed as a Windows system service on first use of Express Mode (via `sogame-helper.exe` with UAC elevation) |
| Modified | No — the official unmodified MSI is downloaded, verified (SHA256), and distributed |
| Distribution method | Bundled binary inside the SoGame installer (`installer/output/SoGame-Setup-*.exe`) |

## License Status

- The NetBird project changed its licensing over time (BSD-3-Clause /
  AGPLv3 directory exceptions → Business Source License 1.1 → AGPLv3 as
  announced in August 2025). Per the project decision, the v0.74.7 MSI
  is treated as **AGPLv3**.
- SoGame does **not** modify the MSI and does **not** relicense it.
  SoGame's AGPLv3 does not change NetBird's copyright: all NetBird
  copyright and license notices remain with NetBird GmbH.
- When distributing the MSI, comply with the AGPLv3 obligations that
  apply to it (including source-availability obligations for modified
  versions; the unmodified official MSI is distributed as-is).
- **Action for the maintainer:** when releasing, confirm the license
  text that upstream ships with the v0.74.7 MSI (e.g., an EULA or
  LICENSE file inside the installer) and preserve it alongside the
  binary. If upstream's v0.74.7 release notes state a different
  license, update this document accordingly.

## Integrity

`internal/releasebuild/netbird-release.json` records the expected
SHA256 (`1be9ce80767a728a8682bc3c114256b224b8d6657400ac031e458a05b5e5942d`)
and the NetBird GmbH publisher certificate data for the MSI. The build
script verifies the SHA256 before packaging.
