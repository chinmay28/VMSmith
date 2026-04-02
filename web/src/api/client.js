const BASE = '/api/v1';
const API_KEY_STORAGE_KEY = 'vmsmith.apiKey';

let apiKey = typeof window !== 'undefined'
  ? window.localStorage.getItem(API_KEY_STORAGE_KEY) || ''
  : '';

export function getAPIKey() {
  return apiKey;
}

export function setAPIKey(nextKey) {
  apiKey = (nextKey || '').trim();
  if (typeof window !== 'undefined') {
    if (apiKey) {
      window.localStorage.setItem(API_KEY_STORAGE_KEY, apiKey);
    } else {
      window.localStorage.removeItem(API_KEY_STORAGE_KEY);
    }
  }
}

export class APIError extends Error {
  constructor(message, { status = 0, code = '', payload = null } = {}) {
    super(message);
    this.name = 'APIError';
    this.status = status;
    this.code = code;
    this.payload = payload;
  }
}

function buildHeaders(headers = {}, isFormData = false) {
  const nextHeaders = isFormData ? { ...headers } : { 'Content-Type': 'application/json', ...headers };
  if (apiKey) {
    nextHeaders.Authorization = `Bearer ${apiKey}`;
  }
  return nextHeaders;
}

async function request(path, options = {}) {
  const url = `${BASE}${path}`;
  const isFormData = options.body instanceof FormData;
  const res = await fetch(url, {
    ...options,
    headers: buildHeaders(options.headers, isFormData),
  });

  if (res.status === 204) return null;

  const data = await res.json();
  if (!res.ok) {
    throw new APIError(data.message || data.error || `Request failed: ${res.status}`, {
      status: res.status,
      code: data.code || '',
      payload: data,
    });
  }
  return data;
}

export async function checkAuth() {
  try {
    await request('/vms');
    return { ok: true, requiresAuth: false };
  } catch (err) {
    if (err instanceof APIError && err.status === 401) {
      return { ok: false, requiresAuth: true, error: err.message };
    }
    throw err;
  }
}

// --- VMs ---
export const vms = {
  list:    (tag = '')   => request(tag ? `/vms?tag=${encodeURIComponent(tag)}` : '/vms'),
  get:     (id)         => request(`/vms/${id}`),
  create:  (spec)       => request('/vms', { method: 'POST', body: JSON.stringify(spec) }),
  update:  (id, patch)  => request(`/vms/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  start:   (id)         => request(`/vms/${id}/start`, { method: 'POST' }),
  stop:    (id)         => request(`/vms/${id}/stop`, { method: 'POST' }),
  delete:  (id)         => request(`/vms/${id}`, { method: 'DELETE' }),
};

// --- Snapshots ---
export const snapshots = {
  list:    (vmId)             => request(`/vms/${vmId}/snapshots`),
  create:  (vmId, name)      => request(`/vms/${vmId}/snapshots`, { method: 'POST', body: JSON.stringify({ name }) }),
  restore: (vmId, snapName)  => request(`/vms/${vmId}/snapshots/${snapName}/restore`, { method: 'POST' }),
  delete:  (vmId, snapName)  => request(`/vms/${vmId}/snapshots/${snapName}`, { method: 'DELETE' }),
};

// --- Images ---
export const images = {
  list:     ()            => request('/images'),
  create:   (vmId, name)  => request('/images', { method: 'POST', body: JSON.stringify({ vm_id: vmId, name }) }),
  upload:   (file, name)  => {
    const fd = new FormData();
    fd.append('file', file);
    if (name) fd.append('name', name);
    return request('/images/upload', { method: 'POST', body: fd });
  },
  delete:   (id)          => request(`/images/${id}`, { method: 'DELETE' }),
  downloadUrl: (id)       => `${BASE}/images/${id}/download`,
};

// --- Port Forwards ---
export const ports = {
  list:   (vmId)                          => request(`/vms/${vmId}/ports`),
  add:    (vmId, hostPort, guestPort, protocol = 'tcp') =>
    request(`/vms/${vmId}/ports`, {
      method: 'POST',
      body: JSON.stringify({ host_port: hostPort, guest_port: guestPort, protocol }),
    }),
  remove: (vmId, portId) => request(`/vms/${vmId}/ports/${portId}`, { method: 'DELETE' }),
};

// --- Host ---
export const host = {
  interfaces: () => request('/host/interfaces'),
};

// --- Logs ---
export const logs = {
  list: ({ level = 'debug', limit = 200, since = '', source = '' } = {}) => {
    const params = new URLSearchParams({ level, limit });
    if (since) params.set('since', since);
    if (source) params.set('source', source);
    return request(`/logs?${params}`);
  },
};

export default { vms, snapshots, images, ports, host, logs, checkAuth, getAPIKey, setAPIKey };
