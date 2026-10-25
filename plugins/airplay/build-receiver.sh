#!/bin/bash
set -euo pipefail
BASE=$(cd "$(dirname "$0")/../.." && pwd)
WORK=${AIRPLAY_BUILD_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/alice-airplay-build}
mkdir -p "$WORK/sources" "$WORK/src" "$WORK/prefix"
if test -n "${AIRPLAY_SOURCE_DIR:-}"; then cp -u "$AIRPLAY_SOURCE_DIR"/* "$WORK/sources/"; fi
export LC_ALL=C
PREFIX=$WORK/prefix
export CC=aarch64-linux-gnu-gcc AR=aarch64-linux-gnu-ar RANLIB=aarch64-linux-gnu-ranlib STRIP=aarch64-linux-gnu-strip
export CFLAGS='-Os -ffunction-sections -fdata-sections'
export LDFLAGS="-L$PREFIX/lib -Wl,--gc-sections"
export CPPFLAGS="-I$PREFIX/include"
export PKG_CONFIG_LIBDIR=$PREFIX/lib/pkgconfig
export PKG_CONFIG_PATH=$PKG_CONFIG_LIBDIR
export PKG_CONFIG='pkg-config --static'
fetch(){ test -s "$WORK/sources/$1" || curl --fail --location --retry 3 "$2" -o "$WORK/sources/$1"; }
fetch alsa-lib-1.2.16.1.tar.bz2 https://www.alsa-project.org/files/pub/lib/alsa-lib-1.2.16.1.tar.bz2
fetch libconfig-1.8.3.tar.gz https://codeload.github.com/hyperrealm/libconfig/tar.gz/refs/tags/v1.8.3
fetch popt-1.19.tar.gz https://ftp.osuosl.org/pub/rpm/popt/releases/popt-1.x/popt-1.19.tar.gz
echo 'c25a4838fc8e4c1c8aacb8bd620edb3084a3d63bf8987fdad3ca2758c63240f9  sources/popt-1.19.tar.gz' | (cd "$WORK"; sha256sum -c -)
fetch openssl-3.5.6.tar.gz https://github.com/openssl/openssl/releases/download/openssl-3.5.6/openssl-3.5.6.tar.gz
fetch openssl-3.5.6.tar.gz.sha256 https://github.com/openssl/openssl/releases/download/openssl-3.5.6/openssl-3.5.6.tar.gz.sha256
test "$(sha256sum "$WORK/sources/openssl-3.5.6.tar.gz" | cut -d' ' -f1)" = "$(awk '{print $1}' "$WORK/sources/openssl-3.5.6.tar.gz.sha256")"
fetch ffmpeg-8.1.3.tar.xz https://ffmpeg.org/releases/ffmpeg-8.1.3.tar.xz
fetch shairport-sync-5.6.1.tar.gz https://codeload.github.com/mikebrady/shairport-sync/tar.gz/refs/tags/5.6.1
(cd "$WORK/sources"; sha256sum -c "$BASE/plugins/airplay/sources.sha256")
cd "$WORK/src"
for archive in "$WORK"/sources/*.tar.*; do
 name=$(basename "$archive"); name=${name%.tar.*}
 test -d "$name" || tar xf "$archive"
done
if ! test -f "$PREFIX/lib/libasound.a"; then
 cd "$WORK/src/alsa-lib-1.2.16.1"
 ./configure --host=aarch64-linux-gnu --prefix="$PREFIX" --disable-shared --enable-static --disable-python --disable-aload --disable-ucm --disable-topology --disable-rawmidi --disable-seq --disable-hwdep --disable-timer --disable-instr --disable-alisp --disable-old-symbols
 make -j4; make install
fi
if ! test -f "$PREFIX/lib/libconfig.a"; then
 cd "$WORK/src/libconfig-1.8.3"; autoreconf -fi
 ./configure --host=aarch64-linux-gnu --prefix="$PREFIX" --disable-shared --enable-static --disable-cxx --disable-examples
 make -j4 MAKEINFO=true; make install MAKEINFO=true
fi
if ! test -f "$PREFIX/lib/libpopt.a"; then
 cd "$WORK/src/popt-1.19"
 ./configure --host=aarch64-linux-gnu --prefix="$PREFIX" --disable-shared --enable-static --disable-nls
 make -j4; make install
fi
if ! test -f "$PREFIX/lib/libssl.a"; then
 cd "$WORK/src/openssl-3.5.6"
 ./Configure linux-aarch64 --prefix="$PREFIX" --libdir=lib no-shared no-tests no-module no-apps no-engine no-dso no-zlib no-comp
 make -j4; make install_sw
fi
if ! test -f "$PREFIX/lib/libavcodec.a"; then
 cd "$WORK/src/ffmpeg-8.1.3"
 ./configure --prefix="$PREFIX" --arch=aarch64 --target-os=linux --cross-prefix=aarch64-linux-gnu- --enable-cross-compile --disable-shared --enable-static --disable-everything --disable-programs --disable-doc --disable-network --disable-autodetect --disable-avdevice --disable-avfilter --disable-swscale --enable-decoder=alac,pcm_s16le,pcm_s16be --enable-swresample --enable-small --extra-cflags="$CFLAGS"
 make -j4; make install
fi
cd "$WORK/src/shairport-sync-5.6.1"
cp "$BASE/plugins/airplay/mdns_external.c" mdns_external.c
autoreconf -fi
./configure --host=aarch64-linux-gnu --prefix="$PREFIX" --with-alsa --with-stdout --with-dummy --with-external-mdns --with-ssl=openssl --with-ffmpeg --without-libdaemon LDFLAGS="$LDFLAGS -static" LIBS='-lpthread -lm -ldl'
make -j4
$STRIP shairport-sync
cp shairport-sync "$BASE/plugins/airplay/service/receiver-arm64"
file shairport-sync
echo AIRPLAY_BUILD_OK
