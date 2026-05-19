-- 019_quality_preset_slugs: convert the cosmetic free-text quality_preset
-- display strings into the validated tier slugs introduced alongside the
-- functional quality-tier picker. Only the known seeded strings are
-- rewritten; any user-authored free-text value is left untouched and
-- surfaces as "Custom" in the editor on next edit. Both quality_preset and
-- its deprecated `preset` mirror are updated so a profile re-saved through
-- the API (which copies preset := quality_preset) stays consistent.

-- Encode-bearing seeds (HandBrake-family x264/x265) -> the "high" tier.
UPDATE profiles SET quality_preset = 'high', preset = 'high'
WHERE quality_preset IN (
  'x264 RF 18 · slow',
  'x264 RF 18 · slow · + extras',
  'x264 RF 18 · slow · per-title',
  'x265 RF 20 · slow · + extras',
  'x265 RF 20 · slow · per-title',
  'x265 RF 19 10-bit',
  'x265 RF 19 10-bit · + extras'
);

-- Lossless / passthrough seeds (MakeMKV copy, whipper, redumper, ddrescue)
-- carry no quality tier.
UPDATE profiles SET quality_preset = '', preset = ''
WHERE quality_preset IN (
  'passthrough',
  'AccurateRip · cuesheet',
  'default'
);
