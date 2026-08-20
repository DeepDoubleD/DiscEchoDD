# syntax=docker/dockerfile:1.7

###############################################################################
# Stage 1 — build the SvelteKit UI
###############################################################################
FROM node:20-bookworm-slim AS webui-build
WORKDIR /webui
RUN corepack enable && corepack prepare pnpm@9 --activate
COPY webui/package.json webui/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY webui/ ./
RUN pnpm build

###############################################################################
# Stage 2 — build the Go daemon with the UI embedded
###############################################################################
FROM golang:1.25-bookworm AS daemon-build
WORKDIR /src
COPY daemon/go.mod daemon/go.sum ./daemon/
WORKDIR /src/daemon
RUN go mod download
COPY daemon/ ./
# Drop the placeholder UI and replace with the real build from stage 1.
RUN rm -rf embed/webui_build
COPY --from=webui-build /webui/build ./embed/webui_build
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags "-s -w \
      -X github.com/jumpingmushroom/DiscEcho/daemon/version.Version=${VERSION} \
      -X github.com/jumpingmushroom/DiscEcho/daemon/version.Commit=${COMMIT} \
      -X github.com/jumpingmushroom/DiscEcho/daemon/version.BuildDate=${BUILD_DATE}" \
    -o /out/discecho ./cmd/discecho

###############################################################################
# Stage 3 — build MakeMKV from source
#
# MakeMKV has no apt package and depends on heavy build-time toolchains
# (qtbase5-dev, libgl1-mesa-dev) that we don't want shipped in the
# runtime image. We compile it in this isolated stage and the runtime
# stage copies only the resulting binary + shared libs.
###############################################################################
# Pulled from jlesage/makemkv rather than built from source. Our previous
# from-source build linked makemkvcon against Debian bookworm's libcrypto3
# / libssl3 / libavcodec59. The resulting binary saved + loaded purchased
# `M-` registration keys correctly (verified byte-perfect on disk) but
# MakeMKV's internal signature verification still rejected them with
# MSG:5021 ("application version is too old"). The same key + same upstream
# version (v1.18.3) worked on the user's desktop, so the divergence was in
# the build/linkage. jlesage/makemkv bundles its own complete library tree
# (including its own dynamic linker at /opt/makemkv/lib/ld-linux-x86-64.so.2)
# matched to MakeMKV's expectations, so we adopt that pre-built bundle
# wholesale.
FROM jlesage/makemkv:latest AS makemkv-build

###############################################################################
# Stage — build HandBrakeCLI from source on Debian bookworm
#
# Debian bookworm's `handbrake-cli` package (1.6.1) is compiled without
# NVENC support. We build HandBrake from source so the resulting binary
# links against bookworm's own libraries (no cross-distro ABI issues)
# and is compiled with --enable-nvenc (on by default for x86_64-linux).
# NVENC requires only the nv-codec-headers at build time; no GPU is
# needed during the build — the runtime driver is dlopen'd at job start.
###############################################################################
FROM debian:bookworm-slim AS handbrake-build
ARG HANDBRAKE_VERSION=1.11.1
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        build-essential cmake git nasm ninja-build meson m4 patch pkg-config \
        python3 tar curl ca-certificates \
        libtool libtool-bin autoconf automake \
        libass-dev libbz2-dev libfontconfig-dev libfreetype6-dev \
        libfribidi-dev libharfbuzz-dev libjansson-dev liblzma-dev \
        libmp3lame-dev libnuma-dev libogg-dev libopus-dev \
        libsamplerate0-dev libspeex-dev libtheora-dev \
        libturbojpeg0-dev libvorbis-dev libx264-dev libxml2-dev \
        libvpx-dev libdvdread-dev libdvdnav-dev libbluray-dev \
        libva-dev libdrm-dev \
        zlib1g-dev \
 && curl -fsSL "https://github.com/HandBrake/HandBrake/releases/download/${HANDBRAKE_VERSION}/HandBrake-${HANDBRAKE_VERSION}-source.tar.bz2" \
        | tar xj -C /tmp \
 && cd "/tmp/HandBrake-${HANDBRAKE_VERSION}" \
 && ./configure --disable-gtk --launch-jobs="$(nproc)" --launch \
 && make --directory=build install \
 && rm -rf /tmp/HandBrake*

