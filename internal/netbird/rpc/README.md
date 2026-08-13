# Vendored NetBird daemon RPC contract

These files are copied unchanged from the official `netbirdio/netbird` tag
`v0.74.7`, commit `a1c9427d8004576e2cbb9e546d409847fa9df318`:

- `client/proto/daemon.proto`
- `client/proto/daemon.pb.go`
- `client/proto/daemon_grpc.pb.go`

Upstream generated the Go bindings with `protoc-gen-go v1.36.6` and
`protoc-gen-go-grpc v1.6.1`. The generated package contains the complete single
daemon service contract because protobuf descriptors and stream indexes must
remain byte-for-byte compatible. The Sogame adapter imports only the methods
listed in `contract_test.go`; no NetBird networking implementation is vendored.

`LICENSE.netbird` is the upstream BSD 3-Clause license that applies to the
client contract. On a coordinated NetBird upgrade, replace all three contract
files from the new exact tag, update this attribution, and rerun adapter contract
tests before changing the server or packaged daemon version.
