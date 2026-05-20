import '@testing-library/jest-dom/vitest';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import SystemSection from './SystemSection.svelte';
import { settings, drives, integrations as integrationsStore } from '$lib/store';
import { toasts } from '$lib/toasts';

const versionResp = { version: '1.0.0', commit: 'abc', build_date: '2026-05-08' };
const hostResp = {
  hostname: 'discecho-host',
  kernel: '6.12.1',
  cpu_count: 8,
  uptime_seconds: 7200,
};
const librariesResp = {
  libraries: [
    { media: 'movies', label: 'Movies', path: '/library/movies', bytes: 1288490188, exists: true },
    { media: 'tv', label: 'TV', path: '/library/tv', bytes: 858993459, exists: true },
    { media: 'music', label: 'Music', path: '/library/music', bytes: 40000000, exists: true },
    { media: 'games', label: 'Games', path: '/library/games', bytes: 0, exists: false },
    { media: 'data', label: 'Data', path: '/library/data', bytes: 4831838208, exists: true },
  ],
  measured_at: '2026-05-20T12:00:00Z',
  measuring: false,
  array: { used_bytes: 6710886400, total_bytes: 13194139533, available_bytes: 6483253133 },
};
// After Task 8, TMDB and IGDB are no longer in /api/system/integrations.
const integrationsResp = {
  musicbrainz: { base_url: 'https://musicbrainz.org', user_agent: 'DiscEcho/test' },
  apprise: { bin: 'apprise', version: '1.7.0' },
  library_roots: {
    movies: '/library/movies',
    tv: '/library/tv',
    music: '/library/music',
    games: '/library/games',
    data: '/library/data',
  },
  items: [
    {
      name: 'MusicBrainz',
      hint: 'audio CD metadata + AccurateRip',
      status: 'connected',
      detail: 'https://musicbrainz.org',
    },
    {
      name: 'Game discs',
      hint: 'auto-id by boot code + post-rip MD5 verify',
      status: 'connected',
    },
    { name: 'Apprise', hint: 'notification dispatch', status: 'no URLs configured' },
  ],
};

const igdbResp = {
  name: 'igdb',
  source: 'env',
  configured: true,
  fields_present: ['client_id', 'client_secret'],
  values: { client_id: 'env-id', client_secret: 'env-secret' },
};
const tmdbResp = {
  name: 'tmdb',
  source: 'unset',
  configured: false,
  fields_present: [],
  values: {},
};
const makemkvResp = {
  name: 'makemkv',
  source: 'unset',
  configured: false,
  fields_present: [],
  values: {},
};

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as unknown as Response;
}

function mockEndpoints(overrides: Record<string, unknown> = {}) {
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : (input as URL).toString();
      const method = init?.method ?? 'GET';
      if (method === 'GET' && url === '/api/version')
        return Promise.resolve(jsonResponse(overrides.version ?? versionResp));
      if (method === 'GET' && url === '/api/system/host')
        return Promise.resolve(jsonResponse(overrides.host ?? hostResp));
      if (method === 'GET' && url === '/api/system/integrations')
        return Promise.resolve(jsonResponse(overrides.integrations ?? integrationsResp));
      if (method === 'GET' && url === '/api/system/libraries')
        return Promise.resolve(jsonResponse(overrides.libraries ?? librariesResp));
      if (method === 'POST' && url === '/api/system/libraries/recalc') {
        recalcMock();
        return Promise.resolve(
          jsonResponse(overrides.libraries ?? { ...librariesResp, measuring: true }),
        );
      }
      if (method === 'GET' && url === '/api/integrations/igdb')
        return Promise.resolve(jsonResponse(overrides.igdb ?? igdbResp));
      if (method === 'GET' && url === '/api/integrations/tmdb')
        return Promise.resolve(jsonResponse(overrides.tmdb ?? tmdbResp));
      if (method === 'GET' && url === '/api/integrations/makemkv')
        return Promise.resolve(jsonResponse(overrides.makemkv ?? makemkvResp));
      if (method === 'PUT' && url === '/api/settings') {
        apiPutMock(url, init?.body ? JSON.parse(init.body as string) : null);
        return Promise.resolve(jsonResponse({ ok: true }));
      }
      if (method === 'PATCH' && url.endsWith('/offset')) {
        const body = init?.body ? JSON.parse(init.body as string) : null;
        apiPatchMock(url, body);
        return Promise.resolve(
          jsonResponse({ id: 'd1', read_offset: body?.read_offset, read_offset_source: 'manual' }),
        );
      }
      return Promise.reject(new Error('unexpected ' + method + ' ' + url));
    }),
  );
}

