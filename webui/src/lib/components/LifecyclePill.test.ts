import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/svelte';
import type { DiscLifecycleState } from '$lib/wire';
import LifecyclePill from './LifecyclePill.svelte';

describe('LifecyclePill', () => {
  it('renders the human label for each lifecycle state', () => {
    const cases: Array<[DiscLifecycleState, string]> = [
      ['awaiting_decision', 'Awaiting decision'],
      ['ripping', 'Ripping'],
      ['awaiting_encode', 'Awaiting encode'],
      ['encoding', 'Encoding'],
      ['done', 'Done'],
      ['failed', 'Failed'],
      ['cancelled', 'Cancelled'],
      ['interrupted', 'Interrupted'],
    ];
    for (const [state, label] of cases) {
      const { getByTestId, unmount } = render(LifecyclePill, {
        props: { state },
      });
      const el = getByTestId('lifecycle-pill');
      expect(el.textContent?.trim()).toBe(label);
      expect(el.dataset.state).toBe(state);
      unmount();
    }
  });
});
