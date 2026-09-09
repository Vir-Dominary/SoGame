# n2n (edge.exe)

| Field | Value |
|---|---|
| Component | n2n edge (classic-mode networking client) |
| Source | https://github.com/ntop/n2n |
| Version | **Undetermined** — the exact n2n version/build of `bin/edge.exe` is not recorded in the repository (see "Remaining human review") |
| License | GNU General Public License version 3 (GPLv3) |
| Copyright | ntop project and n2n contributors (all upstream copyright and license notices preserved) |
| Usage | Bundled standalone executable, packaged into the SoGame installer; invoked at runtime by `internal/n2n` for classic-mode room networking |
| Modified | No (binary only; no n2n source code is vendored) |
| Distribution method | Bundled binary inside the SoGame installer (`{app}\bin\edge.exe`) |

## License Status

- n2n is licensed under GPLv3. SoGame keeps n2n under GPLv3 and does
  **not** relicense it. In particular, n2n is **not** licensed under
  SoGame's AGPLv3, and SoGame's AGPLv3 copyright notice does not apply
  to n2n.
- n2n (GPLv3) is distributed as a **separate work** alongside SoGame's
  AGPLv3 code. This is permissible: AGPLv3 section 13 expressly permits
  combining AGPLv3-covered work with GPLv3-covered work, and the n2n
  binary is an independent executable invoked at runtime, not linked
  into SoGame.
- The full GPLv3 text is provided in the repository root `LICENSE`
  (identical license text; no separate copy is maintained here to avoid
  divergence).

## Remaining Human Review

- **Exact n2n version / build of `bin/edge.exe` is not recorded.** The
  file has no version metadata. The maintainer who produced the binary
  should record the exact upstream n2n version (e.g., n2n v3.x tag and
  build configuration) here, so that the GPLv3 source-availability
  obligations (Corresponding Source) for the distributed binary can be
  fulfilled on request.

## How to Update

When replacing `bin/edge.exe`:

1. Record the exact upstream tag/commit of n2n used to build it.
2. Record the build environment (compiler, target OS/arch).
3. Confirm the new binary is still GPLv3-licensed (check the upstream
   license if it changes).
