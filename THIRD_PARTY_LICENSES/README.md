# Third-Party Licenses

This directory documents the third-party software that SoGame includes,
bundles, links, or invokes. Each component remains subject to its own
copyright and license terms, which are independent of the AGPLv3
license that applies to SoGame's original code.

**Important:** The root `LICENSE` file (AGPLv3) applies **only to
SoGame's original code**. It does not relicense any third-party
component. Third-party software keeps its own licenses; SoGame does not
modify third-party license texts or copyright notices.

This directory is the entry point for the project's third-party license
inventory. It is maintained as part of the SoGame release process: when
third-party components are added, upgraded, or removed, this inventory
must be updated accordingly.

## Inventory

| Directory / File | Component | License | Status |
|---|---|---|---|
| [netbird-rpc/](netbird-rpc/README.md) | NetBird daemon RPC contract (daemon.proto / daemon.pb.go / daemon_grpc.pb.go), vendored | BSD 3-Clause | Vendored source (unchanged) |
| [netbird-msi/](netbird-msi/README.md) | Official NetBird v0.74.7 Windows installer (MSI) | AGPLv3 (NetBird upstream) | Bundled binary (downloaded at build time) |
| [n2n/](n2n/README.md) | n2n edge.exe (classic-mode networking) | GPLv3 | Bundled binary |
| [tap-windows/](tap-windows/README.md) | TAP-Windows Adapter V9 driver (tap0901.sys, tap0901.cat, OemWin2k.inf) and tapinstall.exe | GPLv2 | Bundled binary + INF |
| [wintun/](wintun/README.md) | Wintun driver (inside the NetBird MSI) | MIT | Included in NetBird MSI |
| [go-modules/](go-modules/README.md) | Go module dependencies (built into SoGame.exe and room-api) | MIT / BSD / Apache-2.0 / ISC | Linked dependencies |
| [npm/](npm/README.md) | npm / frontend build dependencies (React, Vite, etc.) | MIT / ISC / BSD / Apache-2.0 / CC-BY-4.0 | Build-time dependencies |

## Important Notes

1. **Third-party licenses are not modified by this project.** SoGame
   does not relicense third-party code, and it does not change or remove
   third-party copyright notices or license texts.

2. **The AGPLv3 in the root LICENSE file applies only to SoGame's
   original code**, not to the third-party components listed here. Do
   not assume that a component is AGPLv3 just because the project
   overall is AGPLv3.

3. **Vendored source** (the NetBird RPC contract under
   `internal/netbird/rpc/`) is copied unchanged from its upstream and
   keeps its own BSD 3-Clause license and attribution files.

4. **Bundled binaries** (edge.exe, TAP-Windows driver, tapinstall.exe,
   NetBird MSI) are distributed as separate works alongside SoGame.
   Their licenses and attribution requirements must be preserved when
   SoGame is redistributed. The SoGame installer packages this
   directory so that users of the binary release can access the
   third-party license information.

5. **Linked/build dependencies** (Go modules, npm packages) are governed
   by their own licenses. The permissive licenses used by the current
   dependency set are compatible with AGPLv3, but this must be
   re-checked whenever dependencies change.

6. **License compatibility summary:**
   - NetBird MSI (AGPLv3) and n2n (GPLv3) are both copyleft but are
     distributed as **separate works** alongside SoGame's AGPLv3 code.
     AGPLv3 section 13 expressly permits combining AGPLv3-covered work
     with GPLv3-covered work.
   - TAP-Windows / tapinstall (GPLv2) are separate kernel driver /
     installer tools, not merged into SoGame's code. They keep GPLv2
     and are **not** converted to GPLv3 or AGPLv3.
   - MIT / BSD / Apache-2.0 / ISC dependencies are permissive and
     compatible with AGPLv3.

## How to Update

When adding or changing a third-party component:

1. Record the component here and in the corresponding subdirectory.
2. Keep the exact upstream license text and copyright notice.
3. Record the exact version / tag / commit the component came from.
4. Note whether the component was modified, and if so, what was changed
   and when.
5. Re-run the project's license audit checklist (see the final
   consistency check in the migration report).
