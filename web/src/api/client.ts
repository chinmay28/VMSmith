import createClient from 'openapi-fetch';
import { clearAuthToken, getAuthToken, requireAuth } from '../auth.js';
import type { paths } from './generated/schema';

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
  list: ({ tag = '', status = '', page, perPage }: { tag?: string; status?: string; page?: number; perPage?: number } = {}) =>
    unwrap(apiClient.GET('/vms', { params: { query: { tag, status, page, per_page: perPage } } }), { withMeta: true }),
  get: (id: string) => unwrap(apiClient.GET('/vms/{vmID}', { params: { path: { vmID: id } } })),
  create: (spec: paths['/vms']['post']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.POST('/vms', { body: spec })),
  update: (id: string, patch: paths['/vms/{vmID}']['patch']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.PATCH('/vms/{vmID}', { params: { path: { vmID: id } }, body: patch })),
  clone: (id: string, name: string) =>
    unwrap(apiClient.POST('/vms/{vmID}/clone', { params: { path: { vmID: id } }, body: { name } })),
  start: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/start', { params: { path: { vmID: id } } })),
  stop: (id: string) => unwrap(apiClient.POST('/vms/{vmID}/stop', { params: { path: { vmID: id } } })),
  delete: (id: string) => unwrap(apiClient.DELETE('/vms/{vmID}', { params: { path: { vmID: id } } })),
};

// --- Snapshots ---
export const snapshots = {
  list: (vmId: string) => unwrap(apiClient.GET('/vms/{vmID}/snapshots', { params: { path: { vmID: vmId } } })),
  create: (vmId: string, name: string) =>
    unwrap(apiClient.POST('/vms/{vmID}/snapshots', { params: { path: { vmID: vmId } }, body: { name } })),
  restore: (vmId: string, snapName: string) =>
    unwrap(apiClient.POST('/vms/{vmID}/snapshots/{snapName}/restore', { params: { path: { vmID: vmId, snapName } } })),
  delete: (vmId: string, snapName: string) =>
    unwrap(apiClient.DELETE('/vms/{vmID}/snapshots/{snapName}', { params: { path: { vmID: vmId, snapName } } })),
};

function uploadImageWithProgress(file: File, name: string, onProgress?: (progress: { loaded: number; total: number; percent: number }) => void) {
  const token = getAuthToken();
  const fd = new FormData();
  fd.append('file', file);
  if (name) fd.append('name', name);

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
  list: ({ page, perPage }: { page?: number; perPage?: number } = {}) =>
    unwrap(apiClient.GET('/images', { params: { query: { page, per_page: perPage } } }), { withMeta: true }),
  create: (vmId: string, name: string) => unwrap(apiClient.POST('/images', { body: { vm_id: vmId, name } })),
  upload: (file: File, name: string, onProgress?: (progress: { loaded: number; total: number; percent: number }) => void) =>
    uploadImageWithProgress(file, name, onProgress),
  delete: (id: string) => unwrap(apiClient.DELETE('/images/{imageID}', { params: { path: { imageID: id } } })),
  downloadUrl: (id: string) => `${BASE}/images/${id}/download`,
};

// --- Port Forwards ---
export const ports = {
  list: (vmId: string) => unwrap(apiClient.GET('/vms/{vmID}/ports', { params: { path: { vmID: vmId } } })),
  add: (vmId: string, hostPort: number, guestPort: number, protocol: 'tcp' | 'udp' = 'tcp') =>
    unwrap(apiClient.POST('/vms/{vmID}/ports', {
      params: { path: { vmID: vmId } },
      body: { host_port: hostPort, guest_port: guestPort, protocol },
    })),
  remove: (vmId: string, portId: string) =>
    unwrap(apiClient.DELETE('/vms/{vmID}/ports/{portID}', { params: { path: { vmID: vmId, portID: portId } } })),
};

// --- Templates ---
export const templates = {
  list: ({ page, perPage }: { page?: number; perPage?: number } = {}) =>
    unwrap(apiClient.GET('/templates', { params: { query: { page, per_page: perPage } } }), { withMeta: true }),
  create: (spec: paths['/templates']['post']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.POST('/templates', { body: spec })),
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

export default { vms, snapshots, images, templates, ports, host, quotas, logs, events };
