# npm / Frontend Build Dependencies

SoGame's frontend (`frontend/package.json`, `frontend/package-lock.json`)
uses npm packages to build the React UI. These are build-time
dependencies downloaded from the npm registry; their compiled output is
bundled into the frontend assets by Vite.

The current dependency set is licensed under permissive licenses
(MIT / ISC / BSD-3-Clause / Apache-2.0 / CC-BY-4.0 for a data package),
all compatible with AGPLv3. **This must be re-checked whenever
dependencies change.**

## Direct dependencies

| Package | Version | License |
|---|---|---|
| react | 18.3.1 | MIT |
| react-dom | 18.3.1 | MIT |
| @vitejs/plugin-react | 4.7.0 | MIT |
| vite | 5.4.21 | MIT |

## Notable transitive dependencies

| Package | Version | License |
|---|---|---|
| @babel/* (code-frame, core, parser, traverse, types, etc.) | 7.x | MIT |
| @jridgewell/* | 0.3.x / 1.x | MIT |
| @rolldown/pluginutils | 1.0.0-beta.27 | MIT |
| @rollup/rollup-* (platform binaries) | 4.60.2 | MIT |
| @types/* (babel, estree) | various | MIT |
| browserslist | 4.28.2 | MIT |
| caniuse-lite | 1.0.30001791 | CC-BY-4.0 (data) |
| esbuild | 0.21.5 | MIT |
| electron-to-chromium | 1.5.349 | ISC |
| fsevents | 2.3.3 | MIT |
| nanoid | 3.3.12 | MIT |
| postcss | 8.5.13 | MIT |
| rollup | 4.60.2 | MIT |
| scheduler | 0.23.2 | MIT |
| source-map-js | 1.2.1 | BSD-3-Clause |

Full list (names, versions, declared licenses) is available from
`frontend/package-lock.json` (`packages` section). The licenses recorded
there are the declared licenses of each package.

## Compatibility check

- No GPL / LGPL / AGPL / BSL / SSPL / MPL / EPL licensed package was
  found in the current dependency set. All licenses are permissive and
  compatible with SoGame's AGPLv3.
- If a future dependency introduces a copyleft or source-available
  license, stop and review compatibility before adding it. Do not
  assume compatibility.

## Notes

- `caniuse-lite` is distributed under CC-BY-4.0; it is a **data**
  package (browser-support data) used at build time and is not part of
  SoGame's original code. CC-BY-4.0 is a data/attribution license, not
  a software license; it does not impose software obligations on SoGame.
- License texts live in each package's own LICENSE file under
  `frontend/node_modules/`; npm install regenerates them.
- Generated bundles under `frontend/dist/` and the Wails-generated
  bindings under `frontend/wailsjs/` are build outputs, not third-party
  code.
