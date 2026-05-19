import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import EncodingChip from './EncodingChip.svelte';
import type { Disc, Job } from '$lib/wire';

const disc: Disc = {
  id: 'd1',
  drive_id: 'drv1',
  type: 'DVD',
  title: 'Jackass Volume Three',
  year: 2002,
  candidates: [],
  created_at: '2026-05-19T09:00:00Z',
  lifecycle_state: 'encoding',
};
const job: Job = {
  id: 'j-transcode',
  disc_id: 'd1',
  profile_id: 'p1',
  kind: 'transcode',
  state: 'running',
  active_step: 'transcode',
  progress: 42,
  output_bytes: 0,
  speed: '',
  eta_seconds: 480,
  started_at: '2026-05-19T10:00:51Z',
  created_at: '2026-05-19T10:00:51Z',
  steps: [],
};

describe('EncodingChip', () => {
  it('renders title, percent, and ETA', () => {
    const { getByTestId } = render(EncodingChip, { props: { disc, job } });
    const chip = getByTestId('encoding-chip');
    expect(chip.textContent).toContain('Jackass Volume Three');
    expect(chip.textContent).toContain('42'); // not strict '42%' — let formatProgress format it
    expect(chip.textContent).toContain('8m');
  });

  it('dispatches cancel when the close button is clicked', async () => {
    const { getByTestId, component } = render(EncodingChip, { props: { disc, job } });
    const onCancel = vi.fn();
    component.$on('cancel', onCancel);
    await fireEvent.click(getByTestId('encoding-chip-cancel'));
    expect(onCancel).toHaveBeenCalled();
    expect(onCancel.mock.calls[0][0].detail).toBe('j-transcode');
  });

  it('dispatches navigate when the chip body is clicked', async () => {
    const { getByTestId, component } = render(EncodingChip, { props: { disc, job } });
    const onNavigate = vi.fn();
    component.$on('navigate', onNavigate);
    await fireEvent.click(getByTestId('encoding-chip-body'));
    expect(onNavigate).toHaveBeenCalled();
    expect(onNavigate.mock.calls[0][0].detail).toBe('j-transcode');
  });
});
