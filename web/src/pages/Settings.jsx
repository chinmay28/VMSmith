import { useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Webhook, Trash2, Pencil, Plus, Send, CheckCircle2, AlertCircle, Clock, Search, X } from 'lucide-react';
import { webhooks as webhooksApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, EmptyState, Spinner, ErrorBanner, Modal, PaginationControls } from '../components/Shared';

const DEFAULT_WEBHOOK_PER_PAGE = 25;

export default function Settings() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState(null);
  const [testResults, setTestResults] = useState({});
  const [testingID, setTestingID] = useState(null);
  const [selected, setSelected] = useState(() => new Set());
  const [bulkResult, setBulkResult] = useState(null);

  // Free-text search across URL, description, and event_types.
  // `searchInput` is the live input value; `searchFilter` is the debounced
  // value that drives the useFetch dependency below. Mirrors the 5.4.x
  // pattern used for VMs, images, events, snapshots, port forwards,
  // templates, and logs.
  const [searchInput, setSearchInput] = useState(searchParams.get('search') || '');
  const [searchFilter, setSearchFilter] = useState(searchParams.get('search') || '');

  // Explicit event-type filter (5.4.26) — case-insensitive exact-match against
  // entries in each webhook's `event_types` list. Catch-all webhooks (empty
  // event_types) are NOT matched, mirroring the bulk_delete selector. Both
  // the live input and the debounced value are tracked separately so typing
  // doesn't thrash the request loop.
  const [eventTypeInput, setEventTypeInput] = useState(searchParams.get('event_type') || '');
  const [eventTypeFilter, setEventTypeFilter] = useState(searchParams.get('event_type') || '');

  // Sort field + order — whitelisted to the values the daemon accepts.
  // URL round-trip mirrors the 5.4.x sort dropdown pattern (VMs, images,
  // snapshots, templates, port forwards). Empty == "use the daemon default".
  const VALID_SORT_FIELDS = ['', 'id', 'url', 'created_at', 'last_delivery_at'];
  const VALID_SORT_ORDERS = ['', 'asc', 'desc'];
  const initialSort = (() => {
    const raw = (searchParams.get('sort') || '').toLowerCase();
    return VALID_SORT_FIELDS.includes(raw) ? raw : '';
  })();
  const initialOrder = (() => {
    const raw = (searchParams.get('order') || '').toLowerCase();
    return VALID_SORT_ORDERS.includes(raw) ? raw : '';
  })();
  const [sortField, setSortField] = useState(initialSort);
  const [sortOrder, setSortOrder] = useState(initialOrder);

  const initialPage = (() => {
    const parsed = parseInt(searchParams.get('page') || '', 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : 1;
  })();
  const initialPerPage = (() => {
    const parsed = parseInt(searchParams.get('per_page') || '', 10);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : DEFAULT_WEBHOOK_PER_PAGE;
  })();
  const [page, setPage] = useState(initialPage);
  const [perPage, setPerPage] = useState(initialPerPage);

  useEffect(() => {
    const trimmed = searchInput.trim();
    const id = setTimeout(() => setSearchFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [searchInput]);

  useEffect(() => {
    const trimmed = eventTypeInput.trim();
    const id = setTimeout(() => setEventTypeFilter(trimmed), 250);
    return () => clearTimeout(id);
  }, [eventTypeInput]);

  // Whenever the filter / sort changes, reset to page 1 so the user doesn't
  // land on an empty page beyond the post-filter population.
  useEffect(() => {
    setPage(1);
  }, [searchFilter, eventTypeFilter, sortField, sortOrder]);

  useEffect(() => {
    const next = new URLSearchParams(searchParams);
    if (searchFilter) next.set('search', searchFilter); else next.delete('search');
    if (eventTypeFilter) next.set('event_type', eventTypeFilter); else next.delete('event_type');
    if (sortField) next.set('sort', sortField); else next.delete('sort');
    if (sortOrder) next.set('order', sortOrder); else next.delete('order');
    if (page > 1) next.set('page', String(page)); else next.delete('page');
    if (perPage !== DEFAULT_WEBHOOK_PER_PAGE) next.set('per_page', String(perPage)); else next.delete('per_page');
    setSearchParams(next, { replace: true });
  }, [searchFilter, eventTypeFilter, sortField, sortOrder, page, perPage]); // eslint-disable-line react-hooks/exhaustive-deps

  const { data: hookResponse, loading, error, refresh } = useFetch(
    () => webhooksApi.list({ search: searchFilter, eventType: eventTypeFilter, sort: sortField, order: sortOrder, page, perPage }),
    [searchFilter, eventTypeFilter, sortField, sortOrder, page, perPage],
    15000,
  );
  const deleteMut = useMutation(webhooksApi.delete);
  const bulkMut = useMutation(webhooksApi.bulkDelete);
  const hooks = hookResponse?.data || [];
  const totalHooks = hookResponse?.meta?.totalCount ?? hooks.length;

  // Drop selections that disappear from the list (after refresh / external delete).
  useEffect(() => {
    if (!hooks.length) {
      if (selected.size) setSelected(new Set());
      return;
    }
    const existing = new Set(hooks.map((wh) => wh.id));
    let changed = false;
    const next = new Set();
    selected.forEach((id) => {
      if (existing.has(id)) next.add(id);
      else changed = true;
    });
    if (changed) setSelected(next);
  }, [hooks]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleDelete = async (id, url) => {
    if (!window.confirm(`Delete webhook for ${url}?`)) return;
    await deleteMut.execute(id);
    refresh();
  };

  const allSelected = selected.size > 0 && selected.size === hooks.length;
  const someSelected = selected.size > 0 && !allSelected;
  const toggleAll = () => {
    if (allSelected) setSelected(new Set());
    else setSelected(new Set(hooks.map((wh) => wh.id)));
  };
  const toggleOne = (id) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const handleBulkDelete = async () => {
    if (!selected.size) return;
    if (!window.confirm(`Delete ${selected.size} webhook${selected.size === 1 ? '' : 's'}?`)) return;
    const result = await bulkMut.execute({ ids: Array.from(selected) });
    setBulkResult(result);
    setSelected(new Set());
    refresh();
  };
  const dismissBulkResult = () => setBulkResult(null);

  const handleTest = async (id) => {
    setTestingID(id);
    try {
      const result = await webhooksApi.test(id);
      setTestResults((prev) => ({ ...prev, [id]: result }));
      refresh();
    } catch (err) {
      setTestResults((prev) => ({
        ...prev,
        [id]: { success: false, error: err?.message || 'request failed' },
      }));
    } finally {
      setTestingID(null);
    }
  };

  return (
    <div data-testid="settings-page">
      <PageHeader
        title="Settings"
        subtitle="Webhooks, integrations, and daemon-wide preferences"
        actions={
          <button className="btn-primary" onClick={() => setShowAdd(true)} data-testid="add-webhook-btn">
            <Plus size={15} /> Add webhook
          </button>
        }
      />

      <h2 className="font-display font-semibold text-steel-200 text-sm uppercase tracking-wider mb-3">
        Webhooks
      </h2>

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}

      <AddWebhookModal open={showAdd} onClose={() => setShowAdd(false)} onCreated={refresh} />
      <EditWebhookModal
        webhook={editing}
        open={editing !== null}
        onClose={() => setEditing(null)}
        onUpdated={refresh}
      />

      <div className="mb-4 flex items-center gap-2 flex-wrap">
        <div className="relative flex-1 max-w-md min-w-[200px]">
          <Search size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-steel-500 pointer-events-none" />
          <input
            type="search"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search by URL, description, or event type…"
            className="input w-full pl-8 pr-8 py-1.5 text-sm"
            data-testid="webhook-list-search"
            aria-label="Search webhooks"
          />
          {searchInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setSearchInput('')}
              data-testid="webhook-list-search-clear"
              aria-label="Clear search"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <div className="relative min-w-[180px]">
          <input
            type="search"
            value={eventTypeInput}
            onChange={(e) => setEventTypeInput(e.target.value)}
            placeholder="Filter by event type…"
            className="input w-full pl-2.5 pr-8 py-1.5 text-sm"
            data-testid="webhook-list-event-type-filter"
            aria-label="Filter by event type"
          />
          {eventTypeInput && (
            <button
              type="button"
              className="absolute right-2 top-1/2 -translate-y-1/2 text-steel-500 hover:text-steel-200"
              onClick={() => setEventTypeInput('')}
              data-testid="webhook-list-event-type-filter-clear"
              aria-label="Clear event-type filter"
            >
              <X size={13} />
            </button>
          )}
        </div>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Sort
          <select
            value={sortField}
            onChange={(e) => setSortField(e.target.value)}
            data-testid="webhook-list-sort-field"
            aria-label="Sort webhooks by"
            className="input py-1 text-xs"
          >
            <option value="">Default (id)</option>
            <option value="id">id</option>
            <option value="url">url</option>
            <option value="created_at">created_at</option>
            <option value="last_delivery_at">last_delivery_at</option>
          </select>
        </label>
        <label className="text-xs text-steel-400 flex items-center gap-1.5">
          Order
          <select
            value={sortOrder}
            onChange={(e) => setSortOrder(e.target.value)}
            data-testid="webhook-list-sort-order"
            aria-label="Sort order"
            className="input py-1 text-xs"
          >
            <option value="">Default (asc)</option>
            <option value="asc">asc</option>
            <option value="desc">desc</option>
          </select>
        </label>
      </div>

      {loading && !hookResponse ? (
        <div className="flex justify-center py-20"><Spinner size={20} /></div>
      ) : hooks.length === 0 ? (
        <div className="card">
          {eventTypeFilter ? (
            <EmptyState
              icon={Search}
              title="No webhooks subscribed"
              description={`No webhooks explicitly subscribe to "${eventTypeFilter}". Catch-all webhooks (no event-type filter) are not matched.`}
            />
          ) : searchFilter ? (
            <EmptyState
              icon={Search}
              title="No webhooks match your search"
              description={`No webhooks match "${searchFilter}". Try a different URL, description, or event-type fragment.`}
            />
          ) : (
            <EmptyState
              icon={Webhook}
              title="No webhooks registered"
              description="Webhooks deliver event-bus traffic to external HTTP receivers signed with HMAC-SHA256."
            />
          )}
        </div>
      ) : (
        <div className="card overflow-hidden" data-testid="webhook-list">
          <div className="flex items-center justify-between px-4 py-1.5 border-b border-steel-800/40 bg-steel-900/40">
            <label className="flex items-center gap-2 text-xs text-steel-400 cursor-pointer">
              <input
                type="checkbox"
                checked={allSelected}
                ref={(el) => { if (el) el.indeterminate = someSelected; }}
                onChange={toggleAll}
                data-testid="webhook-select-all"
              />
              {selected.size > 0 ? `${selected.size} selected` : 'Select all'}
            </label>
            <button
              className="btn-ghost text-xs text-red-400 hover:text-red-300 disabled:opacity-40 disabled:cursor-not-allowed"
              onClick={handleBulkDelete}
              disabled={!selected.size || bulkMut.loading}
              data-testid="btn-bulk-delete-webhooks"
            >
              <Trash2 size={12} /> Delete selected
            </button>
          </div>
          {bulkResult && (
            <div
              className="px-4 py-2 border-b border-steel-800/40 bg-steel-900/40 text-xs text-steel-400 flex items-center justify-between"
              data-testid="webhook-bulk-result"
            >
              <span>
                {(bulkResult.results || []).filter(r => r.success).length} of {(bulkResult.results || []).length} succeeded
                {bulkResult.results?.some(r => !r.success) && (
                  <span className="text-red-400">
                    {' '}· {bulkResult.results.filter(r => !r.success).length} failed
                  </span>
                )}
              </span>
              <button className="btn-ghost text-xs" onClick={dismissBulkResult}>Dismiss</button>
            </div>
          )}
          <table className="w-full">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell w-8"></th>
                <th className="table-header table-cell">URL</th>
                <th className="table-header table-cell">Event filters</th>
                <th className="table-header table-cell">Last delivery</th>
                <th className="table-header table-cell">Last status</th>
                <th className="table-header table-cell text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {hooks.map((wh) => {
                const result = testResults[wh.id];
                return (
                  <tr key={wh.id} className="hover:bg-steel-800/20" data-testid={`webhook-row-${wh.id}`}>
                    <td className="table-cell">
                      <input
                        type="checkbox"
                        checked={selected.has(wh.id)}
                        onChange={() => toggleOne(wh.id)}
                        data-testid={`webhook-checkbox-${wh.id}`}
                      />
                    </td>
                    <td className="table-cell">
                      <div className="flex items-center gap-2.5">
                        <div className="w-7 h-7 rounded bg-steel-800/60 border border-steel-700/30 flex items-center justify-center">
                          <Webhook size={13} className="text-steel-500" />
                        </div>
                        <div>
                          <div className="text-sm text-steel-200 font-mono break-all">{wh.url}</div>
                          {wh.description && (
                            <div
                              className="text-[11px] text-steel-400 break-all"
                              data-testid={`webhook-description-${wh.id}`}
                            >
                              {wh.description}
                            </div>
                          )}
                          {wh.tags?.length > 0 && (
                            <div
                              className="flex flex-wrap gap-1 mt-1"
                              data-testid={`webhook-tags-${wh.id}`}
                            >
                              {wh.tags.map((t) => (
                                <span
                                  key={t}
                                  className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-steel-800/60 text-steel-300 border border-steel-700/30"
                                >
                                  {t}
                                </span>
                              ))}
                            </div>
                          )}
                          <div className="text-[11px] text-steel-600 font-mono">{wh.id}</div>
                        </div>
                      </div>
                    </td>
                    <td className="table-cell">
                      {wh.event_types?.length ? (
                        <div className="flex flex-wrap gap-1">
                          {wh.event_types.map((t) => (
                            <span key={t} className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-steel-800/60 text-steel-300 border border-steel-700/30">
                              {t}
                            </span>
                          ))}
                        </div>
                      ) : (
                        <span className="text-[11px] font-mono text-steel-500">all events</span>
                      )}
                    </td>
                    <td className="table-cell">
                      {wh.last_delivery_at ? (
                        <span className="text-xs font-mono text-steel-400 flex items-center gap-1.5">
                          <Clock size={12} />
                          {new Date(wh.last_delivery_at).toLocaleString()}
                        </span>
                      ) : (
                        <span className="text-xs font-mono text-steel-600">never</span>
                      )}
                    </td>
                    <td className="table-cell">
                      <DeliveryStatus webhook={wh} testResult={result} />
                    </td>
                    <td className="table-cell text-right">
                      <div className="inline-flex items-center gap-1.5">
                        <button
                          className="btn-ghost btn-sm"
                          onClick={() => handleTest(wh.id)}
                          disabled={testingID === wh.id}
                          data-testid={`webhook-test-${wh.id}`}
                          title="Send test event"
                        >
                          {testingID === wh.id ? <Spinner size={13} /> : <Send size={13} />}
                          {testingID === wh.id ? 'Sending…' : 'Test'}
                        </button>
                        <button
                          className="btn-ghost btn-sm"
                          onClick={() => setEditing(wh)}
                          data-testid={`webhook-edit-${wh.id}`}
                          title="Edit webhook"
                        >
                          <Pencil size={13} />
                        </button>
                        <button
                          className="btn-ghost btn-sm text-red-400 hover:text-red-300"
                          onClick={() => handleDelete(wh.id, wh.url)}
                          data-testid={`webhook-delete-${wh.id}`}
                          title="Delete webhook"
                        >
                          <Trash2 size={13} />
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {hooks.length > 0 && (
        <PaginationControls
          page={page}
          perPage={perPage}
          total={totalHooks}
          itemLabel="webhooks"
          onPageChange={setPage}
          onPerPageChange={(value) => { setPerPage(value); setPage(1); }}
        />
      )}
    </div>
  );
}

function DeliveryStatus({ webhook, testResult }) {
  // Test result is the most recent local probe; fall back to the persisted
  // last_status / last_error from the daemon.
  if (testResult) {
    if (testResult.success) {
      return (
        <span className="inline-flex items-center gap-1.5 text-xs font-mono text-emerald-300" data-testid="webhook-status">
          <CheckCircle2 size={13} />
          {testResult.status_code} test ok
        </span>
      );
    }
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-mono text-red-300" data-testid="webhook-status">
        <AlertCircle size={13} />
        {testResult.error || 'failed'}
      </span>
    );
  }
  if (webhook.last_status) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-mono text-emerald-300" data-testid="webhook-status">
        <CheckCircle2 size={13} />
        HTTP {webhook.last_status}
      </span>
    );
  }
  if (webhook.last_error) {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-mono text-red-300" data-testid="webhook-status">
        <AlertCircle size={13} />
        {webhook.last_error}
      </span>
    );
  }
  return <span className="text-xs font-mono text-steel-500">—</span>;
}

function AddWebhookModal({ open, onClose, onCreated }) {
  const [url, setUrl] = useState('');
  const [secret, setSecret] = useState('');
  const [eventTypes, setEventTypes] = useState('');
  const [description, setDescription] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState(null);

  const reset = () => {
    setUrl('');
    setSecret('');
    setEventTypes('');
    setDescription('');
    setTagsInput('');
    setErr(null);
    setSubmitting(false);
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setErr(null);
    setSubmitting(true);
    try {
      const types = eventTypes
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      const tags = tagsInput
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      const trimmedDescription = description.trim();
      await webhooksApi.create({
        url: url.trim(),
        secret: secret.trim(),
        event_types: types.length ? types : undefined,
        description: trimmedDescription || undefined,
        tags: tags.length ? tags : undefined,
      });
      onCreated?.();
      reset();
      onClose();
    } catch (e2) {
      setErr(e2?.message || 'failed to create webhook');
      setSubmitting(false);
    }
  };

  return (
    <Modal open={open} onClose={() => { reset(); onClose(); }} title="Add webhook">
      <form onSubmit={handleSubmit} className="space-y-3" data-testid="add-webhook-form">
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">Receiver URL</label>
          <input
            className="input w-full"
            type="url"
            placeholder="https://example.com/hook"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            required
            data-testid="webhook-url-input"
          />
        </div>
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">HMAC secret</label>
          <input
            className="input w-full"
            type="password"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            required
            data-testid="webhook-secret-input"
          />
          <p className="text-[11px] font-mono text-steel-600 mt-1">
            Used to sign every delivery (X-VMSmith-Signature).
          </p>
        </div>
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">Event-type filters (optional)</label>
          <input
            className="input w-full"
            placeholder="vm.started, system.*"
            value={eventTypes}
            onChange={(e) => setEventTypes(e.target.value)}
            data-testid="webhook-event-types-input"
          />
          <p className="text-[11px] font-mono text-steel-600 mt-1">
            Comma-separated. Empty = subscribe to every event.
          </p>
        </div>
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">Description (optional)</label>
          <input
            className="input w-full"
            placeholder='e.g. "Slack notifier for VM crashes"'
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            maxLength={1024}
            data-testid="webhook-description-input"
          />
          <p className="text-[11px] font-mono text-steel-600 mt-1">
            Free-form label that appears in the list and is searchable.
          </p>
        </div>
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">Tags (optional)</label>
          <input
            className="input w-full"
            placeholder="production, audit, slack"
            value={tagsInput}
            onChange={(e) => setTagsInput(e.target.value)}
            data-testid="webhook-tags-input"
          />
          <p className="text-[11px] font-mono text-steel-600 mt-1">
            Comma-separated. Tags are normalised lowercase and searchable.
          </p>
        </div>

        {err && <ErrorBanner message={err} />}

        <div className="flex justify-end gap-2 pt-2">
          <button type="button" className="btn-ghost" onClick={() => { reset(); onClose(); }}>
            Cancel
          </button>
          <button type="submit" className="btn-primary" disabled={submitting} data-testid="webhook-create-submit">
            {submitting ? <Spinner size={13} /> : null}
            {submitting ? 'Creating…' : 'Create webhook'}
          </button>
        </div>
      </form>
    </Modal>
  );
}

// EditWebhookModal lets operators update an existing webhook's URL, secret,
// event-type filters, and active flag.  Each form field tracks both its
// initial value and a "changed" flag — only fields the user actually touches
// are sent on the PATCH so omitted keys keep their server-side value.
//
// Semantics that mirror the PATCH endpoint:
//   - URL: required, must use http:// or https://
//   - Secret: optional rotation.  Empty input means "leave alone"; the user
//     must type a new secret to rotate it.
//   - Event types: comma-separated.  Toggling "Subscribe to every event"
//     sends event_types=[] which clears the filter list server-side.
//   - Active: boolean toggle.
function EditWebhookModal({ webhook, open, onClose, onUpdated }) {
  const [url, setUrl] = useState('');
  const [secret, setSecret] = useState('');
  const [eventTypes, setEventTypes] = useState('');
  const [subscribeAll, setSubscribeAll] = useState(false);
  const [active, setActive] = useState(true);
  const [description, setDescription] = useState('');
  const [tagsInput, setTagsInput] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState(null);

  // Reset form state whenever the modal opens for a new webhook.
  useEffect(() => {
    if (!webhook) return;
    setUrl(webhook.url || '');
    setSecret('');
    const initialTypes = webhook.event_types?.length ? webhook.event_types.join(', ') : '';
    setEventTypes(initialTypes);
    setSubscribeAll(!webhook.event_types?.length);
    setActive(Boolean(webhook.active));
    setDescription(webhook.description || '');
    setTagsInput(webhook.tags?.length ? webhook.tags.join(', ') : '');
    setErr(null);
    setSubmitting(false);
  }, [webhook]);

  if (!webhook) return null;

  const handleSubmit = async (e) => {
    e.preventDefault();
    setErr(null);
    const spec = {};
    const trimmedURL = url.trim();
    if (trimmedURL !== webhook.url) {
      spec.url = trimmedURL;
    }
    const trimmedSecret = secret.trim();
    if (trimmedSecret !== '') {
      spec.secret = trimmedSecret;
    }
    if (subscribeAll) {
      // "every event" only needs to be sent when the webhook currently has a
      // filter — otherwise omitting the field is a true no-op.
      if (webhook.event_types?.length) {
        spec.event_types = [];
      }
    } else {
      const next = eventTypes
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
      const current = (webhook.event_types || []).slice();
      const same = next.length === current.length && next.every((t, i) => t === current[i]);
      if (!same) {
        spec.event_types = next;
      }
    }
    if (active !== Boolean(webhook.active)) {
      spec.active = active;
    }
    // Description: PATCH semantics are nil = no change, "" = clear.  Only
    // send when the trimmed value differs from the current stored value so
    // unchanged forms don't bounce the worker.
    const trimmedDescription = description.trim();
    if (trimmedDescription !== (webhook.description || '')) {
      spec.description = trimmedDescription;
    }
    // Tags: PATCH semantics are nil = no change, [] = clear. Normalise the
    // input client-side (split + trim + drop empties) and only send when the
    // resulting set differs from the current set.  Compare order-independently
    // (lowercase + sort both sides before walking) so re-submitting a typed
    // "production, audit" over a stored ["audit", "production"] is recognised
    // as a no-op locally and skips the round-trip the server would also flag.
    const nextTags = tagsInput
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean);
    const normalisedNext = nextTags.map((t) => t.toLowerCase()).sort();
    const normalisedCurrent = (webhook.tags || []).map((t) => t.toLowerCase()).sort();
    const sameTags = normalisedNext.length === normalisedCurrent.length
      && normalisedNext.every((t, i) => t === normalisedCurrent[i]);
    if (!sameTags) {
      spec.tags = nextTags;
    }

    if (Object.keys(spec).length === 0) {
      setErr('No fields changed.');
      return;
    }

    setSubmitting(true);
    try {
      await webhooksApi.update(webhook.id, spec);
      onUpdated?.();
      onClose();
    } catch (e2) {
      setErr(e2?.message || 'failed to update webhook');
      setSubmitting(false);
    }
  };

  return (
    <Modal open={open} onClose={onClose} title={`Edit webhook (${webhook.id})`}>
      <form onSubmit={handleSubmit} className="space-y-3" data-testid="edit-webhook-form">
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">Receiver URL</label>
          <input
            className="input w-full"
            type="url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            required
            data-testid="edit-webhook-url-input"
          />
        </div>
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">Rotate HMAC secret</label>
          <input
            className="input w-full"
            type="password"
            placeholder="Leave blank to keep the current secret"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            data-testid="edit-webhook-secret-input"
          />
          <p className="text-[11px] font-mono text-steel-600 mt-1">
            Secrets cannot be cleared — only rotated.
          </p>
        </div>
        <div>
          <label className="flex items-center gap-2 text-xs font-mono text-steel-300 mb-1">
            <input
              type="checkbox"
              checked={subscribeAll}
              onChange={(e) => setSubscribeAll(e.target.checked)}
              data-testid="edit-webhook-subscribe-all"
            />
            Subscribe to every event
          </label>
          {!subscribeAll && (
            <input
              className="input w-full"
              placeholder="vm.started, system.*"
              value={eventTypes}
              onChange={(e) => setEventTypes(e.target.value)}
              data-testid="edit-webhook-event-types-input"
            />
          )}
        </div>
        <div>
          <label className="flex items-center gap-2 text-xs font-mono text-steel-300">
            <input
              type="checkbox"
              checked={active}
              onChange={(e) => setActive(e.target.checked)}
              data-testid="edit-webhook-active-toggle"
            />
            Deliveries enabled
          </label>
          <p className="text-[11px] font-mono text-steel-600 mt-1">
            Disable to pause the worker without deleting the registration.
          </p>
        </div>
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">Description</label>
          <input
            className="input w-full"
            placeholder='e.g. "Slack notifier for VM crashes"'
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            maxLength={1024}
            data-testid="edit-webhook-description-input"
          />
          <p className="text-[11px] font-mono text-steel-600 mt-1">
            Clear the field to remove the description; ≤1024 characters.
          </p>
        </div>
        <div>
          <label className="block text-xs font-mono text-steel-400 mb-1">Tags</label>
          <input
            className="input w-full"
            placeholder="production, audit, slack"
            value={tagsInput}
            onChange={(e) => setTagsInput(e.target.value)}
            data-testid="edit-webhook-tags-input"
          />
          <p className="text-[11px] font-mono text-steel-600 mt-1">
            Comma-separated. Clear the field to remove all tags.
          </p>
        </div>

        {err && <ErrorBanner message={err} />}

        <div className="flex justify-end gap-2 pt-2">
          <button type="button" className="btn-ghost" onClick={onClose}>
            Cancel
          </button>
          <button type="submit" className="btn-primary" disabled={submitting} data-testid="edit-webhook-submit">
            {submitting ? <Spinner size={13} /> : null}
            {submitting ? 'Saving…' : 'Save changes'}
          </button>
        </div>
      </form>
    </Modal>
  );
}
