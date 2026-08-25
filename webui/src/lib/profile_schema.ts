// Hand-mirrored from daemon/api/profile_schema.go.
// Server is authoritative; this file drives form-field hints in the
// editor (engine select options, container/codec/format select per
// engine, etc).
//
// When the server adds an engine the client schema goes stale — UI
// degrades to "engine unknown to client" hint, server still validates
// correctly.

export type OptionType = 'string' | 'int' | 'bool';

export interface OptionSpec {
  type: OptionType;
  required?: boolean;
  // defaultBool is the value the daemon will see when the option key is
  // absent from `Profile.options`. Used by the form to render a sensible
  // initial checkbox state for new profiles (the daemon's
  // `optBoolDefaultTrue` helper makes "missing" = "true" for the
  // whipper post-processing options, so the UI should match).
  defaultBool?: boolean;
}

export interface EngineSpec {
  formats: string[];
  containers: string[];
  videoCodecs: string[];
  options: Record<string, OptionSpec>;
  stepCount: number;
}

export const ENGINES: Record<string, EngineSpec> = {
  whipper: {
    formats: ['FLAC'],
    containers: ['FLAC'],
    videoCodecs: [],
    options: {
      embed_cover_art: { type: 'bool', defaultBool: true },
      replaygain_album_mode: { type: 'bool', defaultBool: true },
    },
    stepCount: 6,
  },
  MakeMKV: {
    formats: ['MKV'],
    containers: ['MKV'],
    videoCodecs: ['copy'],
    options: {
      min_title_seconds: { type: 'int' },
      keep_all_tracks: { type: 'bool' },
      show_title_picker: { type: 'bool' },
      include_extras: { type: 'bool' },
      min_extra_seconds: { type: 'int' },
      extras_max_ratio: { type: 'int' },
      dvd_selection_mode: { type: 'string' },
      season: { type: 'int' },
      extract_text_subtitles: { type: 'bool' },
      content_type: { type: 'string' },
    },
    stepCount: 6,
  },
  'MakeMKV+HandBrake': {
    formats: ['MKV'],
    containers: ['MKV'],
    videoCodecs: ['x265', 'x264', 'nvenc_h265', 'nvenc_h264', 'av1', 'copy'],
    options: {
      min_title_seconds: { type: 'int' },
      keep_all_tracks: { type: 'bool' },
      show_title_picker: { type: 'bool' },
      include_extras: { type: 'bool' },
      min_extra_seconds: { type: 'int' },
      extras_max_ratio: { type: 'int' },
      dvd_selection_mode: { type: 'string' },
      quality_rf: { type: 'int' },
      encoder_preset: { type: 'string' },
      season: { type: 'int' },
      max_height: { type: 'int' },
      stereo_audio: { type: 'bool' },
      extract_text_subtitles: { type: 'bool' },
      content_type: { type: 'string' },
    },
    stepCount: 7,
  },
  HandBrake: {
    formats: ['MP4', 'MKV'],
    containers: ['MP4', 'MKV'],
    videoCodecs: ['x265', 'x264', 'nvenc_h265', 'nvenc_h264', 'av1'],
    options: {
      min_title_seconds: { type: 'int' },
      season: { type: 'int' },
      dvd_selection_mode: { type: 'string' },
      quality_rf: { type: 'int' },
      encoder_preset: { type: 'string' },
      show_title_picker: { type: 'bool' },
      include_extras: { type: 'bool' },
      min_extra_seconds: { type: 'int' },
      extras_max_ratio: { type: 'int' },
      max_height: { type: 'int' },
      stereo_audio: { type: 'bool' },
      extract_text_subtitles: { type: 'bool' },
      content_type: { type: 'string' },
    },
    stepCount: 7,
  },
  'redumper+chdman': {
    formats: ['CHD'],
    containers: ['CHD'],
    videoCodecs: [],
    options: {},
    stepCount: 7,
  },
  redumper: {
    formats: ['ISO'],
    containers: ['ISO'],
    videoCodecs: [],
    options: {},
    stepCount: 5,
  },
  ddrescue: {
    formats: ['ISO'],
    containers: ['ISO'],
    videoCodecs: [],
    options: {},
    stepCount: 6,
  },
  vcdimager: {
    formats: ['MPEG'],
    containers: ['MPG'],
    videoCodecs: [],
    options: {},
    stepCount: 6,
  },
};

