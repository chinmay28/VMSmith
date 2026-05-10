import createClient from 'openapi-fetch';
import { clearAuthToken, getAuthToken, requireAuth } from '../auth.js';
import type { components, paths } from './generated/schema';

const BASE = '/api/v1';

const apiClient = createClient<paths>({
  baseUrl: BASE,
});

apiClient.use({
  onRequest({ request }) {
    const token = getAuthToken();
    if (token) request.headers.set('Authorization', `Bearer ${token}`);
    return request;
  },
});

function getErrorMessage(error: unknown, fallback: string) {
  if (error && typeof error === 'object') {
    if ('message' in error && typeof error.message === 'string' && error.message) return error.message;
    if ('error' in error && typeof error.error === 'string' && error.error) return error.error;
  }
  return fallback;
}

async function unwrap(result: Promise<any>, options: { withMeta?: boolean } = {}) {
  const { data, error, response } = await result;

  if (response.status === 401) {
    const token = getAuthToken();
    const message = getErrorMessage(error, token ? 'Invalid API key' : 'API key required');
    if (token) clearAuthToken();
    requireAuth(message);
    throw new Error(message);
  }

  if (error) {
    throw new Error(getErrorMessage(error, `Request failed: ${response.status}`));
  }

  if (options.withMeta) {
    return {
      data,
      meta: {
        totalCount: Number.parseInt(response.headers.get('X-Total-Count') || '', 10) || 0,
      },
    };
  }

  return data ?? null;
}

function parseJSONSafe(text: string) {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

// --- VMs ---
export const vms = {
  list: ({ tag = '', status = '', sort = '', order = '', page, perPage }: { tag?: string; status?: string; sort?: 'id' | 'name' | 'created_at' | 'state' | ''; order?: 'asc' | 'desc' | ''; page?: number; perPage?: number } = {}) =>
    unwrap(apiClient.GET('/vms', { params: { query: { tag, status, sort: sort || undefined, order: order || undefined, page, per_page: perPage } as any } }), { withMeta: true }),
  get: (id: string) => unwrap(apiClient.GET('/vms/{vmID}', { params: { path: { vmID: id } } })),
  create: (spec: paths['/vms']['post']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.POST('/vms', { body: spec })),
  update: (id: string, patch: paths['/vms/{vmID}']['patch']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.PATCH('/vms/{vmID}', { params: { path: { vmID: id } }, body: patch })),
  clone: (id: string, name: string) =>
    unwrap(apiClient.POST('/vms/{vmID}/clone', { params: { path: { vmID: id } }, body: { name } })),
  start: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/start', { params: { path: { vmID: id } } })),
  stop: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/stop', { params: { path: { vmID: id } } })),
  forceStop: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/force-stop', { params: { path: { vmID: id } } })),
  restart: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/restart', { params: { path: { vmID: id } } })),
  reboot: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/reboot', { params: { path: { vmID: id } } })),
  suspend: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/suspend', { params: { path: { vmID: id } } })),
  resume: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/resume', { params: { path: { vmID: id } } })),
  delete: (id: string) => unwrap(apiClient.DELETE('/vms/{vmID}', { params: { path: { vmID: id } } })),
  stats: (id: string, { since = '', fields = '' }: { since?: string; fields?: string } = {}) =>
    unwrap(apiClient.GET('/vms/{vmID}/stats', {
      params: { path: { vmID: id }, query: { since: since || undefined, fields: fields || undefined } as any },
    })),
  top: ({ metric = 'cpu', limit = 5, state = 'running' }: { metric?: 'cpu' | 'mem' | 'disk_read' | 'disk_write' | 'net_rx' | 'net_tx'; limit?: number; state?: 'running' | 'all' } = {}) =>
    unwrap(apiClient.GET('/vms/stats/top', { params: { query: { metric, limit, state } } })),
};

// --- Snapshots ---
export const snapshots = {
  list: (vmId: string) => unwrap(apiClient.GET('/vms/{vmID}/snapshots', { params: { path: { vmID: vmId } } })),
  create: (vmId: string, name: string, description?: string) =>
    unwrap(
      apiClient.POST('/vms/{vmID}/snapshots', {
        params: { path: { vmID: vmId } },
        body: description ? { name, description } : { name },
      }),
    ),
  restore: (vmId: string, snapName: string) =>
    unwrap(apiClient.POST('/vms/{vmID}/snapshots/{snapName}/restore', { params: { path: { vmID: vmId, snapName } } })),
  // update edits snapshot metadata (currently only the description). Pass an
  // empty string to clear; omit the field entirely to leave it unchanged.
  update: (vmId: string, snapName: string, body: { description?: string | null }) =>
    unwrap(apiClient.PATCH('/vms/{vmID}/snapshots/{snapName}', {
      params: { path: { vmID: vmId, snapName } },
      body,
    })),
  delete: (vmId: string, snapName: string) =>
    unwrap(apiClient.DELETE('/vms/{vmID}/snapshots/{snapName}', { params: { path: { vmID: vmId, snapName } } })),
  // bulkDelete deletes multiple snapshots in a single round-trip. Pass either
  // {names: [...]} for explicit IDs or {prefix: "..."} for a prefix sweep.
  bulkDelete: (vmId: string, body: { names?: string[]; prefix?: string }) =>
    unwrap(apiClient.POST('/vms/{vmID}/snapshots/bulk_delete', {
      params: { path: { vmID: vmId } },
      body,
    })),
};

function uploadImageWithProgress(file: File, name: string, options: { description?: string; tags?: string[] } = {}, onProgress?: (progress: { loaded: number; total: number; percent: number }) => void) {
  const token = getAuthToken();
  const fd = new FormData();
  fd.append('file', file);
  if (name) fd.append('name', name);
  if (options.description) fd.append('description', options.description);
  if (options.tags && options.tags.length > 0) {
    fd.append('tags', options.tags.join(','));
  }

  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', `${BASE}/images/upload`);
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);

    xhr.upload.onprogress = (event) => {
      if (!onProgress || !event.lengthComputable) return;
      onProgress({
        loaded: event.loaded,
        total: event.total,
        percent: Math.min(100, Math.round((event.loaded / event.total) * 100)),
      });
    };

    xhr.onload = () => {
      const data = parseJSONSafe(xhr.responseText);
      if (xhr.status === 401) {
        const message = getErrorMessage(data, token ? 'Invalid API key' : 'API key required');
        if (token) clearAuthToken();
        requireAuth(message);
        reject(new Error(message));
        return;
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(new Error(getErrorMessage(data, `Request failed: ${xhr.status}`)));
        return;
      }
      resolve(data);
    };

    xhr.onerror = () => reject(new Error('Upload failed'));
    xhr.send(fd);
  });
}

