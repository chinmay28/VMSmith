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
  list: ({ tag = '', status = '', search = '', image = '', defaultUser = '', osType = '', osVariant = '', firmware = '', diskBus = '', nicModel = '', machine = '', clockOffset = '', gpu = '', network = '', prefix = '', natStaticIp = '', natGateway = '', ip = '', autoStart = '', locked = '', since = '', until = '', minCpus = '', maxCpus = '', minRamMb = '', maxRamMb = '', minDiskGb = '', maxDiskGb = '', sort = '', order = '', page, perPage }: { tag?: string; status?: string; search?: string; image?: string; defaultUser?: string; osType?: 'linux' | 'windows' | ''; osVariant?: 'windows-10' | 'windows-11' | 'windows-server-2019' | 'windows-server-2022' | 'windows-server-2025' | ''; firmware?: 'bios' | 'uefi' | 'ovmf' | ''; diskBus?: 'virtio' | 'sata' | ''; nicModel?: 'virtio' | 'e1000e' | ''; machine?: string; clockOffset?: 'utc' | 'localtime' | ''; gpu?: string; network?: string; prefix?: string; natStaticIp?: string; natGateway?: string; ip?: string; autoStart?: 'true' | 'false' | ''; locked?: 'true' | 'false' | ''; since?: string; until?: string; minCpus?: string; maxCpus?: string; minRamMb?: string; maxRamMb?: string; minDiskGb?: string; maxDiskGb?: string; sort?: 'id' | 'name' | 'created_at' | 'state' | ''; order?: 'asc' | 'desc' | ''; page?: number; perPage?: number } = {}) =>
    unwrap(apiClient.GET('/vms', { params: { query: { tag, status, search: search || undefined, image: image || undefined, default_user: defaultUser || undefined, os_type: osType || undefined, os_variant: osVariant || undefined, firmware: firmware || undefined, disk_bus: diskBus || undefined, nic_model: nicModel || undefined, machine: machine || undefined, clock_offset: clockOffset || undefined, gpu: gpu || undefined, network: network || undefined, prefix: prefix || undefined, nat_static_ip: natStaticIp || undefined, nat_gateway: natGateway || undefined, ip: ip || undefined, auto_start: autoStart || undefined, locked: locked || undefined, since: since || undefined, until: until || undefined, min_cpus: minCpus || undefined, max_cpus: maxCpus || undefined, min_ram_mb: minRamMb || undefined, max_ram_mb: maxRamMb || undefined, min_disk_gb: minDiskGb || undefined, max_disk_gb: maxDiskGb || undefined, sort: sort || undefined, order: order || undefined, page, per_page: perPage } as any } }), { withMeta: true }),
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
  issueConsoleTicket: (id: string, intent?: 'vnc' | 'serial') =>
    unwrap(apiClient.POST('/vms/{vmID}/console/ticket', { params: { path: { vmID: id }, query: intent ? { intent } : undefined } as any })),
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
  list: (
    vmId: string,
    opts: { sort?: 'id' | 'name' | 'created_at'; order?: 'asc' | 'desc'; search?: string; tag?: string; prefix?: string; since?: string; until?: string } = {},
  ) =>
    unwrap(
      apiClient.GET('/vms/{vmID}/snapshots', {
        params: {
          path: { vmID: vmId },
          query: {
            sort: opts.sort,
            order: opts.order,
            search: opts.search || undefined,
            tag: opts.tag || undefined,
            prefix: opts.prefix || undefined,
            since: opts.since || undefined,
            until: opts.until || undefined,
          } as any,
        },
      }),
    ),
  create: (vmId: string, name: string, description?: string, tags?: string[]) => {
    const body: { name: string; description?: string; tags?: string[] } = { name };
    if (description) body.description = description;
    if (tags && tags.length > 0) body.tags = tags;
    return unwrap(
      apiClient.POST('/vms/{vmID}/snapshots', {
        params: { path: { vmID: vmId } },
        body: body as any,
      }),
    );
  },
  restore: (vmId: string, snapName: string) =>
    unwrap(apiClient.POST('/vms/{vmID}/snapshots/{snapName}/restore', { params: { path: { vmID: vmId, snapName } } })),
  // update edits snapshot metadata.  description and tags follow pointer
  // semantics: omit a field to leave it unchanged; pass an empty string
  // ("" for description, [] for tags) to clear.
  update: (vmId: string, snapName: string, body: { description?: string | null; tags?: string[] | null }) =>
    unwrap(apiClient.PATCH('/vms/{vmID}/snapshots/{snapName}', {
      params: { path: { vmID: vmId, snapName } },
      body: body as any,
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
  list: ({ page, perPage, tag = '', sourceVM = '', search = '', prefix = '', since = '', until = '', minSize = '', maxSize = '', sort, order }: { page?: number; perPage?: number; tag?: string; sourceVM?: string; search?: string; prefix?: string; since?: string; until?: string; minSize?: string; maxSize?: string; sort?: string; order?: string } = {}) =>
    unwrap(apiClient.GET('/images', { params: { query: { page, per_page: perPage, tag: tag || undefined, source_vm: sourceVM || undefined, search: search || undefined, prefix: prefix || undefined, since: since || undefined, until: until || undefined, min_size: minSize || undefined, max_size: maxSize || undefined, sort, order } as any } }), { withMeta: true }),
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
  list: (
    vmId: string,
    opts: { sort?: string; order?: string; search?: string; tag?: string; protocol?: string; minHostPort?: string; maxHostPort?: string; minGuestPort?: string; maxGuestPort?: string; guestIp?: string; page?: number; perPage?: number } = {},
  ) => {
    const query: Record<string, string | number> = {};
    if (opts.sort)         query.sort           = opts.sort;
    if (opts.order)        query.order          = opts.order;
    if (opts.search)       query.search         = opts.search;
    if (opts.tag)          query.tag            = opts.tag;
    if (opts.protocol)     query.protocol       = opts.protocol;
    if (opts.minHostPort)  query.min_host_port  = opts.minHostPort;
    if (opts.maxHostPort)  query.max_host_port  = opts.maxHostPort;
    if (opts.minGuestPort) query.min_guest_port = opts.minGuestPort;
    if (opts.maxGuestPort) query.max_guest_port = opts.maxGuestPort;
    if (opts.guestIp)      query.guest_ip       = opts.guestIp;
    if (opts.page)         query.page           = opts.page;
    if (opts.perPage)     query.per_page      = opts.perPage;
    return unwrap(apiClient.GET('/vms/{vmID}/ports', {
      params: { path: { vmID: vmId }, query: query as any },
    } as any), { withMeta: true });
  },
  add: (
    vmId: string,
    hostPort: number,
    guestPort: number,
    protocol: 'tcp' | 'udp' = 'tcp',
    description?: string,
    tags?: string[],
  ) => {
    const body: components['schemas']['AddPortRequest'] & { tags?: string[] } = {
      host_port: hostPort,
      guest_port: guestPort,
      protocol,
    };
    if (description) {
      body.description = description;
    }
    if (tags && tags.length > 0) {
      body.tags = tags;
    }
    return unwrap(apiClient.POST('/vms/{vmID}/ports', {
      params: { path: { vmID: vmId } },
      body,
    } as any));
  },
  remove: (vmId: string, portId: string) =>
    unwrap(apiClient.DELETE('/vms/{vmID}/ports/{portID}', { params: { path: { vmID: vmId, portID: portId } } })),
  update: (vmId: string, portId: string, patch: { description?: string; tags?: string[] }) =>
    unwrap(apiClient.PATCH('/vms/{vmID}/ports/{portID}' as any, {
      params: { path: { vmID: vmId, portID: portId } },
      body: patch,
    } as any)),
  bulkDelete: (vmId: string, args: { ids?: string[]; protocol?: 'tcp' | 'udp' }) => {
    const body: components['schemas']['BulkDeletePortsRequest'] = {};
    if (args.ids && args.ids.length) body.ids = args.ids;
    if (args.protocol) body.protocol = args.protocol;
    return unwrap(apiClient.POST('/vms/{vmID}/ports/bulk_delete', {
      params: { path: { vmID: vmId } },
      body,
    }));
  },
};

// --- Templates ---
export const templates = {
  list: ({ page, perPage, tag, search, image, defaultUser, osType, osVariant, network, prefix, since, until, minCpus, maxCpus, minRamMb, maxRamMb, minDiskGb, maxDiskGb, sort, order }: { page?: number; perPage?: number; tag?: string; search?: string; image?: string; defaultUser?: string; osType?: 'linux' | 'windows' | ''; osVariant?: 'windows-10' | 'windows-11' | 'windows-server-2019' | 'windows-server-2022' | 'windows-server-2025' | ''; network?: string; prefix?: string; since?: string; until?: string; minCpus?: string; maxCpus?: string; minRamMb?: string; maxRamMb?: string; minDiskGb?: string; maxDiskGb?: string; sort?: string; order?: string } = {}) =>
    unwrap(apiClient.GET('/templates', { params: { query: { page, per_page: perPage, tag, search, image, default_user: defaultUser || undefined, os_type: osType || undefined, os_variant: osVariant || undefined, network: network || undefined, prefix: prefix || undefined, since: since || undefined, until: until || undefined, min_cpus: minCpus || undefined, max_cpus: maxCpus || undefined, min_ram_mb: minRamMb || undefined, max_ram_mb: maxRamMb || undefined, min_disk_gb: minDiskGb || undefined, max_disk_gb: maxDiskGb || undefined, sort, order } as any } } as any), { withMeta: true }),
  create: (spec: paths['/templates']['post']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.POST('/templates', { body: spec })),
  update: (id: string, patch: { description?: string; tags?: string[] }) =>
    unwrap(apiClient.PATCH('/templates/{templateID}' as any, { params: { path: { templateID: id } }, body: patch } as any)),
  delete: (id: string) => unwrap(apiClient.DELETE('/templates/{templateID}', { params: { path: { templateID: id } } })),
  // bulkDelete deletes multiple templates in a single round-trip. Pass either
  // {ids: [...]} for explicit IDs or {tag: "..."} to delete every template
  // carrying that tag (case-insensitive). Mirrors images.bulkDelete (2.3.6).
  bulkDelete: (body: { ids?: string[]; tag?: string }) =>
    unwrap(apiClient.POST('/templates/bulk_delete', { body })),
};

// --- Host ---
export const host = {
  interfaces: () => unwrap(apiClient.GET('/host/interfaces')),
  gpus: () => unwrap(apiClient.GET('/host/gpus')),
  stats: () => unwrap(apiClient.GET('/host/stats')),
  // Multi-host overview (5.5.4): one row per managed libvirt host.
  list: () => unwrap(apiClient.GET('/hosts' as any)),
};

// --- Quotas ---
export const quotas = {
  usage: () => unwrap(apiClient.GET('/quotas/usage')),
};

// --- Logs ---
export const logs = {
  // `sort` / `order` whitelist mirrors the daemon: sort one of
  // timestamp|level|source (default timestamp); order asc|desc (default
  // asc — preserves the legacy oldest-first contract).  Empty/undefined
  // omits the param so the daemon's defaults apply.
  list: ({ level = 'debug', page, perPage, limit, since = '', until = '', source = '', vmId = '', search = '', sort = '', order = '' }: { level?: string; page?: number; perPage?: number; limit?: number; since?: string; until?: string; source?: string; vmId?: string; search?: string; sort?: 'timestamp' | 'level' | 'source' | ''; order?: 'asc' | 'desc' | '' } = {}) =>
    unwrap(apiClient.GET('/logs', {
      params: { query: { level, page, per_page: perPage, limit, since: since || undefined, until: until || undefined, source, vm_id: vmId || undefined, search, sort: sort || undefined, order: order || undefined } as any },
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
  // The `search` param is a case-insensitive substring filter applied
  // server-side across each webhook's URL and event_types. Empty/undefined
  // omits the param so the daemon returns every webhook. Mirrors the
  // pattern used for VMs (2.2.13), images (5.4.9), and events (4.2.20).
  //
  // The `eventType` param is a case-insensitive exact-match against entries
  // in each webhook's `event_types` filter list. Catch-all webhooks (empty
  // event_types) are NOT matched — mirrors the bulk_delete event_type
  // selector semantics. Empty/undefined omits the param. (5.4.26)
  //
  // `sort` / `order` whitelist mirrors the daemon: sort one of id|url|
  // created_at|last_delivery_at (default id); order asc|desc (default asc).
  // Empty/undefined omits the param so the daemon's defaults apply.
  //
  // `deliveryStatus` filters by the webhook's most-recent delivery
  // classification: 'never' (no attempt yet), 'healthy' (last attempt was
  // 2xx + no error), 'failing' (last attempt did not meet the healthy
  // contract). Whitespace/empty omits the param. (5.4.35)
  //
  // `active` is a tristate boolean exact-match on the webhook's active flag:
  // 'true' (only live webhooks), 'false' (only disabled webhooks), '' (no
  // filter). Mirrors the VM list autoStart/locked tristate filters. (5.4.37)
  //
  // `lastDeliverySince`/`lastDeliveryUntil` form an inclusive time-range
  // filter on the webhook's `last_delivery_at`. Never-delivered webhooks
  // (zero `last_delivery_at`) are filtered OUT whenever either bound is
  // set — use `deliveryStatus: 'never'` when the intent is to find
  // never-delivered webhooks. Empty/undefined disables the bound. (5.4.61)
  //
  // `urlPrefix` is a case-insensitive HasPrefix(wh.URL, value) filter.
  // Empty/undefined omits the param. Closes the receiver-cohort operator
  // queries that `search` (substring across URL + description + event_types
  // + tags) can answer only with noisy fuzzy matches. (5.4.83)
  list: ({ search = '', tag = '', eventType = '', deliveryStatus = '', active = '', urlPrefix = '', since = '', until = '', lastDeliverySince = '', lastDeliveryUntil = '', sort = '', order = '', page, perPage }: { search?: string; tag?: string; eventType?: string; deliveryStatus?: 'never' | 'healthy' | 'failing' | ''; active?: 'true' | 'false' | ''; urlPrefix?: string; since?: string; until?: string; lastDeliverySince?: string; lastDeliveryUntil?: string; sort?: 'id' | 'url' | 'created_at' | 'last_delivery_at' | ''; order?: 'asc' | 'desc' | ''; page?: number; perPage?: number } = {}) =>
    unwrap(apiClient.GET('/webhooks', { params: { query: { search: search || undefined, tag: tag || undefined, event_type: eventType || undefined, delivery_status: deliveryStatus || undefined, active: active || undefined, url_prefix: urlPrefix || undefined, since: since || undefined, until: until || undefined, last_delivery_since: lastDeliverySince || undefined, last_delivery_until: lastDeliveryUntil || undefined, sort: sort || undefined, order: order || undefined, page, per_page: perPage } as any } }), { withMeta: true }),
  create: (spec: paths['/webhooks']['post']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.POST('/webhooks', { body: spec })),
  update: (id: string, spec: paths['/webhooks/{webhookID}']['patch']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.PATCH('/webhooks/{webhookID}', { params: { path: { webhookID: id } }, body: spec })),
  delete: (id: string) =>
    unwrap(apiClient.DELETE('/webhooks/{webhookID}', { params: { path: { webhookID: id } } })),
  // bulkDelete deletes multiple webhooks in a single round-trip. Pass either
  // {ids: [...]} for explicit IDs or {event_type: "..."} to sweep every
  // webhook subscribed to that exact event type. Catch-all webhooks
  // (empty event_types) are not swept by the categorical selector.
  bulkDelete: (body: { ids?: string[]; event_type?: string }) =>
    unwrap(apiClient.POST('/webhooks/bulk_delete', { body })),
  test: (id: string) =>
    unwrap(apiClient.POST('/webhooks/{webhookID}/test', { params: { path: { webhookID: id } } })),
};

// --- Schedules ---
// Filter params line up 1:1 with GET /api/v1/schedules query params.
// `enabled` is a tristate string ('true'|'false'|''); empty omits the param.
// `sort`/`order` whitelist mirrors the daemon: sort one of
// id|name|created_at|next_fire_at|last_fired_at (default id); order asc|desc
// (default asc). `last_fired_at` (5.4.84) puts schedules with a nil
// last_fired_at at the tail of asc / head of desc — mirrors the next_fire_at
// nil-handling and the webhook last_delivery_at sort axis.
export const schedules = {
  list: ({ page, perPage, vmId = '', tagSelector = '', action = '', catchUpPolicy = '', timezone = '', enabled = '', search = '', since = '', until = '', nextFireSince = '', nextFireUntil = '', lastFiredSince = '', lastFiredUntil = '', prefix = '', sort = '', order = '' }: { page?: number; perPage?: number; vmId?: string; tagSelector?: string; action?: 'snapshot' | 'start' | 'stop' | 'restart' | 'force-stop' | 'reboot' | 'suspend' | 'resume' | ''; catchUpPolicy?: 'skip' | 'run_once' | 'run_all' | ''; timezone?: string; enabled?: 'true' | 'false' | ''; search?: string; since?: string; until?: string; nextFireSince?: string; nextFireUntil?: string; lastFiredSince?: string; lastFiredUntil?: string; prefix?: string; sort?: 'id' | 'name' | 'created_at' | 'next_fire_at' | 'last_fired_at' | ''; order?: 'asc' | 'desc' | '' } = {}) =>
    unwrap(apiClient.GET('/schedules', { params: { query: { vm_id: vmId || undefined, tag_selector: tagSelector || undefined, action: action || undefined, catch_up_policy: catchUpPolicy || undefined, timezone: timezone || undefined, enabled: enabled || undefined, search: search || undefined, since: since || undefined, until: until || undefined, next_fire_since: nextFireSince || undefined, next_fire_until: nextFireUntil || undefined, last_fired_since: lastFiredSince || undefined, last_fired_until: lastFiredUntil || undefined, prefix: prefix || undefined, sort: sort || undefined, order: order || undefined, page, per_page: perPage } as any } }), { withMeta: true }),
  get: (id: string) => unwrap(apiClient.GET('/schedules/{scheduleID}', { params: { path: { scheduleID: id } } })),
  create: (spec: paths['/schedules']['post']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.POST('/schedules', { body: spec as any })),
  update: (id: string, patch: paths['/schedules/{scheduleID}']['patch']['requestBody']['content']['application/json']) =>
    unwrap(apiClient.PATCH('/schedules/{scheduleID}', { params: { path: { scheduleID: id } }, body: patch as any })),
  delete: (id: string) =>
    unwrap(apiClient.DELETE('/schedules/{scheduleID}', { params: { path: { scheduleID: id } } })),
  runs: (id: string, { page, perPage, status = '', skipReason = '', vmId = '', since = '', until = '', finishedSince = '', finishedUntil = '', minDurationMs, maxDurationMs, search = '', sort = '', order = '' }: { page?: number; perPage?: number; status?: 'running' | 'success' | 'error' | 'skipped' | ''; skipReason?: 'vm_not_found' | 'vm_already_stopped' | 'vm_already_running' | 'concurrent_run' | 'catch_up_skipped' | 'queue_full' | ''; vmId?: string; since?: string; until?: string; finishedSince?: string; finishedUntil?: string; minDurationMs?: number; maxDurationMs?: number; search?: string; sort?: 'id' | 'started_at' | 'finished_at' | 'status' | 'duration' | ''; order?: 'asc' | 'desc' | '' } = {}) =>
    unwrap(apiClient.GET('/schedules/{scheduleID}/runs', { params: { path: { scheduleID: id }, query: { status: status || undefined, skip_reason: skipReason || undefined, vm_id: vmId || undefined, since: since || undefined, until: until || undefined, finished_since: finishedSince || undefined, finished_until: finishedUntil || undefined, min_duration_ms: minDurationMs == null ? undefined : minDurationMs, max_duration_ms: maxDurationMs == null ? undefined : maxDurationMs, search: search || undefined, sort: sort || undefined, order: order || undefined, page, per_page: perPage } as any } }), { withMeta: true }),
  runNow: (id: string) =>
    unwrap(apiClient.POST('/schedules/{scheduleID}/run-now', { params: { path: { scheduleID: id } } })),
};

// --- Events ---
// Filter params line up 1:1 with GET /api/v1/events query params.
export const events = {
  list: ({ vmId = '', type = '', source = '', severity = '', minSeverity = '', actor = '', resourceId = '', typePrefix = '', search = '', since = '', until = '', sort = '', order = '', page, perPage }: {
    vmId?: string;
    type?: string;
    typePrefix?: string;
    source?: 'libvirt' | 'app' | 'system' | '';
    severity?: 'info' | 'warn' | 'error' | '';
    minSeverity?: 'info' | 'warn' | 'error' | '';
    actor?: string;
    resourceId?: string;
    search?: string;
    since?: string;
    until?: string;
    sort?: 'id' | 'occurred_at' | 'type' | 'source' | 'severity' | '';
    order?: 'asc' | 'desc' | '';
    page?: number;
    perPage?: number;
  } = {}) =>
    unwrap(apiClient.GET('/events', {
      params: { query: { vm_id: vmId, type, source: source || undefined, severity: severity || undefined, min_severity: minSeverity || undefined, actor: actor || undefined, resource_id: resourceId || undefined, type_prefix: typePrefix || undefined, search: search || undefined, since, until, sort: sort || undefined, order: order || undefined, page, per_page: perPage } as any },
    }), { withMeta: true }),
};

export default { vms, snapshots, images, templates, ports, host, quotas, logs, events, system, webhooks, schedules };
