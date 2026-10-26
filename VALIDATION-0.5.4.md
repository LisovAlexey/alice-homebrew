# Validation — Homebrew 0.5.4

Changes: preserve bounded diagnostic lines after a plugin exit, include them in status polling, update the open detail view without rerendering configuration fields, flush a final unterminated native line. A short failure stays diagnosable after manager restart.

New tests exercise a failing runner through Manager.Run, disk save/reload, Snapshot/Status and final native output. JavaScript validation runs the actual diagnostics update function against log/error/toggle elements and checks syntax.

Executed: full Windows `go test ./...`, complete Linux homebrew test binary in WSL (one pre-existing real-process updater fixture skipped because its optional old/next paths were not supplied), JavaScript syntax and diagnostics assertions, and Windows AMD64/Linux AMD64/Linux ARM64 production builds. The new native unterminated-line and persisted-failure cases passed. The built manager accepted the beta.2 package and its native-module hash.

WSL tests use a dedicated TMPDIR and wait for startup initialization; an earlier default /tmp run lost temporary files, and an early 20ms WASM fixture hit scheduler startup latency. No production execution deadlines or test assertions were relaxed. No physical speaker installation or firmware write was performed.
