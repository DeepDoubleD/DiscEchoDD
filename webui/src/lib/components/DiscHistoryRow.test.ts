import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import DiscHistoryRow from './DiscHistoryRow.svelte';
import type { DiscHistoryRow as RowT, DiscLifecycleState } from '$lib/wire';

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
});