// Display order (NOT the Go enum order): drives the desktop profile-list
// rank, the mobile group headers, and the editor's disc-type dropdown.
// Grouped Movies → Audio → Games → Data, each group oldest → newest.
// Game order is by console release year (intra-1994 order is approximate).
export const DISC_TYPES: ReadonlyArray<string> = [
  // Movies
  'VCD',
  'DVD',
  'BDMV',
  'UHD',
  // Audio
  'AUDIO_CD',
  // Games (oldest → newest)
  'PCECD',
  'FMTOWNS',
  'SEGACD',
  'CDI',
  '3DO',
  'CD32',
  'PCFX',
  'NEOCD',
  'SAT',
  'PSX',
  'JAGCD',
  'PIPPIN',
  'DC',
  'PS2',
  'XBOX',
  'XBOX360',
  'WII',
  'WIIU',
  'PS3',
  // Catch-all
  'DATA',
];

// HDR_PIPELINES mirrors daemon/api/profile_schema.go HDRPipelines. The
// empty string is the per-engine default (audio/data/games engines have
// no HDR concept).
export const HDR_PIPELINES: ReadonlyArray<string> = [
  '',
  'passthrough',
  'hdr10plus',
  'tone-map-sdr',
  'strip',
];

export const HDR_PIPELINE_LABELS: Record<string, string> = {
  '': '—',
  passthrough: 'HDR10+ passthrough',
  hdr10plus: 'HDR10+ enhance',
  'tone-map-sdr': 'Tone-map to SDR',
  strip: 'Strip HDR',
};

// DRIVE_POLICIES mirrors daemon/api/profile_schema.go DrivePolicies.
// "drv-N" pins to a specific drive id (the UI also accepts whatever
// drive ids are currently attached).
export const DRIVE_POLICIES: ReadonlyArray<string> = ['any', 'drv-1', 'drv-2', 'drv-3'];

export const DRIVE_POLICY_LABELS: Record<string, string> = {
  any: 'Any available',
  'drv-1': 'Pin to drv-1',
  'drv-2': 'Pin to drv-2',
  'drv-3': 'Pin to drv-3',
};

// DISC_TYPE_ENGINES mirrors daemon/api/profile_schema.go DiscTypeEngines:
// the curated allow-list of engines per disc type. The first entry is the
// recommended default the editor pre-fills when a disc type is chosen.
// The server is authoritative and rejects engines not listed here.
export const DISC_TYPE_ENGINES: Record<string, string[]> = {
  AUDIO_CD: ['whipper'],
  DVD: ['MakeMKV+HandBrake', 'MakeMKV', 'HandBrake'],
  BDMV: ['MakeMKV+HandBrake', 'MakeMKV'],
  UHD: ['MakeMKV', 'MakeMKV+HandBrake'],
  PSX: ['redumper+chdman'],
  PS2: ['redumper+chdman'],
  SAT: ['redumper+chdman'],
  DC: ['redumper+chdman'],
  XBOX: ['redumper'],
  XBOX360: ['redumper'],
  WII: ['redumper'],
  PS3: ['ps3dumper-cli'],
  SEGACD: ['redumper+chdman'],
  '3DO': ['redumper+chdman'],
  PCFX: ['redumper+chdman'],
  JAGCD: ['redumper+chdman'],
  CDI: ['redumper+chdman'],
  PCECD: ['redumper+chdman'],
  NEOCD: ['redumper+chdman'],
  CD32: ['redumper+chdman'],
  FMTOWNS: ['redumper+chdman'],
  PIPPIN: ['redumper+chdman'],
  DATA: ['ddrescue'],
  VCD: ['vcdimager'],
};

// QUALITY_TIER_SLUGS mirrors daemon/api/profile_schema.go QualityTierSlugs.
// "" is the value for lossless/passthrough engines; "custom" reveals the
// raw RF + encoder-preset inputs.
export const QUALITY_TIER_SLUGS: ReadonlyArray<string> = [
  'archival',
  'high',
  'balanced',
  'space-saver',
  'custom',
];

export const QUALITY_TIER_LABELS: Record<string, string> = {
  archival: 'Archival — visually lossless',
  high: 'High — near-transparent',
  balanced: 'Balanced',
  'space-saver': 'Space-saver — smaller files',
  custom: 'Custom…',
};

export interface ResolvedTier {
  quality_rf: number;
  encoder_preset: string;
}

