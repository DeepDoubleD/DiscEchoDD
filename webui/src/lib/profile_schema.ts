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
};

export const DISC_TYPES: ReadonlyArray<string> = [
  'AUDIO_CD',
  'DVD',
  'BDMV',
  'UHD',
  'PSX',
  'PS2',
  'SAT',
  'DC',
  'XBOX',
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
  DATA: ['ddrescue'],
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
    outputPathTemplate: '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  PS2: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  SAT: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  DC: {
    engine: 'redumper+chdman',
    container: 'CHD',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd',
  },
  XBOX: {
    engine: 'redumper',
    container: 'ISO',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso',
  },
  DATA: {
    engine: 'ddrescue',
    container: 'ISO',
    videoCodec: '',
    qualityPreset: '',
    outputPathTemplate: '{{.Title}}/{{.Title}}.iso',
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
  DATA: [{ name: 'Title', desc: 'Disc volume label' }],
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
