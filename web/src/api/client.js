const BASE = '/api/v1';

async function request(path, options = {}) {
  const url = `${BASE}${path}`;
  const isFormData = options.body instanceof FormData;
  const res = await fetch(url, {
    headers: isFormData ? options.headers : { 'Content-Type': 'application/json', ...options.headers },
    ...options,
  });

  if (res.status === 204) return null;

  const data = await res.json();
  if (!res.ok) throw new Error(data.error || `Request failed: ${res.status}`);
  return data;
}

// --- VMs ---
export const vms = {
  list:    ()           => request('/vms'),
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

export default { vms, snapshots, images, ports, host, logs };