// QUALITY_TIERS mirrors daemon/api/profile_schema.go QualityTiers: codec →
// tier slug → resolved (RF, encoder speed-preset). Picking a tier writes
// these into options.quality_rf + options.encoder_preset; the pipeline
// reads those two options. "custom" is intentionally absent.
export const QUALITY_TIERS: Record<string, Record<string, ResolvedTier>> = {
  x264: {
    archival: { quality_rf: 16, encoder_preset: 'slower' },
    high: { quality_rf: 18, encoder_preset: 'slow' },
    balanced: { quality_rf: 21, encoder_preset: 'medium' },
    'space-saver': { quality_rf: 23, encoder_preset: 'fast' },
  },
  x265: {
    archival: { quality_rf: 18, encoder_preset: 'slower' },
    high: { quality_rf: 20, encoder_preset: 'slow' },
    balanced: { quality_rf: 23, encoder_preset: 'medium' },
    'space-saver': { quality_rf: 25, encoder_preset: 'fast' },
  },
  nvenc_h264: {
    archival: { quality_rf: 18, encoder_preset: 'slower' },
    high: { quality_rf: 21, encoder_preset: 'slow' },
    balanced: { quality_rf: 24, encoder_preset: 'medium' },
    'space-saver': { quality_rf: 27, encoder_preset: 'fast' },
  },
  nvenc_h265: {
    archival: { quality_rf: 18, encoder_preset: 'slower' },
    high: { quality_rf: 21, encoder_preset: 'slow' },
    balanced: { quality_rf: 24, encoder_preset: 'medium' },
    'space-saver': { quality_rf: 27, encoder_preset: 'fast' },
  },
  av1: {
    archival: { quality_rf: 20, encoder_preset: 'slower' },
    high: { quality_rf: 24, encoder_preset: 'slow' },
    balanced: { quality_rf: 28, encoder_preset: 'medium' },
    'space-saver': { quality_rf: 32, encoder_preset: 'fast' },
  },
};

export interface DiscTypeDefault {
  engine: string;
  container: string;
  videoCodec: string;
  qualityPreset: string;
  outputPathTemplate: string;
}

// DISC_TYPE_DEFAULTS pre-fills a valid, runnable profile when a disc type
// is chosen for a new profile. Output templates reuse the seeder's strings
// (daemon/settings/settings.go). step_count is derived from the engine.
export const DISC_TYPE_DEFAULTS: Record<string, DiscTypeDefault> = {
  AUDIO_CD: {
    engine: 'whipper',
    container: 'FLAC',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate:
      '{{.Artist}}/{{.Album}} ({{.Year}})/{{printf "%02d" .TrackNumber}} - {{.Title}}.flac',
  },
  DVD: {
    engine: 'MakeMKV+HandBrake',
    container: 'MKV',
    videoCodec: 'x265',
    qualityPreset: 'high',
    outputPathTemplate: '{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv',
  },
  BDMV: {
    engine: 'MakeMKV+HandBrake',
    container: 'MKV',
    videoCodec: 'x265',
    qualityPreset: 'high',
    outputPathTemplate: '{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv',
  },
  UHD: {
    engine: 'MakeMKV',
    container: 'MKV',
    videoCodec: 'copy',
    qualityPreset: '',
    outputPathTemplate: '{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}) [UHD].mkv',
  },
  PSX: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'PlayStation/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  PS2: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'PlayStation 2/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  SAT: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Sega Saturn/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  DC: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Dreamcast/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  XBOX: {
    engine: 'redumper',
    container: 'ISO',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Xbox/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso',
  },
  XBOX360: {
    engine: 'redumper',
    container: 'ISO',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Xbox 360/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso',
  },
  WII: {
    engine: 'redumper',
    container: 'ISO',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Wii/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso',
  },
  PS3: {
    engine: 'ps3dumper-cli',
    container: '',
    videoCodec: '',
    qualityPreset: '',
    // A ps3dumper-cli dump is a decrypted folder tree, not a single
    // file -- {{.Title}} names the folder itself.
    outputPathTemplate: 'PS3/{{.Title}}',
  },
  SEGACD: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Sega CD/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  '3DO': {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: '3DO/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  PCFX: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'PC-FX/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  JAGCD: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Atari Jaguar CD/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  CDI: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Philips CD-i/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  PCECD: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'PC Engine CD/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  NEOCD: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Neo Geo CD/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  CD32: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Amiga CD32/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  FMTOWNS: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'FM Towns/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  PIPPIN: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: 'Bandai Pippin/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  DATA: {
    engine: 'ddrescue',
    container: 'ISO',
    videoCodec: '',
    qualityPreset: '',
    // Mirrors migration 021 + settings.go seeder: the [{{.ShortHash}}] suffix
    // disambiguates discs that share a generic ISO9660 volume label, which
    // otherwise collide at the move step. Keep in sync with the Go seeder.
    outputPathTemplate: '{{.Title}}/{{.Title}} [{{.ShortHash}}].iso',
  },
  VCD: {
    engine: 'vcdimager',
    container: 'MPG',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: '{{.Title}}',
  },
};

