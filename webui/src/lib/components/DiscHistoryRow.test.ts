import '@testing-library/jest-dom/vitest';
import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest';
import { render, fireEvent, cleanup } from '@testing-library/svelte';
import DiscHistoryRow from './DiscHistoryRow.svelte';
import type { DiscHistoryRow as RowT, DiscLifecycleState, Job } from '$lib/wire';
import * as store from '$lib/store';
import { jobs } from '$lib/store';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  jobs.set([]);
});

const baseRow = (lifecycle: DiscLifecycleState, overrides: Partial<RowT> = {}): RowT => ({
  disc: {
    id: 'd1',
    drive_id: 'drv1',
    type: 'DVD',
    title: 'Jackass Volume Three',
    year: 2002,
    runtime_seconds: 4560,
    candidates: [],
    created_at: '2026-05-19T08:25:40Z',
    lifecycle_state: lifecycle,
  },
  latest_rip_job_id: 'r1',
  first_attempt_at: '2026-05-19T08:25:40Z',
  output_bytes: 6_150_364_234,
  ...overrides,
});

const doneRipJob: Job = {
  id: 'r1',
  disc_id: 'd1',
  profile_id: 'p1',
  kind: 'rip',
  state: 'done',
  progress: 100,
  output_bytes: 6_150_364_234,
  started_at: '2026-05-19T08:25:40Z',
  finished_at: '2026-05-19T08:45:40Z',
  created_at: '2026-05-19T08:25:40Z',
};

describe('DiscHistoryRow', () => {
  it('renders title and lifecycle pill', () => {
    const { getByTestId } = render(DiscHistoryRow, { props: { row: baseRow('done') } });
    expect(getByTestId('disc-history-row').textContent).toContain('Jackass Volume Three');
    expect(getByTestId('lifecycle-pill').textContent?.trim()).toBe('Done');
  });

  it('shows Re-rip for done rows', () => {
    const { getByTestId } = render(DiscHistoryRow, { props: { row: baseRow('done') } });
    expect(getByTestId('disc-history-action').textContent?.trim()).toBe('Re-rip');
  });

  it('shows Stop for in-flight rows', () => {
    const { getByTestId } = render(DiscHistoryRow, {
      props: { row: baseRow('encoding', { latest_transcode_job_id: 't1' }) },
    });
    expect(getByTestId('disc-history-action').textContent?.trim()).toBe('Stop');
  });

  it('shows Retry for failed rows', () => {
    const { getByTestId } = render(DiscHistoryRow, { props: { row: baseRow('failed') } });
    expect(getByTestId('disc-history-action').textContent?.trim()).toBe('Retry');
  });

  it('emits navigate(latest_rip_job_id) when the body is clicked', async () => {
    const { getByTestId, component } = render(DiscHistoryRow, {
      props: { row: baseRow('done') },
    });
    const onNav = vi.fn();
    component.$on('navigate', onNav);
    await fireEvent.click(getByTestId('disc-history-body'));
    expect(onNav).toHaveBeenCalled();
    expect(onNav.mock.calls[0][0].detail).toBe('r1');
  });

  it('Re-rip calls startDisc with the disc id and last-done profile', async () => {
    jobs.set([doneRipJob]);
    const startSpy = vi.spyOn(store, 'startDisc').mockResolvedValue({} as never);
    const { getByTestId } = render(DiscHistoryRow, { props: { row: baseRow('done') } });
    await fireEvent.click(getByTestId('disc-history-action'));
    expect(startSpy).toHaveBeenCalledWith('d1', 'p1', 0);
  });

  it('Stop calls cancelJob with the latest transcode id when in-flight', async () => {
    const cancelSpy = vi.spyOn(store, 'cancelJob').mockResolvedValue(undefined);
    const { getByTestId } = render(DiscHistoryRow, {
      props: { row: baseRow('encoding', { latest_transcode_job_id: 't1' }) },
    });
    await fireEvent.click(getByTestId('disc-history-action'));
    expect(cancelSpy).toHaveBeenCalledWith('t1');
  });
});
