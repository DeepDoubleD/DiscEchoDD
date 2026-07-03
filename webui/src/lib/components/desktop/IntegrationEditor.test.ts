import '@testing-library/jest-dom/vitest';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, fireEvent, waitFor } from '@testing-library/svelte';
import IntegrationEditor from './IntegrationEditor.svelte';
import type { IntegrationDetail } from '$lib/wire';

const baseDetail: IntegrationDetail = {
  name: 'igdb',
  source: 'env',
  configured: true,
  fields_present: ['client_id', 'client_secret'],
  values: { client_id: 'env-id', client_secret: 'env-secret' },
};

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as unknown as Response;
}

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  fetchMock = vi.fn();
  fetchMock.mockResolvedValue(jsonResponse({ ok: true }));
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('IntegrationEditor', () => {
  it('renders source pill text reflecting the supplied detail', () => {
    const { container } = render(IntegrationEditor, {
      props: {
        detail: baseDetail,
        fields: [
          { name: 'client_id', label: 'Client ID', secret: false },
          { name: 'client_secret', label: 'Client secret', secret: true },
        ],
        testable: true,
      },
    });
    expect(container.textContent).toContain('Configured (env)');
  });

  it('does not prefill secret fields into the editor', async () => {
    const { container, getByTestId } = render(IntegrationEditor, {
      props: {
        detail: baseDetail,
        fields: [
          { name: 'client_id', label: 'Client ID', secret: false },
          { name: 'client_secret', label: 'Client secret', secret: true },
        ],
        testable: true,
      },
    });
    expect(container.textContent).toContain('•••');
    await fireEvent.click(getByTestId('integration-edit'));
    // Secrets are masked by the server and never echoed back — the input
    // starts blank ("leave blank to keep current"), while the non-secret
    // public field is prefilled.
    const secretInput = getByTestId('integration-field-client_secret') as HTMLInputElement;
    expect(secretInput.value).toBe('');
    expect(secretInput.placeholder).toBe('leave blank to keep current');
    const idInput = getByTestId('integration-field-client_id') as HTMLInputElement;
    expect(idInput.value).toBe('env-id');
  });

  it('sends a partial PUT containing only the changed field', async () => {
    const { getByTestId } = render(IntegrationEditor, {
      props: {
        detail: baseDetail,
        fields: [
          { name: 'client_id', label: 'Client ID', secret: false },
          { name: 'client_secret', label: 'Client secret', secret: true },
        ],
        testable: true,
      },
    });
    await fireEvent.click(getByTestId('integration-edit'));
    const idInput = getByTestId('integration-field-client_id') as HTMLInputElement;
    idInput.value = 'new-id';
    await fireEvent.input(idInput);
    fetchMock.mockResolvedValueOnce(
      jsonResponse({
        name: 'igdb',
        source: 'ui',
        configured: true,
        fields_present: ['client_id', 'client_secret'],
        values: { client_id: 'new-id', client_secret: 'env-secret' },
      }),
    );
    await fireEvent.click(getByTestId('integration-save'));
    await waitFor(() => {
      const calls = fetchMock.mock.calls;
      const putCall = calls.find(([, init]) => (init as RequestInit)?.method === 'PUT');
      expect(putCall).toBeDefined();
      const body = JSON.parse((putCall![1] as RequestInit).body as string);
      // Only client_id was touched — partial PUT must omit client_secret.
      expect(body).toEqual({ client_id: 'new-id' });
    });
  });

  it('shows the Test button when testable=true', async () => {
    const { queryByTestId, getByTestId } = render(IntegrationEditor, {
      props: { detail: baseDetail, fields: [], testable: true },
    });
    await fireEvent.click(getByTestId('integration-edit'));
    expect(queryByTestId('integration-test')).not.toBeNull();
  });

  it('hides the Test button when testable=false', async () => {
    const { queryByTestId, getByTestId } = render(IntegrationEditor, {
      props: { detail: baseDetail, fields: [], testable: false },
    });
    await fireEvent.click(getByTestId('integration-edit'));
    expect(queryByTestId('integration-test')).toBeNull();
  });

  it('renders the Test result inline on failure', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({
        ok: false,
        error: 'Unauthorized',
        http_status: 401,
      }),
    );
    const { getByTestId, container } = render(IntegrationEditor, {
      props: {
        detail: baseDetail,
        fields: [{ name: 'client_id', label: 'Client ID', secret: false }],
        testable: true,
      },
    });
    await fireEvent.click(getByTestId('integration-edit'));
    await fireEvent.click(getByTestId('integration-test'));
    await waitFor(() => {
      expect(container.textContent).toContain('Unauthorized');
    });
  });
});
