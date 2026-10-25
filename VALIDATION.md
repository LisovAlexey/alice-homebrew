# Validation — 25 October 2026

- Go unit/integration tests cover schema rejection, archive paths, unsupported permissions/imports, WASM memory boundaries, timeout termination, function-table limits, local package consent, signed catalogs and pinned keys, immutable publication, updates, config migration, rollback, revocation persistence, authentication and Origin checks.
- The added fetch test proves that the service itself downloads a LAN package, checks its SHA-256 and still treats it as unreviewed. It checks that service credentials are not sent to the download host.
- The compiled Windows executable completed the full workflow using actual Ring Studio WASM: package → local moderation → signature → exported static Pages catalog in a subdirectory → service-side download → install → enable → animated frames → config changes.
- A separate personal package was served by HTTP and fetched/installed through `/api/fetch`, with explicit local-package consent. The station preview shows both official and personal packages with different review badges.
- Browser checks: login, generated fields, brightness slider, animation select, reverse switch, color input, config application and RU/EN. The static Pages preview verified its Ed25519 signature in the browser.
- Cross-built binaries: Windows AMD64, Linux AMD64, Linux ARM64. ARM64 is the architecture reported by the connected Mini 1.

Real Mini 1 hardware: installed the ARM64 service, verified unauthenticated station access (no admin-token.txt), loaded and ran the Ring Studio WASM module, checked successful I2C frame writes, and left the example disabled. Rebooted the speaker: the Homebrew init hook started the service, plugin state persisted, and HTTP access worked without login. The installer preserves stock Alice and other applications. No rooting tools or firmware are part of this repository.

The separate moderator service on the maintainer PC retains its own access code. Station management does not require one. Catalog signing keys stay outside source and release archives.
