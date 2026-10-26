# Validation — 0.1.0-beta.1

Local beta; not installed or activated on the donor speaker. A voice fix on the affected 2022 speaker is not yet confirmed.

Verified inputs:
- Recipient legacy image `0f40fec2...c3a907`: ARM32 maind/launcher, yiod/audiod launch layout.
- Working donor: ARM32 maind/launcher on an ARM64 kernel. CPU architecture does not require all apps to be AArch64.
- Nine external runtime libraries match byte-for-byte between that legacy image and the working donor. Vendor libraries, models and launch configuration are shipped as one pinned set.
- Export includes only `/system/vendor/quasar`, never the donor's `/data` profile, account, SSH keys or Wi-Fi configuration.

Executed checks:
- Linux ARM64 bootstrap and Windows/Linux AMD64 bundle servers compile with Go 1.26.5, CGO disabled.
- Ten Linux tests passed: archive traversal and link guards, profile isolation and size limits, LAN URL restrictions, PID identity, safe runtime directory selection, complete pinned archive extraction, watchdog EOF recovery, and termination/unmounting of a disposable chroot process.
- Windows bundle server returned a byte-identical SHA-256-verified archive; unrelated paths returned 404.
- Existing Homebrew CLI `check` accepted the packaged manifest, ARM64 native module and module SHA-256.
- Harmless probe on the working Mini verified tmpfs, read-only bind mounts and execution of ARM32 BusyBox inside chroot. Stock voice PIDs 15926/16013 and firmware hashes stayed unchanged. Probe files and mounts were removed.
- The first namespace probe was rejected by the device kernel; `/proc/config.gz` confirms CONFIG_NAMESPACES is disabled. Final implementation uses chroot, not clone namespaces. The full donor framework was not activated on the working speaker.

Real cloud replies, audio and account compatibility must be checked by the affected user before calling this a successful repair. A successful process-health check does not establish that the old assertion is fixed.
