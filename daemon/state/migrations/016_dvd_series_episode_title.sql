-- 016_dvd_series_episode_title: refresh the seeded DVD-Series output
-- template to include the new {{.EpisodeTitle}} field. The picker's
-- TMDB-mapped episode names land in EpisodeTitle; when absent (movie
-- profile, picker-less rip), the `{{if .EpisodeTitle}}…{{end}}` guard
-- omits the suffix so the rendered path stays clean.
--
-- Only flips rows that still carry the pre-016 default template
-- verbatim — user-customised templates keep their tweaks. The WHERE
-- check matches the exact string the migration is replacing so a
-- profile someone edited in the UI is left alone.
UPDATE profiles
   SET output_path_template = '{{.Show}}/Season {{printf "%02d" .Season}}/{{.Show}} - S{{printf "%02d" .Season}}E{{printf "%02d" .EpisodeNumber}}{{if .EpisodeTitle}} - {{.EpisodeTitle}}{{end}}.mkv'
 WHERE name = 'DVD-Series'
   AND engine = 'HandBrake'
   AND output_path_template = '{{.Show}}/Season {{printf "%02d" .Season}}/{{.Show}} - S{{printf "%02d" .Season}}E{{printf "%02d" .EpisodeNumber}}.mkv';
