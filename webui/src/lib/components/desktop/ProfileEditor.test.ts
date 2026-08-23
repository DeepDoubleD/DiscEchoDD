import '@testing-library/jest-dom/vitest';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import { tick } from 'svelte';
import { get } from 'svelte/store';
import ProfileEditor from './ProfileEditor.svelte';
import { toasts } from '$lib/toasts';
import type { Profile } from '$lib/wire';

async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) {
    await Promise.resolve();
    await tick();
  }
}

const seed: Profile = {
  id: 'p-cd',
  disc_type: 'AUDIO_CD',
  name: 'CD-FLAC',
  engine: 'whipper',
  format: 'FLAC',
  preset: 'AccurateRip',
  container: 'FLAC',
  video_codec: '',
  quality_preset: 'AccurateRip',
  hdr_pipeline: '',
  drive_policy: 'any',
  options: {},
  output_path_template: '{{.Title}}.flac',
  enabled: true,
  step_count: 6,
  created_at: '2026-05-07T12:00:00Z',
  updated_at: '2026-05-07T12:00:00Z',
};

const bd: Profile = {
  ...seed,
  id: 'p-bd',
  disc_type: 'BDMV',
  name: 'BD-1080p',
  engine: 'MakeMKV+HandBrake',
  format: 'MKV',
  container: 'MKV',
  video_codec: 'x265',
  quality_preset: 'high',
  hdr_pipeline: 'passthrough',
  drive_policy: 'any',
  output_path_template: '/library/movies/{{.Title}} ({{.Year}}).mkv',
  step_count: 7,
};

