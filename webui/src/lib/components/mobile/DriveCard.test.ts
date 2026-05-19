import '@testing-library/jest-dom/vitest';
import { render, cleanup } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import DriveCard from './DriveCard.svelte';
import { jobs, discs } from '$lib/store';
import type { Drive, Disc, Job } from '$lib/wire';

const idleDrive: Drive = {
  id: 'drv-1',
  model: 'ASUS SDRW',
  bus: 'USB · sr0',
  dev_path: '/dev/sr0',
  state: 'idle',
  last_seen_at: '2026-05-15T10:00:00Z',
  read_offset: 0,
};

const rippingDrive: Drive = {
  ...idleDrive,
  state: 'ripping',
};

const baseRipJob: Job = {
  id: 'job-1',
  disc_id: 'disc-1',
  drive_id: 'drv-1',
  profile_id: 'p1',
  state: 'running',
  active_step: 'rip',
  progress: 50,
  output_bytes: 0,
  created_at: '2026-05-15T10:00:00Z',
  steps: [],
};

const seedDisc = (overrides: Partial<Disc> = {}): Disc => ({
  id: 'disc-1',
  drive_id: 'drv-1',
  type: 'AUDIO_CD',
  title: 'Fear and Bullets',
  year: 1997,
  toc_hash: 'toc',
  candidates: [],
  created_at: '2026-05-16T10:00:00Z',
  ...overrides,
});