const apiPutMock = vi.fn();
const apiPatchMock = vi.fn();
const recalcMock = vi.fn();

describe('SystemSection', () => {
  beforeEach(() => {
    apiPutMock.mockReset();
    apiPatchMock.mockReset();
    recalcMock.mockReset();
    mockEndpoints();
    toasts.set([]);
    settings.set({});
    integrationsStore.set({ igdb: undefined, tmdb: undefined, makemkv: undefined });
    drives.set([
      {
        id: 'd1',
        model: 'ASUS BW-16D1HT',
        bus: 'usb',
        dev_path: '/dev/sr0',
        state: 'idle',
        last_seen_at: '2026-05-08T00:00:00Z',
        read_offset: 0,
      },
    ]);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders four subsections after data loads', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('discecho-host'));
    expect(container.textContent).toContain('Library paths');
    expect(container.textContent).toContain('Drives');
    expect(container.textContent).toContain('API keys & connections');
    expect(container.textContent).toContain('Host');
  });

  it('renders ApiRow for remaining integrations (MusicBrainz, Game discs, Apprise)', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('audio CD metadata'));
    expect(container.textContent).toContain('Game discs');
    expect(container.textContent).toContain('Apprise');
    // Connected pill rendered for the connected rows.
    const connectedBadges = container.querySelectorAll('span.text-accent');
    expect(connectedBadges.length).toBeGreaterThanOrEqual(1);
  });

  it('renders one PathField row per typed library root', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('/library/movies'));
    for (const label of ['Movies', 'TV', 'Music', 'Games', 'Data archive']) {
      expect(container.textContent).toContain(label);
    }
    for (const path of [
      '/library/movies',
      '/library/tv',
      '/library/music',
      '/library/games',
      '/library/data',
    ]) {
      expect(container.textContent).toContain(path);
    }
  });

  it('saves edited library roots via apiPut with typed keys', async () => {
    const { container, getByText } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('/library/movies'));
    // Click the first PathField (Movies) to enter edit mode.
    const moviesButton = Array.from(container.querySelectorAll('button')).find(
      (b) => b.textContent && b.textContent.includes('/library/movies'),
    ) as HTMLButtonElement;
    expect(moviesButton).toBeTruthy();
    await fireEvent.click(moviesButton);
    const input = container.querySelector('input[type="text"]') as HTMLInputElement;
    expect(input).toBeTruthy();
    input.value = '/srv/films';
    await fireEvent.input(input);
    await fireEvent.blur(input);
    // Save changes button appears once a row is dirty.
    await waitFor(() => expect(getByText('Save changes')).toBeInTheDocument());
    await fireEvent.click(getByText('Save changes'));
    await waitFor(() =>
      expect(apiPutMock).toHaveBeenCalledWith('/api/settings', {
        'library.movies': '/srv/films',
      }),
    );
    await waitFor(() =>
      expect(get(toasts)).toContainEqual(
        expect.objectContaining({ kind: 'success', message: 'Library paths saved' }),
      ),
    );
  });

  it('shows the drive row from the store', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('ASUS BW-16D1HT'));
    expect(container.textContent).toContain('/dev/sr0');
    expect(container.textContent).toContain('idle');
  });

  it('shows MusicBrainz connected badge', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('connected'));
    expect(container.textContent).toContain('audio CD metadata');
  });

  it('shows TMDB as Unset when not configured via /api/integrations/tmdb', async () => {
    // tmdbResp is already source:'unset', so default mockEndpoints covers this.
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toMatch(/tmdb/i));
    expect(container.textContent).toMatch(/unset/i);
  });

  it('renders the IGDB / TMDB / MakeMKV editor rows', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toMatch(/igdb/i));
    expect(container.textContent).toContain('Configured (env)');
    expect(container.textContent).toMatch(/tmdb/i);
    expect(container.textContent).toMatch(/makemkv/i);
  });

  it('renders per-library sizes and the array summary', async () => {
    const { container } = render(SystemSection);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="libraries-panel"]')).toBeTruthy(),
    );
    // A row per media type, with the missing root showing a dash.
    expect(container.querySelector('[data-testid="library-row-movies"]')).toBeTruthy();
    const games = container.querySelector('[data-testid="library-row-games"]');
    expect(games?.textContent).toContain('—');
    // Array summary line + "last measured" affordance.
    expect(container.querySelector('[data-testid="library-array"]')?.textContent).toMatch(/free/);
    expect(container.textContent).toMatch(/last measured/);
  });

  it('recalculates library sizes via POST when the button is clicked', async () => {
    const { container } = render(SystemSection);
    await waitFor(() =>
      expect(container.querySelector('[data-testid="library-recalc"]')).toBeTruthy(),
    );
    const btn = container.querySelector('[data-testid="library-recalc"]') as HTMLButtonElement;
    await fireEvent.click(btn);
    await waitFor(() => expect(recalcMock).toHaveBeenCalledTimes(1));
  });

  it('renders empty state when no drives', async () => {
    drives.set([]);
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('No drives detected'));
  });

  it('shows the read-offset row with uncalibrated badge when unset', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('ASUS BW-16D1HT'));
    const offsetRow = container.querySelector('[data-testid="drive-offset-row"]');
    expect(offsetRow).toBeTruthy();
    const source = offsetRow?.querySelector('[data-testid="drive-offset-source"]');
    expect(source?.textContent).toContain('uncalibrated');
    const value = offsetRow?.querySelector('[data-testid="drive-offset-value"]');
    expect(value?.textContent?.trim()).toBe('0');
  });

  it('disables the offset edit button while the drive is busy', async () => {
    drives.set([
      {
        id: 'd1',
        model: 'ASUS BW-16D1HT',
        bus: 'usb',
        dev_path: '/dev/sr0',
        state: 'ripping',
        last_seen_at: '2026-05-08T00:00:00Z',
        read_offset: 0,
      },
    ]);
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('ASUS BW-16D1HT'));
    const btn = container.querySelector(
      '[data-testid="drive-offset-edit"]',
    ) as HTMLButtonElement | null;
    expect(btn).toBeTruthy();
    expect(btn?.disabled).toBe(true);
  });

  it('saves a new offset via PATCH and updates the drive store', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('ASUS BW-16D1HT'));
    await fireEvent.click(
      container.querySelector('[data-testid="drive-offset-edit"]') as HTMLButtonElement,
    );
    const input = container.querySelector('[data-testid="drive-offset-input"]') as HTMLInputElement;
    expect(input).toBeTruthy();
    input.value = '667';
    await fireEvent.input(input);
    await fireEvent.click(
      container.querySelector('[data-testid="drive-offset-save"]') as HTMLButtonElement,
    );
    await waitFor(() =>
      expect(apiPatchMock).toHaveBeenCalledWith('/api/drives/d1/offset', { read_offset: 667 }),
    );
    await waitFor(() => {
      const stored = get(drives)[0];
      expect(stored.read_offset).toBe(667);
      expect(stored.read_offset_source).toBe('manual');
    });
  });

  it('rejects an out-of-range offset client-side without calling the API', async () => {
    const { container } = render(SystemSection);
    await waitFor(() => expect(container.textContent).toContain('ASUS BW-16D1HT'));
    await fireEvent.click(
      container.querySelector('[data-testid="drive-offset-edit"]') as HTMLButtonElement,
    );
    const input = container.querySelector('[data-testid="drive-offset-input"]') as HTMLInputElement;
    input.value = '9001';
    await fireEvent.input(input);
    await fireEvent.click(
      container.querySelector('[data-testid="drive-offset-save"]') as HTMLButtonElement,
    );
    await waitFor(() => {
      const err = container.querySelector('[data-testid="drive-offset-error"]');
      expect(err?.textContent).toMatch(/within ±3000/);
    });
    expect(apiPatchMock).not.toHaveBeenCalled();
  });
});
