# Ring Studio

Example plugin for Alice Homebrew API 1. Requires only `led.ring`.
Solid, breathing, rainbow and chase effects are generated inside WebAssembly.
The station validates every 36-byte BGR frame and allows one frame per tick.
On a PC the frame is previewed in the web interface; hardware writes require
the station service's explicit `--hardware-led` option.

Source: `plugin.c`. Build with `scripts/build-example.sh`, then package with
`alice-homebrew pack examples/ring-studio RingStudio-1.0.0.aliceplugin`.
