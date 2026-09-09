# Wintun (WireGuard TUN driver)

| Field | Value |
|---|---|
| Component | Wintun — the WireGuard project's TUN adapter kernel driver used by NetBird for the data plane |
| Source | https://git.zx2c4.com/wintun / https://github.com/WireGuard/wintun |
| Version | As bundled inside the official NetBird v0.74.7 MSI (version not separately pinned by SoGame) |
| License | MIT |
| Copyright | Copyright (c) 2018-2021 WireGuard contributors (upstream copyright and license notices preserved) |
| Usage | Installed as part of the official NetBird v0.74.7 MSI; provides the virtual adapter used by Express Mode (WireGuard tunnels) |
| Modified | No (SoGame does not distribute or modify the Wintun driver directly; it is contained in and installed by the NetBird MSI) |
| Distribution method | Included inside the NetBird MSI (`netbird_installer_0.74.7_windows_amd64.msi`), which is bundled in the SoGame installer |

## License Text

Wintun is licensed under the MIT license:

```
Copyright (c) 2018-2021 WireGuard contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Notes

- SoGame does not bundle Wintun directly; the driver ships inside the
  official NetBird MSI (see [../netbird-msi](../netbird-msi/README.md)).
  This entry documents the driver's MIT license for completeness.
- MIT is permissive and compatible with SoGame's AGPLv3 original code.
