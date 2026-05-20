import '@testing-library/jest-dom/vitest';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import { get } from 'svelte/store';
import RetentionSection from './RetentionSection.svelte';
import { settings } from '$lib/store';
import { toasts } from '$lib/toasts';
import type { RetentionStatus } from '$lib/wire';

const updateRetentionMock = vi.fn();
const fetchStatusMock = vi.fn();
const runNowMock = vi.fn();

vi.mock('$lib/store', async () => {
  const actual = await vi.importActual<typeof import('$lib/store')>('$lib/store');
  return {
    ...actual,
    updateRetention: (...args: unknown[]) => updateRetentionMock(...args),
    fetchRetentionStatus: (...args: unknown[]) => fetchStatusMock(...args),
    runRetentionNow: (...args: unknown[]) => runNowMock(...args),
  };
});

const sampleStatus: RetentionStatus = {
  forever: false,
  policy: { success_days: 0, success_count: 0, failed_days: 0, failed_count: 0 },
  success_total: 3,
  failed_total: 2,
  would_delete: { success: 0, failed: 2, total: 2 },
  last_run_count: 0,
  next_run_at: '2026-05-21T03:00:00Z',
};

describe('RetentionSection', () => {
  beforeEach(() => {
    updateRetentionMock.mockReset();
    fetchStatusMock.mockReset();
    fetchStatusMock.mockResolvedValue(sampleStatus);
    runNowMock.mockReset();
    // All knobs zero so the invalid-path test can exercise it; forever on.
    settings.set({
      'retention.forever': 'true',
      'retention.success.days': '0',
      'retention.success.count': '0',
      'retention.failed.days': '0',
      'retention.failed.count': '0',
    });
    toasts.set([]);
  });

  it('toggle off reveals the per-outcome inputs', async () => {
    const { container, getByText } = render(RetentionSection);
    const toggle = container.querySelector('input[type="checkbox"]') as HTMLInputElement;
    expect(container.querySelectorAll('input[type="number"]').length).toBe(0);
    toggle.checked = false;
    await fireEvent.change(toggle);
    // Two buckets × (days + count) = 4 inputs.
    expect(container.querySelectorAll('input[type="number"]').length).toBe(4);
    expect(getByText('Successful rips')).toBeInTheDocument();
    expect(getByText('Failed / cancelled rips')).toBeInTheDocument();
  });

  it('save with no active limit surfaces inline error and skips PUT', async () => {
    const { container, getByText } = render(RetentionSection);
    const toggle = container.querySelector('input[type="checkbox"]') as HTMLInputElement;
    toggle.checked = false;
    await fireEvent.change(toggle);
    await fireEvent.click(getByText('Save'));
    expect(container.textContent).toMatch(/at least one limit/i);
    expect(updateRetentionMock).not.toHaveBeenCalled();
  });

  it('save with a valid knob PUTs the full policy', async () => {
    updateRetentionMock.mockResolvedValueOnce(undefined);
    const { container, getByText } = render(RetentionSection);
    const toggle = container.querySelector('input[type="checkbox"]') as HTMLInputElement;
    toggle.checked = false;
    await fireEvent.change(toggle);
    // First number input is "successful rips → delete after N days".
    const successDays = container.querySelectorAll('input[type="number"]')[0] as HTMLInputElement;
    successDays.value = '60';
    await fireEvent.input(successDays);
    await fireEvent.click(getByText('Save'));
    await Promise.resolve();
    await Promise.resolve();
    expect(updateRetentionMock).toHaveBeenCalledWith(
      expect.objectContaining({ forever: false, successDays: 60 }),
    );
    expect(get(toasts)).toContainEqual(
      expect.objectContaining({ kind: 'success', message: 'Retention settings saved' }),
    );
  });

  it('Run cleanup now triggers a prune and toasts the count', async () => {
    settings.set({
      'retention.forever': 'false',
      'retention.success.days': '0',
      'retention.success.count': '0',
      'retention.failed.days': '14',
      'retention.failed.count': '0',
    });
    runNowMock.mockResolvedValueOnce({
      success_deleted: 0,
      failed_deleted: 2,
      discs_deleted: 1,
      total: 2,
    });
    const { getByText } = render(RetentionSection);
    await fireEvent.click(getByText('Run cleanup now'));
    await Promise.resolve();
    await Promise.resolve();
    expect(runNowMock).toHaveBeenCalled();
    expect(get(toasts)).toContainEqual(
      expect.objectContaining({ kind: 'success', message: 'Removed 2 history entries' }),
    );
  });
});
