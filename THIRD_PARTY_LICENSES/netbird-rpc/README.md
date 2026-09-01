# NetBird — daemon RPC contract (vendored source)

| Field | Value |
|---|---|
| Component | NetBird daemon gRPC contract: `client/proto/daemon.proto`, `daemon.pb.go`, `daemon_grpc.pb.go` |
| Source | https://github.com/netbirdio/netbird |
| Version | v0.74.7 |
| Commit | `a1c9427d8004576e2cbb9e546d409847fa9df318` |
| Location in repo | `internal/netbird/rpc/` |
| Usage | Vendored generated RPC/protobuf code, imported by SoGame's gRPC adapter (`internal/netbird`) to talk to the official NetBird daemon on `127.0.0.1:41731` |
| Modified | No — copied byte-for-byte from upstream; see `internal/netbird/rpc/README.md` |
| License | BSD 3-Clause (see `internal/netbird/rpc/LICENSE.netbird`) |
| Copyright | Copyright (c) 2022 NetBird GmbH & AUTHORS |
| Distribution method | Vendored source within the SoGame source repository (not bundled into the binary installer) |

## License Text

The BSD 3-Clause license text that applies to the vendored files is
preserved at `internal/netbird/rpc/LICENSE.netbird` and copied below for
reference.

```
BSD 3-Clause License

Copyright (c) 2022 NetBird GmbH & AUTHORS

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice,
   this list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its contributors
   may be used to endorse or promote products derived from this software
   without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE
LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR
CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS
INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE)
ARISING IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
POSSIBILITY OF SUCH DAMAGE.
```

## Notes

- This is the **only third-party source code vendored into the SoGame
  repository**. It is governed by BSD 3-Clause and is **not** relicensed
  under SoGame's AGPLv3. SoGame's AGPLv3 license does not apply to these
  files.
- These files are **not** modified by SoGame, and their upstream
  copyright and license notices are preserved.
- On a coordinated NetBird upgrade, replace the three contract files
  from the new exact tag, update this attribution and
  `internal/netbird/rpc/README.md`, and re-run the adapter contract
  tests before changing the server or packaged daemon version.