describe('ProfileEditor', () => {
  let fetchSpy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchSpy = vi.fn();
    vi.stubGlobal('fetch', fetchSpy);
    toasts.set([]);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders empty state when no profile and not creating', () => {
    const { getByText } = render(ProfileEditor, { profile: null, creating: false });
    expect(getByText(/select a profile/i)).toBeInTheDocument();
  });

  it('renders form fields populated from a loaded profile', () => {
    const { getByDisplayValue, getByText } = render(ProfileEditor, {
      profile: seed,
      creating: false,
    });
    expect(getByDisplayValue('CD-FLAC')).toBeInTheDocument();
    expect(getByText('{{.Title}}.flac')).toBeInTheDocument();
  });

  it('renders the mockup FormSections in edit mode (no Post-processing)', () => {
    const { getByText, queryByText } = render(ProfileEditor, { profile: bd, creating: false });
    expect(getByText('Engine')).toBeInTheDocument();
    expect(getByText('Encoding')).toBeInTheDocument();
    expect(getByText('Library')).toBeInTheDocument();
    expect(queryByText('Post-processing')).toBeNull();
  });

  it('locks Engine in edit mode', () => {
    const { container } = render(ProfileEditor, { profile: seed, creating: false });
    const engine = container.querySelector('[name="engine"]') as HTMLSelectElement;
    expect(engine.disabled).toBe(true);
  });

  it('Container select limits to engine schema containers', () => {
    const { container } = render(ProfileEditor, { profile: seed, creating: false });
    const containerSel = container.querySelector('[name="container"]') as HTMLSelectElement;
    const opts = Array.from(containerSel.options).map((o) => o.value);
    // whipper engine has only FLAC
    expect(opts).toEqual(['FLAC']);
  });

  it('exposes Video codec + HDR pipeline for video engines', () => {
    const { container } = render(ProfileEditor, { profile: bd, creating: false });
    const codec = container.querySelector('[name="video_codec"]') as HTMLSelectElement;
    const hdr = container.querySelector('[name="hdr_pipeline"]') as HTMLSelectElement;
    expect(codec).not.toBeNull();
    expect(hdr).not.toBeNull();
    expect(codec.value).toBe('x265');
    expect(hdr.value).toBe('passthrough');
  });

  it('hides Video codec for audio-only engines', () => {
    const { container } = render(ProfileEditor, { profile: seed, creating: false });
    expect(container.querySelector('[name="video_codec"]')).toBeNull();
    expect(container.querySelector('[name="hdr_pipeline"]')).toBeNull();
  });

  it('Save in new mode POSTs and dispatches saved on success', async () => {
    fetchSpy.mockResolvedValueOnce({
      ok: true,
      status: 201,
      json: async () => ({ ...seed, id: 'p-new' }),
    });
    const onSaved = vi.fn();
    const { getByRole, getByLabelText, component } = render(ProfileEditor, {
      profile: null,
      creating: true,
    });
    component.$on('saved', onSaved);

    await fireEvent.input(getByLabelText(/name/i), { target: { value: 'CD-FLAC-2' } });
    await fireEvent.click(getByRole('button', { name: /^create$/i }));

    // Allow microtasks to drain.
    await flush();
    expect(fetchSpy).toHaveBeenCalled();
    const [url, init] = fetchSpy.mock.calls[0];
    expect(url).toBe('/api/profiles');
    expect((init as RequestInit).method).toBe('POST');
    expect(onSaved).toHaveBeenCalled();
    expect(get(toasts)).toContainEqual(
      expect.objectContaining({ kind: 'success', message: 'Profile created' }),
    );
  });

  it('Save with 422 surfaces field errors against typed container field', async () => {
    fetchSpy.mockResolvedValueOnce({
      ok: false,
      status: 422,
      text: async () =>
        '{"container":"engine MakeMKV+HandBrake requires container in [MKV], got \\"MP4\\""}',
    });
    const { getByText, container } = render(ProfileEditor, { profile: bd, creating: false });

    await fireEvent.click(getByText(/save changes/i));
    await flush();

    expect(container.textContent).toMatch(/requires container in/);
  });

  it('Save with 422 on an unrendered option key surfaces a generic banner', async () => {
    // Regression: a field error whose key has no rendered control (e.g.
    // an options.<key> the schema mirror is missing) used to vanish —
    // the save appeared to do nothing. It must now show in the banner.
    fetchSpy.mockResolvedValueOnce({
      ok: false,
      status: 422,
      text: async () => '{"options.mystery_knob":"unknown option for engine MakeMKV+HandBrake"}',
    });
    const { getByText, container } = render(ProfileEditor, { profile: bd, creating: false });

    await fireEvent.click(getByText(/save changes/i));
    await flush();

    expect(container.textContent).toMatch(/Save rejected/);
    expect(container.textContent).toMatch(/mystery_knob/);
  });

  it('Duplicate dispatches a draft profile with empty id and "(copy)" suffix', async () => {
    const onDuplicate = vi.fn();
    const { getByText, component } = render(ProfileEditor, {
      profile: bd,
      creating: false,
    });
    component.$on('duplicate', (e) => onDuplicate(e.detail));

    await fireEvent.click(getByText(/duplicate/i));
    expect(onDuplicate).toHaveBeenCalledTimes(1);
    const draft = onDuplicate.mock.calls[0][0] as Profile;
    expect(draft.id).toBe('');
    expect(draft.name).toBe('BD-1080p (copy)');
    expect(draft.engine).toBe('MakeMKV+HandBrake');
  });

  describe('disc-type requirements callout', () => {
    const uhd: Profile = {
      ...bd,
      id: 'p-uhd',
      disc_type: 'UHD',
      name: 'UHD-Remux',
      engine: 'MakeMKV',
      video_codec: 'copy',
    };
    const xbox: Profile = {
      ...seed,
      id: 'p-xbox',
      disc_type: 'XBOX',
      name: 'Xbox-ISO',
      engine: 'redumper',
      container: 'ISO',
      format: 'ISO',
    };
    const xbox360: Profile = {
      ...seed,
      id: 'p-xbox360',
      disc_type: 'XBOX360',
      name: 'Xbox360-ISO',
      engine: 'redumper',
      container: 'ISO',
      format: 'ISO',
    };
    const wii: Profile = {
      ...seed,
      id: 'p-wii',
      disc_type: 'WII',
      name: 'WII-ISO',
      engine: 'redumper',
      container: 'ISO',
      format: 'ISO',
    };
    const ps3: Profile = {
      ...seed,
      id: 'p-ps3',
      disc_type: 'PS3',
      name: 'PS3-DECRYPTED',
      engine: 'ps3dumper-cli',
      container: '',
      format: 'DECRYPTED',
    };

    it('shows the UHD hardware/setup requirements with links', () => {
      const { getByText, container } = render(ProfileEditor, { profile: uhd, creating: false });
      expect(getByText(/Requires special hardware/i)).toBeInTheDocument();
      expect(container.textContent).toMatch(/LibreDrive/);
      expect(container.textContent).toMatch(/KEYDB\.cfg/);
      const libre = container.querySelector('a[href*="t=19634"]') as HTMLAnchorElement;
      expect(libre).not.toBeNull();
      expect(libre.target).toBe('_blank');
    });

    it('shows the Xbox OmniDrive requirement with a firmware link', () => {
      const { getByText, container } = render(ProfileEditor, { profile: xbox, creating: false });
      expect(getByText(/Requires special drive firmware/i)).toBeInTheDocument();
      expect(container.textContent).toMatch(/OmniDrive/);
      expect(
        container.querySelector('a[href="https://wiki.redump.info/index.php?title=OmniDrive"]'),
      ).not.toBeNull();
    });

    it('shows the Xbox 360 OmniDrive requirement with a firmware link', () => {
      const { getByText, container } = render(ProfileEditor, { profile: xbox360, creating: false });
      expect(getByText(/Requires special drive firmware/i)).toBeInTheDocument();
      expect(container.textContent).toMatch(/OmniDrive/);
      expect(container.textContent).toMatch(/XGD2\/XGD3/);
      expect(
        container.querySelector('a[href="https://wiki.redump.info/index.php?title=OmniDrive"]'),
      ).not.toBeNull();
    });

    it('shows the Wii OmniDrive + manual-override requirement with a firmware link', () => {
      const { getByText, container } = render(ProfileEditor, { profile: wii, creating: false });
      expect(getByText(/Requires special drive firmware/i)).toBeInTheDocument();
      expect(container.textContent).toMatch(/OmniDrive/);
      expect(container.textContent).toMatch(/not even a table of contents/);
      expect(
        container.querySelector('a[href="https://wiki.redump.info/index.php?title=OmniDrive"]'),
      ).not.toBeNull();
    });

    it('shows the PS3 disc-key requirement (no firmware needed, unlike the others)', () => {
      const { getByText, container } = render(ProfileEditor, { profile: ps3, creating: false });
      expect(getByText(/Needs a disc decryption key/i)).toBeInTheDocument();
      expect(container.textContent).toMatch(/stock-mountable/);
      expect(container.textContent).toMatch(/IRD library/);
    });

    it('shows no requirements callout for an audio CD profile', () => {
      const { queryByText } = render(ProfileEditor, { profile: seed, creating: false });
      expect(queryByText(/Requires special/i)).toBeNull();
      expect(queryByText(/Requires MakeMKV/i)).toBeNull();
    });
  });
});
