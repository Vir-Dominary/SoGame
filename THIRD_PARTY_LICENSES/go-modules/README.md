# Go Module Dependencies

SoGame's Go backend (`go.mod`) and the Room API server
(`server/room-api/go.mod`) link third-party Go modules. These modules
are downloaded at build time from their upstream repositories; no
third-party Go source is vendored into this repository (the only
vendored third-party source is the NetBird RPC contract — see
[../netbird-rpc](../netbird-rpc/README.md)).

The current dependency set is licensed under permissive licenses
(MIT / BSD / Apache-2.0 / ISC), all of which are compatible with
AGPLv3. **This must be re-checked whenever dependencies change.**

## Main module (`go.mod`)

| Module | Version | License |
|---|---|---|
| github.com/wailsapp/wails/v2 | v2.12.0 | MIT |
| golang.org/x/sys | v0.45.0 | BSD-3-Clause |
| google.golang.org/grpc | v1.80.0 | Apache-2.0 |
| google.golang.org/protobuf | v1.36.11 | BSD-3-Clause |
| gopkg.in/yaml.v3 | v3.0.1 | MIT / Apache-2.0 |

## Indirect dependencies (main module, subset of `go.sum`)

| Module | License |
|---|---|
| git.sr.ht/~jackmordaunt/go-toast/v2 | MIT |
| github.com/bep/debounce | MIT |
| github.com/cespare/xxhash/v2 | MIT |
| github.com/davecgh/go-spew | ISC |
| github.com/godbus/dbus/v5 | BSD-2-Clause |
| github.com/golang/protobuf | BSD-3-Clause |
| github.com/go-logr/logr, go-logr/stdr | Apache-2.0 |
| github.com/google/go-cmp | BSD-3-Clause |
| github.com/google/uuid | BSD-3-Clause |
| github.com/go-ole/go-ole | MIT |
| github.com/gorilla/websocket | BSD-2-Clause |
| github.com/jchv/go-winloader | MIT |
| github.com/labstack/echo/v4 | MIT |
| github.com/labstack/gommon | MIT |
| github.com/leaanthony/debme, go-ansi-parser, gosod, slicer, u | MIT |
| github.com/matryer/is | MIT |
| github.com/mattn/go-colorable, go-isatty | MIT |
| github.com/pkg/browser | MIT |
| github.com/pkg/errors | BSD-2-Clause |
| github.com/pmezard/go-difflib | BSD-3-Clause |
| github.com/rivo/uniseg | MIT |
| github.com/samber/lo | MIT |
| github.com/stretchr/testify | MIT |
| github.com/tkrajina/go-reflector | MIT |
| github.com/valyala/bytebufferpool, fasttemplate | MIT |
| github.com/wailsapp/go-webview2, mimetype | MIT |
| go.opentelemetry.io/auto/sdk, otel, otel/metric, otel/sdk, otel/sdk/metric, otel/trace | Apache-2.0 |
| golang.org/x/crypto, x/net, x/term, x/text, x/tools | BSD-3-Clause |
| gonum.org/v1/gonum | BSD-3-Clause |
| google.golang.org/genproto/googleapis/rpc | Apache-2.0 |
| gopkg.in/check.v1 | BSD-2-Clause |

## Room API server (`server/room-api/go.mod`)

| Module | Version | License |
|---|---|---|
| github.com/mattn/go-sqlite3 | v1.14.24 | MIT |

## Compatibility check

- No GPL / LGPL / AGPL / BSL / SSPL / MPL / EPL licensed module was
  found in the current dependency set. All licenses are permissive and
  compatible with SoGame's AGPLv3.
- If a future dependency introduces a copyleft license (GPL/LGPL/AGPL)
  or a source-available license (BSL/SSPL), stop and review
  compatibility before adding it. Do not assume compatibility.

## Notes

- The authoritative license texts live in each upstream module; when a
  license requires notice preservation in distributions, the module's
  own LICENSE/NOTICE files apply.
- `go.sum` entries are hashes only; they do not affect licensing.
- The complete authoritative inventory at any given time is
  `go mod graph` / `go list -m all` output; regenerate this table when
  dependencies change.
