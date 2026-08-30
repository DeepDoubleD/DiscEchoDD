# Third-party code

DiscEcho itself is released under the [MIT License](./LICENSE). It shells
out to a number of external tools (MakeMKV, HandBrake, redumper, chdman,
whipper, ffmpeg, Apprise, [wudecrypt](https://github.com/maki-chan/wudecrypt),
etc.) at runtime — those are not vendored and keep their own licenses; see
each project for details.

wudecrypt in particular is AGPL-3.0-licensed. DiscEcho builds it from
source into its own binary in a dedicated Docker stage and invokes it as a
separate process (never compiled or linked into DiscEcho's own Go binary),
the same "external tool" pattern already used for HandBrake/whipper/ffmpeg
above — all of which are themselves copyleft- or proprietary-licensed.
wudecrypt's license stays scoped to that one binary. See
`daemon/pipelines/wiiu`'s package doc for the full reasoning, including why
no clean-room reimplementation was pursued instead.

One piece of third-party source is vendored directly into this repository:

## daemon/internal/thirdparty/ps3-disc-dumper/

Core dumping/decryption logic from
[13xforever/ps3-disc-dumper](https://github.com/13xforever/ps3-disc-dumper)
(`Ps3DiscDumper/` and `IrdLibraryClient/`), MIT-licensed — see the
[LICENSE](./daemon/internal/thirdparty/ps3-disc-dumper/LICENSE) in that
directory. Upstream ships only a GUI (Avalonia); the `Cli/` subdirectory
alongside it is DiscEcho's own first-party wrapper (not upstream code),
giving the daemon a headless CLI to shell out to.