// --- Images ---
export const images = {
  list: ({ page, perPage, tag = '' }: { page?: number; perPage?: number; tag?: string } = {}) =>
    unwrap(apiClient.GET('/images', { params: { query: { page, per_page: perPage, tag: tag || undefined } as any } }), { withMeta: true }),
  create: (vmId: string, name: string, options: { description?: string; tags?: string[] } = {}) =>
    unwrap(apiClient.POST('/images', { body: { vm_id: vmId, name, description: options.description, tags: options.tags } })),
  update: (id: string, patch: { description?: string; tags?: string[] }) =>
    unwrap(apiClient.PATCH('/images/{imageID}', { params: { path: { imageID: id } }, body: patch })),
  upload: (file: File, name: string, options: { description?: string; tags?: string[] } = {}, onProgress?: (progress: { loaded: number; total: number; percent: number }) => void) =>
    uploadImageWithProgress(file, name, options, onProgress),
  delete: (id: string) => unwrap(apiClient.DELETE('/images/{imageID}', { params: { path: { imageID: id } } })),
  // bulkDelete deletes multiple images in a single round-trip. Pass either
  // {ids: [...]} for explicit IDs or {tag: "..."} to delete every image
  // carrying that tag (case-insensitive).
  bulkDelete: (body: { ids?: string[]; tag?: string }) =>
    unwrap(apiClient.POST('/images/bulk_delete', { body })),
  downloadUrl: (id: string) => `${BASE}/images/${id}/download`,
};