###############################################################################
# Stage — build chdman from MAME source
#
# Debian bookworm's mame-tools package ships chdman 0.251, which is missing
# the `createdvd` subcommand (added in MAME 0.252, April 2023). PS2 / Xbox
# rips use createdvd to produce DVD-typed CHD files that emulators expect.
# We build MAME's tools subset here (TOOLS=1 EMULATOR=0 skips the full
# emulator and its Qt deps) and copy out just the chdman binary.
# MAME does not ship source tarballs in its GitHub releases; we shallow-clone
# the tagged commit instead.
###############################################################################
FROM debian:bookworm-slim AS chdman-build
ARG MAME_VERSION=0.275
ARG MAME_TAG=mame0275
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        build-essential git python3 ca-certificates \
        libsdl2-dev libsdl2-ttf-dev \
        libxinerama-dev libxi-dev \
        libfontconfig-dev libasound2-dev \
 && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch "${MAME_TAG}" \
        https://github.com/mamedev/mame.git /src/mame
WORKDIR /src/mame
# USE_QTDEBUG=0: on Linux the Genie build system defaults USE_QTDEBUG=1
# which requires Qt5Widgets headers and moc. We have no use for the Qt
# debugger UI in a CHD-tools-only build, so disable it explicitly.
RUN make -j"$(nproc)" TOOLS=1 EMULATOR=0 USE_QTDEBUG=0 IGNORE_GIT=1
RUN strip /src/mame/chdman \
 && /src/mame/chdman --help 2>&1 | head -5

###############################################################################
# Stage — build loudgain from source
#
# loudgain (https://github.com/Moonbase59/loudgain) is the audiophile-grade
# EBU R128 ReplayGain 2.0 tagger DiscEcho uses for the audio-CD post-rip
# pass. It is not packaged in Debian bookworm (apt-cache returns nothing
# in either main or contrib), so we build it from a tagged release here
# and copy only the resulting binary into the runtime image. Runtime
# shared-lib deps (libebur128 + libavformat/swresample/avutil) are
# installed via apt in the runtime stage; this stage adds the matching
# -dev headers + cmake for the build.
###############################################################################
FROM debian:bookworm-slim AS loudgain-build
ARG LOUDGAIN_VERSION=v0.6.8
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        build-essential cmake pkg-config git ca-certificates \
        libavformat-dev libavutil-dev libswresample-dev \
        libebur128-dev libtag1-dev \
 && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch "${LOUDGAIN_VERSION}" \
        https://github.com/Moonbase59/loudgain.git /src/loudgain
# Patch out the av_register_all() call in scan.c. loudgain v0.6.8 wraps
# it in a runtime version-check `if`, but the linker still needs the
# symbol — and FFmpeg 5.x (Debian bookworm) removed it entirely. The
# version-check has been "always false" since lavf 58.9.100 (FFmpeg 4.0,
# 2018), so replacing the call with a no-op is functionally equivalent
# and lets the link succeed against modern libavformat.
RUN sed -i 's/av_register_all();/(void)0;/' /src/loudgain/src/scan.c
WORKDIR /src/loudgain/build
RUN cmake -DCMAKE_BUILD_TYPE=Release .. \
 && make -j"$(nproc)" \
 && ( find . -maxdepth 3 -type f -name 'loudgain' -executable -exec cp {} /usr/local/bin/loudgain \; ) \
 && strip /usr/local/bin/loudgain \
 && /usr/local/bin/loudgain --version

