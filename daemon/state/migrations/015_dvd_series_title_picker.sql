-- 015_dvd_series_title_picker: flip the seeded DVD-Series profile's
-- options to surface the pre-rip title picker by default. TV box-set
-- discs almost always need user input (episodes are often out of
-- order on the disc; the auto pickLongestTitle/per-title-floor
-- heuristic guesses wrong on multi-episode discs that include trailers
-- + behind-the-scenes featurettes). show_title_picker=true makes the
-- dashboard route DVD-Series rips through the scan + picker flow
-- instead of straight to rip.
--
-- json_set merges the new key without clobbering existing options
-- (quality_rf, encoder_preset, dvd_selection_mode, …). Profile rows
-- the user has manually edited keep their existing options blob;
-- only DVD-Series rows still using the default name and unchanged
-- engine pick up the flip.
UPDATE profiles
   SET options_json = json_set(options_json, '$.show_title_picker', json('true'))
 WHERE name = 'DVD-Series'
   AND engine = 'HandBrake';
