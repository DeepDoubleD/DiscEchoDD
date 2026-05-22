-- 021_data_iso_shorthash: append a short content-hash id to the DATA profile's
-- output template so discs that share a generic ISO9660 volume label (e.g. the
-- many PictureCD/VCD hybrids labelled "VIDEOCD") no longer resolve to the same
-- path and fail at the move step. Only rows still on the old seeded default are
-- rewritten; any user-customised template is left untouched.

UPDATE profiles
SET output_path_template = '{{.Title}}/{{.Title}} [{{.ShortHash}}].iso'
WHERE disc_type = 'DATA'
  AND output_path_template = '{{.Title}}/{{.Title}}.iso';