export interface OutputVar {
  name: string;
  desc: string;
}

// OUTPUT_VARS_BY_DISC_TYPE lists the template variables that are populated
// for each disc type. Display-only affordance — mirrors the per-pipeline
// field population in daemon/pipelines/* (OutputFields in output.go). For
// DVD/BDMV this is the union of movie + series fields (unused vars render
// empty, so referencing both is safe).
export const OUTPUT_VARS_BY_DISC_TYPE: Record<string, OutputVar[]> = {
  AUDIO_CD: [
    { name: 'Artist', desc: 'Album artist' },
    { name: 'Album', desc: 'Album title' },
    { name: 'Year', desc: 'Release year' },
    { name: 'TrackNumber', desc: 'Track number (use printf "%02d" to zero-pad)' },
    { name: 'Title', desc: 'Track title' },
    { name: 'DiscNumber', desc: 'Disc number in a multi-disc set' },
  ],
  DVD: dvdBdmvVars(),
  BDMV: dvdBdmvVars(),
  UHD: [
    { name: 'Title', desc: 'Movie title' },
    { name: 'Year', desc: 'Release year' },
  ],
  PSX: gameVars(),
  PS2: gameVars(),
  SAT: gameVars(),
  DC: gameVars(),
  XBOX: gameVars(),
  XBOX360: gameVars(),
  WII: gameVars(),
  // No Year/Region -- PARAM.SFO gives a real Title but not a
  // Redump-style region tag, unlike every other console here.
  PS3: [{ name: 'Title', desc: 'Game title (from PARAM.SFO)' }],
  SEGACD: gameVars(),
  '3DO': gameVars(),
  PCFX: gameVars(),
  JAGCD: gameVars(),
  CDI: gameVars(),
  PCECD: gameVars(),
  NEOCD: gameVars(),
  CD32: gameVars(),
  FMTOWNS: gameVars(),
  PIPPIN: gameVars(),
  DATA: [
    { name: 'Title', desc: 'Disc volume label' },
    { name: 'ShortHash', desc: 'Short content-hash id (disambiguates same-label discs)' },
  ],
  VCD: [{ name: 'Title', desc: 'Disc volume label (per-title folder for the .mpg tracks)' }],
};

function dvdBdmvVars(): OutputVar[] {
  return [
    { name: 'Title', desc: 'Movie title' },
    { name: 'Year', desc: 'Release year' },
    { name: 'Show', desc: 'Series name (per-title / series profiles)' },
    { name: 'Season', desc: 'Season number (use printf "%02d")' },
    { name: 'EpisodeNumber', desc: 'Episode number (use printf "%02d")' },
    { name: 'EpisodeTitle', desc: 'Episode title (when matched)' },
  ];
}

function gameVars(): OutputVar[] {
  return [
    { name: 'Title', desc: 'Game title' },
    { name: 'Year', desc: 'Release year' },
    { name: 'Region', desc: 'Disc region (USA / Europe / Japan / …)' },
  ];
}

// DISC_TYPE_REQUIREMENTS surfaces the hardware/setup prerequisites for the
// disc types that can fail (or never detect a disc) without them. Display-only
// webui copy — like DISC_TYPE_DEFAULTS / OUTPUT_VARS_BY_DISC_TYPE it has no Go
// mirror. Only the constrained types have an entry; everything else (audio,
// games, data) needs no special setup and renders nothing.
export interface DiscTypeRequirement {
  // 'warn' (amber) = won't work without it; 'info' (accent) = softer "configure X".
  tone: 'warn' | 'info';
  title: string;
  // Bullet lines. **double-asterisk** spans render bold (see ProfileEditor).
  items: string[];
  links?: { label: string; href: string }[];
}

const MAKEMKV_KEY_LINK = {
  label: 'Get a MakeMKV key',
  href: 'https://forum.makemkv.com/forum/viewtopic.php?t=1053',
};