###############################################################################
# Stage 4 — runtime: python slim + apprise + the daemon binary
###############################################################################
FROM python:3.12-slim-bookworm AS runtime
# whipper is not on PyPI, so install it from Debian apt. cdparanoia +
# libcdio-utils provide the lower-level rippers and cd-info that
# identify/classify use. libdvd-pkg + dvdbackup + genisoimage
# provide DVD ripping (libdvdcss CSS bypass, isoinfo for volume-label
# probe). HandBrakeCLI itself comes from the handbrake-build stage
# below — the Debian package lacks NVENC support. libdvd-pkg lives in
# Debian's `contrib` archive, which the python:slim base doesn't enable
# by default. libbluray-bin ships bd_info (UHD AACS2 detection).
# makemkvcon ships with its own bundled libraries under /opt/makemkv/lib/
# (its own ld-linux + libcrypto3 + libssl3 + libexpat + libavcodec) so
# the runtime image no longer needs to provide them on its behalf.
# libass9 + libturbojpeg0 are HandBrakeCLI runtime deps not pulled in
# transitively by anything else in this image. libsdl2-2.0-0 is the
# sole runtime dep of the chdman binary built from MAME source in the
# chdman-build stage above (chdman links ocore_sdl).
RUN echo "deb http://deb.debian.org/debian bookworm main contrib" \
        > /etc/apt/sources.list.d/contrib.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends \
        ca-certificates eject udev cdparanoia libcdio-utils whipper \
        python3-cdio \
        flac \
        libdvd-pkg dvdbackup genisoimage \
        gddrescue \
        vcdimager \
        libbluray-bdj libbluray2 libbluray-bin \
        libass9 libturbojpeg0 \
        libsdl2-2.0-0 \
        libebur128-1 libavformat59 libswresample4 libavutil57 libtag1v5 \
 && DEBIAN_FRONTEND=noninteractive dpkg-reconfigure libdvd-pkg \
 && rm -rf /var/lib/apt/lists/* \
 && pip install --no-cache-dir apprise

# Copy the entire MakeMKV install tree from jlesage's image. The bundle
# at /opt/makemkv/{bin,lib} is self-contained (its own ld-linux,
# libcrypto, libssl, libexpat, libavcodec). Symlink the entry point
# to /usr/bin/makemkvcon so the daemon's exec.Command resolution via
# PATH keeps working unchanged.
COPY --from=makemkv-build /opt/makemkv /opt/makemkv
RUN ln -sf /opt/makemkv/bin/makemkvcon /usr/bin/makemkvcon

# mmgplsrv (MakeMKV's GPL/FFmpeg helper, forked by makemkvcon during a rip)
# is a *musl* binary in jlesage's Alpine bundle (interpreter
# /lib/ld-musl-x86_64.so.1), unlike makemkvcon which is glibc and uses the
# bundled /opt/makemkv/lib loader. Its musl loader + musl-built support libs
# live OUTSIDE /opt/makemkv, so the COPY above doesn't bring them and the
# glibc runtime has no musl loader at all — exec fails with MSG "Failed to
# execute external program 'mmgplsrv'". Pull the three artifacts it needs
# (DT_NEEDED: libc.musl, libstdc++.so.6, libgcc_s.so.1) from the same jlesage
# image. Isolate the support libs under /opt/makemkv/musl via
# /etc/ld-musl-x86_64.path so they can't shadow the glibc libstdc++/libgcc the
# rest of the runtime (HandBrake, loudgain, chdman) uses — Debian usrmerge
# makes /lib == /usr/lib, so a plain copy would collide.
COPY --from=makemkv-build /lib/ld-musl-x86_64.so.1 /lib/ld-musl-x86_64.so.1
COPY --from=makemkv-build /usr/lib/libstdc++.so.6 /usr/lib/libgcc_s.so.1 /opt/makemkv/musl/
RUN ln -sf /lib/ld-musl-x86_64.so.1 /opt/makemkv/musl/libc.musl-x86_64.so.1 \
 && printf '/opt/makemkv/musl\n' > /etc/ld-musl-x86_64.path \
 # Guard against future jlesage drift: mmgplsrv must actually launch. An exec
 # failure (missing interpreter/lib) yields 127; timeout because it's a server
 # that blocks on its control pipe. Any non-127 exit means it loaded fine.
 && sh -c 'timeout 5 /opt/makemkv/bin/mmgplsrv >/dev/null 2>&1; [ "$?" -ne 127 ]'

# HandBrake built from source on Debian bookworm. The binary links
# against the same bookworm shared libs already present in the runtime
# image, so no extra lib COPYs are needed.
COPY --from=handbrake-build /usr/local/bin/HandBrakeCLI /usr/bin/HandBrakeCLI

# chdman built from MAME source (see chdman-build stage). Replaces the
# bookworm mame-tools package (0.251) which predates the createdvd
# subcommand needed for PS2 / Xbox DVD-typed CHDs.
COPY --from=chdman-build /src/mame/chdman /usr/bin/chdman

# loudgain built from source (see loudgain-build stage). Not in Debian.
# Used for audio-CD post-rip ReplayGain 2.0 album-mode tagging.
COPY --from=loudgain-build /usr/local/bin/loudgain /usr/bin/loudgain

# redumper — pre-built static Linux binary released on GitHub.
# Pinned via REDUMPER_VERSION build arg.
ARG REDUMPER_VERSION=b720
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl unzip ca-certificates \
 && curl -fsSLo /tmp/redumper.zip \
        "https://github.com/superg/redumper/releases/download/${REDUMPER_VERSION}/redumper-${REDUMPER_VERSION}-linux-x64.zip" \
 && unzip /tmp/redumper.zip -d /tmp/redumper \
 && install -m 0755 /tmp/redumper/redumper-${REDUMPER_VERSION}-linux-x64/bin/redumper /usr/local/bin/redumper \
 && apt-get purge -y --auto-remove curl unzip \
 && rm -rf /var/lib/apt/lists/* /tmp/redumper /tmp/redumper.zip

WORKDIR /app
COPY --from=daemon-build /out/discecho /app/discecho

ENV DISCECHO_ADDR=":8088" \
    DISCECHO_LIBRARY="/library" \
    DISCECHO_DATA="/var/lib/discecho"

EXPOSE 8088
USER root
ENTRYPOINT ["/app/discecho"]