describe('mobile/DriveCard', () => {
  beforeEach(() => {
    jobs.set([]);
    discs.set({});
  });

  afterEach(() => {
    cleanup();
  });

  it('shows a Re-rip button when the inserted disc has a prior done job', () => {
    jobs.set([
      {
        id: 'old-job',
        disc_id: 'disc-1',
        drive_id: 'drv-1',
        profile_id: 'p1',
        state: 'done',
        active_step: 'eject',
        progress: 100,
        output_bytes: 0,
        created_at: '2026-05-15T10:00:00Z',
        started_at: '2026-05-15T10:00:01Z',
        finished_at: '2026-05-15T10:30:00Z',
        steps: [],
      },
    ]);
    const { getByTestId, getByText } = render(DriveCard, {
      drive: idleDrive,
      disc: seedDisc({ id: 'disc-1', drive_id: 'drv-1' }),
    });
    expect(getByTestId('drive-rerip')).toBeTruthy();
    expect(getByText(/Already ripped 2026-05-15 — re-rip\?/)).toBeInTheDocument();
  });

  it('shows the error banner and tip when last_error is set', () => {
    const errorDrive: Drive = {
      ...idleDrive,
      state: 'error',
      last_error: 'cd-info: exit status 1',
      last_error_tip: 'Xbox game discs require a drive with Kreon firmware …',
    };
    const { getByText } = render(DriveCard, { drive: errorDrive });
    expect(getByText(/Drive error/i)).toBeInTheDocument();
    expect(getByText(/cd-info: exit status 1/)).toBeInTheDocument();
    expect(getByText(/Kreon firmware/)).toBeInTheDocument();
  });

  it('hides the error banner when last_error is empty', () => {
    const { queryByText } = render(DriveCard, { drive: idleDrive });
    expect(queryByText(/Drive error/i)).toBeNull();
  });

  it('shows default rip label when active_substep is absent', () => {
    const { getByText } = render(DriveCard, {
      drive: rippingDrive,
      disc: seedDisc(),
      job: baseRipJob,
    });
    expect(getByText(/Rip — Read raw data/)).toBeInTheDocument();
  });

  it('shows REFINE label when active_substep is REFINE', () => {
    const job: Job = { ...baseRipJob, active_substep: 'REFINE' };
    const { getByText } = render(DriveCard, {
      drive: rippingDrive,
      disc: seedDisc(),
      job,
    });
    expect(getByText(/Recovering damaged sectors/i)).toBeInTheDocument();
  });

  it('shows SPLIT label when active_substep is SPLIT', () => {
    const job: Job = { ...baseRipJob, active_substep: 'SPLIT' };
    const { getByText } = render(DriveCard, {
      drive: rippingDrive,
      disc: seedDisc(),
      job,
    });
    expect(getByText(/Splitting tracks/i)).toBeInTheDocument();
  });

  describe('encoding chip (T11)', () => {
    it('renders the chip when a prior disc from this drive is still encoding', () => {
      const newDisc: Disc = {
        id: 'd-new',
        type: 'DVD',
        title: 'Number Two',
        candidates: [],
        created_at: '2026-05-19T10:30:00Z',
        drive_id: 'drv-1',
        lifecycle_state: 'awaiting_decision',
      };
      const priorDisc: Disc = {
        id: 'd-old',
        type: 'DVD',
        title: 'Volume Three',
        candidates: [],
        created_at: '2026-05-19T10:00:00Z',
        drive_id: 'drv-1',
        lifecycle_state: 'encoding',
      };
      const transcodeJob: Job = {
        id: 'j-tr',
        disc_id: 'd-old',
        drive_id: 'drv-1',
        profile_id: 'p1',
        state: 'running',
        kind: 'transcode',
        progress: 42,
        eta_seconds: 480,
        active_step: 'transcode',
        output_bytes: 0,
        created_at: '2026-05-19T10:10:00Z',
        steps: [],
      };
      discs.set({ 'd-old': priorDisc, 'd-new': newDisc });
      jobs.set([transcodeJob]);
      const { queryByTestId } = render(DriveCard, {
        drive: idleDrive,
        disc: newDisc,
      });
      expect(queryByTestId('encoding-chip')).not.toBeNull();
    });

    it('does NOT render the chip when no prior disc is encoding', () => {
      const newDisc: Disc = {
        id: 'd-new',
        type: 'DVD',
        title: 'Number Two',
        candidates: [],
        created_at: '2026-05-19T10:30:00Z',
        drive_id: 'drv-1',
        lifecycle_state: 'awaiting_decision',
      };
      discs.set({ 'd-new': newDisc });
      jobs.set([]);
      const { queryByTestId } = render(DriveCard, {
        drive: idleDrive,
        disc: newDisc,
      });
      expect(queryByTestId('encoding-chip')).toBeNull();
    });

    it('does NOT render the chip when the encoding disc IS the primary disc', () => {
      const sameDisc: Disc = {
        id: 'disc-1',
        type: 'DVD',
        title: 'Vol 3',
        candidates: [],
        created_at: '2026-05-19T10:00:00Z',
        drive_id: 'drv-1',
        lifecycle_state: 'encoding',
      };
      const transcodeJob: Job = {
        id: 'j-tr',
        disc_id: 'disc-1',
        drive_id: 'drv-1',
        profile_id: 'p1',
        state: 'running',
        kind: 'transcode',
        progress: 42,
        active_step: 'transcode',
        output_bytes: 0,
        created_at: '2026-05-19T10:10:00Z',
        steps: [],
      };
      discs.set({ 'disc-1': sameDisc });
      jobs.set([transcodeJob]);
      const { queryByTestId } = render(DriveCard, {
        drive: rippingDrive,
        disc: sameDisc,
      });
      expect(queryByTestId('encoding-chip')).toBeNull();
    });

    it('chip renders as sibling of the link wrapper when href is set', () => {
      const newDisc: Disc = {
        id: 'd-new',
        type: 'DVD',
        title: 'Number Two',
        candidates: [],
        created_at: '2026-05-19T10:30:00Z',
        drive_id: 'drv-1',
        lifecycle_state: 'awaiting_decision',
      };
      const priorDisc: Disc = {
        id: 'd-old',
        type: 'DVD',
        title: 'Volume Three',
        candidates: [],
        created_at: '2026-05-19T10:00:00Z',
        drive_id: 'drv-1',
        lifecycle_state: 'encoding',
      };
      const transcodeJob: Job = {
        id: 'j-tr',
        disc_id: 'd-old',
        drive_id: 'drv-1',
        profile_id: 'p1',
        state: 'running',
        kind: 'transcode',
        progress: 42,
        eta_seconds: 480,
        active_step: 'transcode',
        output_bytes: 0,
        created_at: '2026-05-19T10:10:00Z',
        steps: [],
      };
      discs.set({ 'd-old': priorDisc, 'd-new': newDisc });
      jobs.set([transcodeJob]);
      const { queryByTestId } = render(DriveCard, {
        drive: idleDrive,
        disc: newDisc,
        href: '/drives/drv-1',
      });
      const chip = queryByTestId('encoding-chip');
      expect(chip).not.toBeNull();
      // The chip must NOT be inside an <a> — buttons inside anchors are
      // invalid HTML and Svelte/the browser will warn.
      expect(chip?.closest('a')).toBeNull();
    });
  });
});