// --- Port Forwards ---
export const ports = {
  list: (vmId: string) => unwrap(apiClient.GET('/vms/{vmID}/ports', { params: { path: { vmID: vmId } } })),
  add: (
    vmId: string,
    hostPort: number,
    guestPort: number,
    protocol: 'tcp' | 'udp' = 'tcp',
    description?: string,
  ) => {
    const body: components['schemas']['AddPortRequest'] = {
      host_port: hostPort,
      guest_port: guestPort,
      protocol,
    };
    if (description) {
      body.description = description;
    }
    return unwrap(apiClient.POST('/vms/{vmID}/ports', {
      params: { path: { vmID: vmId } },
      body,
    }));
  },
  remove: (vmId: string, portId: string) =>
    unwrap(apiClient.DELETE('/vms/{vmID}/ports/{portID}', { params: { path: { vmID: vmId, portID: portId } } })),
};

// --- Templates ---
export const templates = {
  list: ({ page, perPage, tag }: { page?: number; perPage?: number; tag?: string } = {}) =>
    unwrap(apiClient.GET('/templates', { params: { query: { page, per_page: perPage, tag } as any } }), { withMeta: true }),
  create: (spec: paths['/templates']['post']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.POST('/templates', { body: spec })),
  update: (id: string, patch: { description?: string; tags?: string[] }) =>
    unwrap(apiClient.PATCH('/templates/{templateID}' as any, { params: { path: { templateID: id } }, body: patch } as any)),
  delete: (id: string) => unwrap(apiClient.DELETE('/templates/{templateID}', { params: { path: { templateID: id } } })),
};

// --- Host ---
export const host = {
  interfaces: () => unwrap(apiClient.GET('/host/interfaces')),
  stats: () => unwrap(apiClient.GET('/host/stats')),
};

// --- Quotas ---
export const quotas = {
  usage: () => unwrap(apiClient.GET('/quotas/usage')),
};

// --- Logs ---
export const logs = {
  list: ({ level = 'debug', page, perPage, limit, since = '', source = '' }: { level?: string; page?: number; perPage?: number; limit?: number; since?: string; source?: string } = {}) =>
    unwrap(apiClient.GET('/logs', {
      params: { query: { level, page, per_page: perPage, limit, since, source } },
    }), { withMeta: true }),
};

// --- System ---
// Build info — public endpoint at /api/version (outside the authenticated
// /api/v1 tree).  Fetched by the layout footer before any auth token is
// available, so we issue a plain fetch instead of going through apiClient.
export const system = {
  version: async (): Promise<{ version: string; commit: string; build_date: string; go_version: string; os: string; arch: string }> => {
    const resp = await fetch('/api/version', { headers: { Accept: 'application/json' } });
    if (!resp.ok) throw new Error(`/api/version returned ${resp.status}`);
    return resp.json();
  },
};

// --- Webhooks ---
export const webhooks = {
  list: () => unwrap(apiClient.GET('/webhooks', {})),
  create: (spec: paths['/webhooks']['post']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.POST('/webhooks', { body: spec })),
  delete: (id: string) =>
    unwrap(apiClient.DELETE('/webhooks/{webhookID}', { params: { path: { webhookID: id } } })),
  test: (id: string) =>
    unwrap(apiClient.POST('/webhooks/{webhookID}/test', { params: { path: { webhookID: id } } })),
};

// --- Events ---
// Filter params line up 1:1 with GET /api/v1/events query params.
export const events = {
  list: ({ vmId = '', type = '', source = '', severity = '', since = '', until = '', page, perPage }: {
    vmId?: string;
    type?: string;
    source?: 'libvirt' | 'app' | 'system' | '';
    severity?: 'info' | 'warn' | 'error' | '';
    since?: string;
    until?: string;
    page?: number;
    perPage?: number;
  } = {}) =>
    unwrap(apiClient.GET('/events', {
      params: { query: { vm_id: vmId, type, source: source || undefined, severity: severity || undefined, since, until, page, per_page: perPage } as any },
    }), { withMeta: true }),
};

export default { vms, snapshots, images, templates, ports, host, quotas, logs, events, system, webhooks };
