import { clearAuthToken, getAuthToken, requireAuth } from '../auth.js';

const BASE = '/api/v1';

function parseJSONSafe(text) {
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return null;
  }
}

async function request(path, options = {}) {
  const url = `${BASE}${path}`;
  const isFormData = options.body instanceof FormData;
  const token = getAuthToken();
  const headers = {
    ...(isFormData ? {} : { 'Content-Type': 'application/json' }),
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(options.headers || {}),
  };

  const res = await fetch(url, {
    ...options,
    headers,
  });

  if (res.status === 204) return null;

  let data = null;
  try {
    data = await res.json();
  } catch {
    data = null;
  }

  if (res.status === 401) {
    const message = data?.error || (token ? 'Invalid API key' : 'API key required');
    if (token) clearAuthToken();
    requireAuth(message);
    throw new Error(message);
  }

  if (!res.ok) throw new Error(data?.error || `Request failed: ${res.status}`);

  if (options.withMeta) {
    return {
      data,
      meta: {
        totalCount: Number.parseInt(res.headers.get('X-Total-Count') || '', 10) || 0,
      },
    };
  }

  return data;
}

function withQuery(path, params = {}) {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    query.set(key, String(value));
  }
  const suffix = query.toString();
  return suffix ? `${path}?${suffix}` : path;
}

// --- VMs ---
export const vms = {
  list:    ({ tag = '', status = '', page, perPage } = {}) => request(withQuery('/vms', {
    tag,
    status,
    page,
    per_page: perPage,
  }), { withMeta: true }),
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

function uploadImageWithProgress(file, name, onProgress) {
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
        const message = data?.error || (token ? 'Invalid API key' : 'API key required');
        if (token) clearAuthToken();
        requireAuth(message);
        reject(new Error(message));
        return;
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(new Error(data?.error || `Request failed: ${xhr.status}`));
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
  list:     ({ page, perPage } = {}) => request(withQuery('/images', { page, per_page: perPage }), { withMeta: true }),
  create:   (vmId, name)  => request('/images', { method: 'POST', body: JSON.stringify({ vm_id: vmId, name }) }),
  upload:   (file, name, onProgress) => uploadImageWithProgress(file, name, onProgress),
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

// --- Quotas ---
export const quotas = {
  usage: () => request('/quotas/usage'),
};

// --- Logs ---
export const logs = {
  list: ({ level = 'debug', page, perPage, limit, since = '', source = '' } = {}) => request(withQuery('/logs', {
    level,
    page,
    per_page: perPage,
    limit,
    since,
    source,
  }), { withMeta: true }),
};

export default { vms, snapshots, images, ports, host, quotas, logs };
