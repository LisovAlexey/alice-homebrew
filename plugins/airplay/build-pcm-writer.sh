#!/usr/bin/env bash
# Build service/alice-pcm-armhf, the shared-mode PCM writer. It links against the
# firmware libasound (32-bit ARM, glibc 2.23), so the compiler matches that libc.
# Usage: bash plugins/airplay/build-pcm-writer.sh path/to/firmware/libasound.so.2
# Copy the library from /system/vendor/quasar/libs/libasound.so.2.0.0 on a Mini 1.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
lib=$(realpath "$1")
docker run --rm -v "$here":/w -v "$lib":/lib/firmware/libasound.so:ro -w /w ubuntu:16.04 bash -c '
set -e
apt-get update -qq >/dev/null
apt-get install -y -qq gcc-arm-linux-gnueabihf >/dev/null
arm-linux-gnueabihf-gcc -O2 -Wall -Wextra -o service/alice-pcm-armhf alice-pcm.c \
  -L/lib/firmware -lasound -Wl,--allow-shlib-undefined
arm-linux-gnueabihf-strip service/alice-pcm-armhf
chown '"$(id -u):$(id -g)"' service/alice-pcm-armhf'
sha256sum "$here/service/alice-pcm-armhf"
