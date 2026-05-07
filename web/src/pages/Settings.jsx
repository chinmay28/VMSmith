import { useState } from 'react';
import { Webhook, Trash2, Plus, Send, CheckCircle2, AlertCircle, Clock } from 'lucide-react';
import { webhooks as webhooksApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, EmptyState, Spinner, ErrorBanner, Modal } from '../components/Shared';

export default function Settings() {
  const [showAdd, setShowAdd] = useState(false);
  const [testResults, setTestResults] = useState({});
  const [testingID, setTestingID] = useState(null);

  const { data: hookList, loading, error, refresh } = useFetch(
    () => webhooksApi.list(),
    [],
    15000,
  );
  const deleteMut = useMutation(webhooksApi.delete);
  const hooks = hookList || [];

  const handleDelete = async (id, url) => {
    if (!window.confirm(`Delete webhook for ${url}?`)) return;
    await deleteMut.execute(id);
    refresh();
  };

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

      {loading && !hookList ? (
        <div className="flex justify-center py-20"><Spinner size={20} /></div>
      ) : hooks.length === 0 ? (
        <div className="card">
          <EmptyState
            icon={Webhook}
            title="No webhooks registered"
            description="Webhooks deliver event-bus traffic to external HTTP receivers signed with HMAC-SHA256."
          />
        </div>
      ) : (
        <div className="card overflow-hidden" data-testid="webhook-list">
          <table className="w-full">
            <thead>
              <tr className="border-b border-steel-800/40">
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
                      <div className="flex items-center gap-2.5">
                        <div className="w-7 h-7 rounded bg-steel-800/60 border border-steel-700/30 flex items-center justify-center">
                          <Webhook size={13} className="text-steel-500" />
                        </div>
                        <div>
                          <div className="text-sm text-steel-200 font-mono break-all">{wh.url}</div>
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
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState(null);

  const reset = () => {
    setUrl('');
    setSecret('');
    setEventTypes('');
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
      await webhooksApi.create({ url: url.trim(), secret: secret.trim(), event_types: types.length ? types : undefined });
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
