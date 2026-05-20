import '@testing-library/jest-dom/vitest';
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import GameDiscsCard, { type GameDiscsInfo } from './GameDiscsCard.svelte';

const partialInfo: GameDiscsInfo = {
  redumper_status: 'connected',
  redumper_bin: 'redumper',
  dat_dir: '/var/lib/discecho/redump',
  status: 'partial',
  systems: [
    {
      system: 'PSX',
      label: 'PlayStation',
      boot_code: 'ok',
      boot_code_count: 11402,
      redump_dat: 'loaded',
    },
    {
      system: 'PS2',
      label: 'PlayStation 2',
      boot_code: 'ok',
      boot_code_count: 10876,
      redump_dat: 'missing',
    },
    {
      system: 'SAT',
      label: 'Saturn',
      boot_code: 'ok',
      boot_code_count: 1204,
      redump_dat: 'missing',
    },
    {
      system: 'DC',
      label: 'Dreamcast',
      boot_code: 'ok',
      boot_code_count: 612,
      redump_dat: 'missing',
    },
    { system: 'XBOX', label: 'Xbox', boot_code: 'na', boot_code_count: 0, redump_dat: 'missing' },
  ],
};

describe('GameDiscsCard', () => {
  it('renders a row per system with boot-code and dat marks', () => {
    const { getByText, container } = render(GameDiscsCard, { info: partialInfo });
    for (const label of ['PlayStation', 'PlayStation 2', 'Saturn', 'Dreamcast', 'Xbox']) {
      expect(getByText(label)).toBeInTheDocument();
    }
    // Xbox boot-code is n/a.
    expect(getByText('— n/a')).toBeInTheDocument();
    // At least one dat loaded (PSX) and several missing.
    expect(container.textContent).toMatch(/✓ loaded/);
    expect(container.textContent).toMatch(/✗ missing/);
  });

  it('shows the partial badge and the dats directory + redump link', () => {
    const { container, getByText } = render(GameDiscsCard, { info: partialInfo });
    expect(getByText('partial')).toBeInTheDocument();
    expect(container.textContent).toMatch(/\/var\/lib\/discecho\/redump/);
    const link = container.querySelector(
      'a[href="http://redump.org/downloads/"]',
    ) as HTMLAnchorElement;
    expect(link).not.toBeNull();
    expect(link.target).toBe('_blank');
  });

  it('reflects IGDB configured state', () => {
    const { container } = render(GameDiscsCard, { info: partialInfo, igdbConfigured: true });
    expect(container.textContent).toMatch(/configured above/);
  });

  it('shows connected when all dats present', () => {
    const allLoaded: GameDiscsInfo = {
      ...partialInfo,
      status: 'connected',
      systems: partialInfo.systems.map((s) => ({ ...s, redump_dat: 'loaded' })),
    };
    const { getByText } = render(GameDiscsCard, { info: allLoaded });
    expect(getByText('connected')).toBeInTheDocument();
  });
});