export const DISC_TYPE_REQUIREMENTS: Record<string, DiscTypeRequirement | undefined> = {
  XBOX: {
    tone: 'warn',
    title: 'Requires special drive firmware',
    items: [
      "Needs an optical drive flashed with **OmniDrive** firmware (the same flash used for Blu-ray ripping). XGD media is unreadable on stock drives — the disc won't be detected at all.",
    ],
    links: [{ label: 'OmniDrive firmware', href: 'https://wiki.redump.info/index.php?title=OmniDrive' }],
  },
  XBOX360: {
    tone: 'warn',
    title: 'Requires special drive firmware',
    items: [
      "Needs an optical drive flashed with **OmniDrive** firmware (the same flash used for Blu-ray ripping) — XGD2/XGD3's security sectors aren't reachable through a plain DVD read.",
      'No curated boot-code fallback yet — identification depends entirely on a Redump "Microsoft - Xbox 360" dat being present.',
    ],
    links: [{ label: 'OmniDrive firmware', href: 'https://wiki.redump.info/index.php?title=OmniDrive' }],
  },
  WII: {
    tone: 'warn',
    title: 'Requires special drive firmware — and a manual type pick',
    items: [
      "Needs an optical drive flashed with **OmniDrive** firmware. A stock drive gets nothing at all off a Wii disc — not even a table of contents — so it can't be auto-classified.",
      'Insert the disc, then use the "Rip as…" override on the card that appears (it lands as an unclassified Data disc) to pick **Nintendo Wii** manually.',
      'No curated boot-code fallback — identification depends entirely on a Redump "Nintendo - Wii" dat being present.',
    ],
    links: [{ label: 'OmniDrive firmware', href: 'https://wiki.redump.info/index.php?title=OmniDrive' }],
  },
  PS3: {
    tone: 'info',
    title: 'Needs a disc decryption key',
    items: [
      'No special drive firmware needed — PS3 discs are stock-mountable Blu-ray media; only the game content itself is encrypted.',
      'Decryption needs a matching disc key, looked up automatically from Redump/the community IRD library and cached locally — an unrecognised disc may fail to dump if no key is available anywhere.',
      'The dump is a decrypted folder, not a single ISO file — point RPCS3 at the title folder directly.',
    ],
  },
  UHD: {
    tone: 'warn',
    title: 'Requires special hardware & setup',
    items: [
      "A UHD-friendly Blu-ray drive with **LibreDrive**-compatible firmware — stock UHD drives can't be read for ripping.",
      '**MakeMKV** configured with a valid key (Settings → Integrations → MakeMKV).',
      "An **AACS2** `KEYDB.cfg` key file on the host — DiscEcho doesn't ship one; without it, UHD jobs fail at the identify step.",
    ],
    links: [
      {
        label: 'LibreDrive drive list',
        href: 'https://forum.makemkv.com/forum/viewtopic.php?f=16&t=19634',
      },
      MAKEMKV_KEY_LINK,
    ],
  },
  BDMV: {
    tone: 'info',
    title: 'Requires MakeMKV',
    items: [
      'Blu-ray ripping uses MakeMKV — configure a valid key (Settings → Integrations → MakeMKV).',
    ],
    links: [MAKEMKV_KEY_LINK],
  },
  DVD: {
    tone: 'info',
    title: 'MakeMKV may be required',
    items: [
      'The **MakeMKV** and **MakeMKV+HandBrake** engines need a valid MakeMKV key (Settings → Integrations → MakeMKV).',
      'The **HandBrake** engine rips via dvdbackup and needs no key.',
    ],
    links: [MAKEMKV_KEY_LINK],
  },
};

export function requirementFor(discType: string): DiscTypeRequirement | undefined {
  return DISC_TYPE_REQUIREMENTS[discType];
}

export function engineNames(): string[] {
  return Object.keys(ENGINES);
}

// enginesFor returns the allowed engines for a disc type (server-curated).
// Falls back to all engines for an unknown disc type so the form still
// renders something rather than an empty dropdown.
export function enginesFor(discType: string): string[] {
  return DISC_TYPE_ENGINES[discType] ?? engineNames();
}

export function defaultsFor(discType: string): DiscTypeDefault | undefined {
  return DISC_TYPE_DEFAULTS[discType];
}

// resolveTier returns the RF + encoder-preset for a codec at a tier, or
// undefined for "custom"/unknown (caller keeps the raw values).
export function resolveTier(codec: string, tier: string): ResolvedTier | undefined {
  return QUALITY_TIERS[codec]?.[tier];
}

export function varsFor(discType: string): OutputVar[] {
  return OUTPUT_VARS_BY_DISC_TYPE[discType] ?? [];
}

export function specFor(engine: string): EngineSpec | undefined {
  return ENGINES[engine];
}
