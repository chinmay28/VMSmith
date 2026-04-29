import { useState, useEffect } from 'react';
import { Plus, FileCode, Pencil, Trash2, Network, X } from 'lucide-react';
import { templates as templatesApi, images as imagesApi, host as hostApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, EmptyState, Spinner, ErrorBanner, Modal, PaginationControls } from '../components/Shared';
import { listData, safeArray } from '../utils/normalize';

const DEFAULT_PER_PAGE = 25;

export default function Templates() {
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PER_PAGE);
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState(null);

  const { data: templateResponse, loading, error, refresh } = useFetch(
    () => templatesApi.list({ page, perPage }),
    [page, perPage],
    10000,
  );
  const deleteMut = useMutation(templatesApi.delete);

  const templateList = listData(templateResponse);
  const total = templateResponse?.meta?.totalCount ?? templateList.length;

  const handleDelete = async (template) => {
    if (!window.confirm(`Delete template "${template.name}"?`)) return;
    try {
      await deleteMut.execute(template.id);
      refresh();
    } catch { /* error shown via mutation state */ }
  };

  const openCreate = () => {
    setEditing(null);
    setShowForm(true);
  };

  const openEdit = (template) => {
    setEditing(template);
    setShowForm(true);
  };

  const closeForm = () => {
    setShowForm(false);
    setEditing(null);
  };

  return (
    <div>
      <PageHeader
        title="Templates"
        subtitle={`${total} reusable VM preset${total === 1 ? '' : 's'}`}
        actions={
          <button className="btn-primary" onClick={openCreate} data-testid="btn-new-template">
            <Plus size={15} /> New Template
          </button>
        }
      />

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}
      {deleteMut.error && <div className="mb-4"><ErrorBanner message={deleteMut.error} /></div>}

      <TemplateFormModal
        open={showForm}
        template={editing}
        onClose={closeForm}
        onSaved={() => {
          closeForm();
          refresh();
        }}
      />

      {loading && !templateList.length ? (
        <div className="flex justify-center py-20"><Spinner size={20} /></div>
      ) : !templateList.length ? (
        <div className="card">
          <EmptyState
            icon={FileCode}
            title="No templates"
            description="Templates pre-fill the Create VM form with image, CPU, RAM, disk, tags and networks. Create one to reuse common VM presets."
            action={
              <button className="btn-primary" onClick={openCreate}>
                <Plus size={15} /> New Template
              </button>
            }
          />
        </div>
      ) : (
        <div className="card overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell">Name</th>
                <th className="table-header table-cell">Image</th>
                <th className="table-header table-cell">Resources</th>
                <th className="table-header table-cell">Tags</th>
                <th className="table-header table-cell">Updated</th>
                <th className="table-header table-cell text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {templateList.map(tpl => (
                <tr key={tpl.id} className="hover:bg-steel-800/20 transition-colors" data-testid={`template-row-${tpl.id}`}>
                  <td className="table-cell">
                    <div className="flex items-center gap-2.5">
                      <div className="w-7 h-7 rounded bg-steel-800/60 border border-steel-700/30 flex items-center justify-center">
                        <FileCode size={13} className="text-steel-500" />
                      </div>
                      <div>
                        <span className="font-mono text-sm text-steel-100">{tpl.name}</span>
                        {tpl.description && (
                          <p className="text-[10px] font-mono text-steel-600 mt-0.5">{tpl.description}</p>
                        )}
                      </div>
                    </div>
                  </td>
                  <td className="table-cell font-mono text-xs text-steel-400 max-w-[260px] truncate" title={tpl.image}>
                    {tpl.image || <span className="text-steel-600">—</span>}
                  </td>
                  <td className="table-cell font-mono text-xs text-steel-400">
                    {(tpl.cpus || tpl.ram_mb || tpl.disk_gb)
                      ? `${tpl.cpus || '–'} CPU · ${tpl.ram_mb || '–'} MB · ${tpl.disk_gb || '–'} GB`
                      : <span className="text-steel-600">defaults</span>}
                  </td>
                  <td className="table-cell">
                    <div className="flex flex-wrap gap-1">
                      {(tpl.tags || []).length === 0
                        ? <span className="text-steel-600 text-xs">—</span>
                        : tpl.tags.map(tag => (
                            <span key={tag} className="badge bg-steel-800/60 text-steel-400 border-steel-700/40">{tag}</span>
                          ))}
                    </div>
                  </td>
                  <td className="table-cell text-xs text-steel-500">
                    {tpl.updated_at ? new Date(tpl.updated_at).toLocaleString() : '—'}
                  </td>
                  <td className="table-cell text-right">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        className="btn-ghost text-xs text-blue-400 hover:text-blue-300"
                        onClick={() => openEdit(tpl)}
                        data-testid={`btn-edit-template-${tpl.id}`}
                      >
                        <Pencil size={13} /> Edit
                      </button>
                      <button
                        className="btn-ghost text-xs text-red-400 hover:text-red-300"
                        onClick={() => handleDelete(tpl)}
                        data-testid={`btn-delete-template-${tpl.id}`}
                      >
                        <Trash2 size={13} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {!!templateList.length && (
        <PaginationControls
          page={page}
          perPage={perPage}
          total={total}
          itemLabel="templates"
          onPageChange={setPage}
          onPerPageChange={(value) => {
            setPerPage(value);
            setPage(1);
          }}
        />
      )}
    </div>
  );
}

const emptyForm = {
  name: '',
  image: '',
  cpus: 0,
  ram_mb: 0,
  disk_gb: 0,
  description: '',
  tags: '',
  default_user: '',
};

function templateToForm(tpl) {
  if (!tpl) return { ...emptyForm };
  return {
    name: tpl.name || '',
    image: tpl.image || '',
    cpus: tpl.cpus || 0,
    ram_mb: tpl.ram_mb || 0,
    disk_gb: tpl.disk_gb || 0,
    description: tpl.description || '',
    tags: (tpl.tags || []).join(', '),
    default_user: tpl.default_user || '',
  };
}

function templateNetworks(tpl) {
  return (tpl?.networks || []).map(net => ({
    mode: net.mode || 'macvtap',
    host_interface: net.host_interface || net.bridge || '',
    static_ip: net.static_ip || '',
    gateway: net.gateway || '',
    dhcp: !net.static_ip,
  }));
}

function TemplateFormModal({ open, template, onClose, onSaved }) {
  const [form, setForm] = useState(templateToForm(null));
  const [networks, setNetworks] = useState([]);
  const [submitError, setSubmitError] = useState('');

  const createMut = useMutation(templatesApi.create);
  const updateMut = useMutation((id, spec) => templatesApi.update(id, spec));
  const submitting = createMut.loading || updateMut.loading;

  const { data: imageResponse } = useFetch(() => imagesApi.list({ perPage: 200 }), [open], 0);
  const { data: hostIfaces } = useFetch(() => hostApi.interfaces(), [open], 0);
  const imageList = listData(imageResponse);
  const hostInterfaces = safeArray(hostIfaces);
  const physIfaces = hostInterfaces.filter(i => i && i.is_physical && i.is_up);

  useEffect(() => {
    if (!open) return;
    setForm(templateToForm(template));
    setNetworks(templateNetworks(template));
    setSubmitError('');
  }, [open, template]);

  const update = (field) => (e) => setForm(f => ({ ...f, [field]: e.target.value }));
  const updateNum = (field) => (e) => setForm(f => ({ ...f, [field]: parseInt(e.target.value, 10) || 0 }));

  const addNetwork = () => setNetworks(n => [...n, { mode: 'macvtap', host_interface: '', static_ip: '', gateway: '', dhcp: true }]);
  const removeNetwork = (i) => setNetworks(n => n.filter((_, idx) => idx !== i));
  const updateNet = (i, field, val) => setNetworks(n => n.map((net, idx) => idx === i ? { ...net, [field]: val } : net));

  const buildSpec = () => {
    const tags = form.tags.split(',').map(t => t.trim()).filter(Boolean);
    const spec = {
      name: form.name.trim(),
      image: form.image.trim(),
    };
    if (form.cpus) spec.cpus = form.cpus;
    if (form.ram_mb) spec.ram_mb = form.ram_mb;
    if (form.disk_gb) spec.disk_gb = form.disk_gb;
    if (form.description.trim()) spec.description = form.description.trim();
    if (tags.length) spec.tags = tags;
    if (form.default_user.trim()) spec.default_user = form.default_user.trim();
    if (networks.length) {
      spec.networks = networks.map(n => {
        const att = { mode: n.mode };
        if (n.mode === 'bridge') att.bridge = n.host_interface;
        else att.host_interface = n.host_interface;
        if (!n.dhcp && n.static_ip) att.static_ip = n.static_ip;
        if (!n.dhcp && n.gateway) att.gateway = n.gateway;
        return att;
      });
    }
    return spec;
  };

  const handleSubmit = async () => {
    setSubmitError('');
    const spec = buildSpec();
    if (!spec.name) {
      setSubmitError('Name is required');
      return;
    }
    if (!spec.image) {
      setSubmitError('Image is required');
      return;
    }
    try {
      if (template?.id) {
        await updateMut.execute(template.id, spec);
      } else {
        await createMut.execute(spec);
      }
      onSaved();
    } catch (err) {
      setSubmitError(err.message);
    }
  };

  const isEdit = !!template?.id;
  const error = submitError || createMut.error || updateMut.error;

  return (
    <Modal open={open} onClose={onClose} title={isEdit ? 'Edit Template' : 'New Template'} wide>
      <div className="flex flex-col max-h-[75vh]">
        <div className="overflow-y-auto flex-1 space-y-4 pr-1 pb-1">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="label">Name</label>
              <input
                className="input"
                placeholder="rocky-small"
                value={form.name}
                onChange={update('name')}
                autoFocus
                data-testid="input-template-name"
              />
            </div>
            <div>
              <label className="label">Default SSH User <span className="text-steel-500 font-normal">(blank = root)</span></label>
              <input
                className="input font-mono"
                placeholder="root"
                value={form.default_user}
                onChange={update('default_user')}
                data-testid="input-template-default-user"
              />
            </div>
          </div>

          <div>
            <label className="label">Base Image</label>
            {imageList.length === 0 ? (
              <input
                className="input font-mono"
                placeholder="rocky9.qcow2"
                value={form.image}
                onChange={update('image')}
                data-testid="input-template-image"
              />
            ) : (
              <select
                className="input"
                value={form.image}
                onChange={update('image')}
                data-testid="input-template-image"
              >
                <option value="">Select an image…</option>
                {imageList.map(img => (
                  <option key={img.id} value={img.path}>{img.name}</option>
                ))}
                {form.image && !imageList.some(img => img.path === form.image) && (
                  <option value={form.image}>{form.image} (custom)</option>
                )}
              </select>
            )}
          </div>

          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className="label">vCPUs <span className="text-steel-500 font-normal">(0 = default)</span></label>
              <input
                className="input"
                type="number"
                min={0}
                value={form.cpus}
                onChange={updateNum('cpus')}
                data-testid="input-template-cpus"
              />
            </div>
            <div>
              <label className="label">RAM (MB)</label>
              <input
                className="input"
                type="number"
                min={0}
                step={256}
                value={form.ram_mb}
                onChange={updateNum('ram_mb')}
                data-testid="input-template-ram"
              />
            </div>
            <div>
              <label className="label">Disk (GB)</label>
              <input
                className="input"
                type="number"
                min={0}
                value={form.disk_gb}
                onChange={updateNum('disk_gb')}
                data-testid="input-template-disk"
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="label">Description</label>
              <input
                className="input"
                placeholder="Small Rocky preset"
                value={form.description}
                onChange={update('description')}
              />
            </div>
            <div>
              <label className="label">Tags</label>
              <input
                className="input font-mono"
                placeholder="dev,small"
                value={form.tags}
                onChange={update('tags')}
              />
            </div>
          </div>

          <div>
            <div className="flex items-center justify-between mb-3">
              <h3 className="text-xs font-semibold text-steel-400 uppercase tracking-wider">Extra Networks</h3>
              <button className="btn-ghost text-xs" type="button" onClick={addNetwork}>
                <Plus size={12} /> Add Interface
              </button>
            </div>
            {networks.length === 0 ? (
              <p className="text-xs text-steel-500 px-1">
                Optional. The NAT network is always attached to VMs created from this template.
              </p>
            ) : (
              <div className="space-y-2">
                {networks.map((net, i) => (
                  <div key={i} className="flex items-start gap-2 p-3 rounded border border-steel-700/40 bg-steel-900/40">
                    <Network size={13} className="text-steel-500 mt-2 shrink-0" />
                    <div className="flex-1 space-y-2">
                      <div className="grid grid-cols-2 gap-2">
                        <div>
                          <label className="label text-[10px]">Mode</label>
                          <select className="input py-1 text-xs" value={net.mode} onChange={e => updateNet(i, 'mode', e.target.value)}>
                            <option value="macvtap">macvtap (direct)</option>
                            <option value="bridge">bridge</option>
                          </select>
                        </div>
                        <div>
                          <label className="label text-[10px]">
                            {net.mode === 'bridge' ? 'Bridge Name' : 'Host Interface'}
                          </label>
                          {net.mode === 'macvtap' && physIfaces.length > 0 ? (
                            <select className="input py-1 text-xs" value={net.host_interface} onChange={e => updateNet(i, 'host_interface', e.target.value)}>
                              <option value="">Select…</option>
                              {physIfaces.map(iface => (
                                <option key={iface.name} value={iface.name}>
                                  {iface.name}{iface.ips?.length ? ` (${iface.ips[0]})` : ''}
                                </option>
                              ))}
                            </select>
                          ) : (
                            <input
                              className="input py-1 text-xs font-mono"
                              placeholder={net.mode === 'bridge' ? 'br-data' : 'eth1'}
                              value={net.host_interface}
                              onChange={e => updateNet(i, 'host_interface', e.target.value)}
                            />
                          )}
                        </div>
                      </div>
                      <label className="flex items-center gap-2 cursor-pointer select-none">
                        <input
                          type="checkbox"
                          className="rounded border-steel-600 bg-steel-800 text-blue-500 focus:ring-blue-500/30"
                          checked={net.dhcp}
                          onChange={e => updateNet(i, 'dhcp', e.target.checked)}
                        />
                        <span className="text-xs text-steel-400">Use DHCP (no static IP)</span>
                      </label>
                      {!net.dhcp && (
                        <div className="grid grid-cols-2 gap-2">
                          <div>
                            <label className="label text-[10px]">Static IP (CIDR)</label>
                            <input
                              className="input py-1 text-xs font-mono"
                              placeholder="10.0.0.2/24"
                              value={net.static_ip}
                              onChange={e => updateNet(i, 'static_ip', e.target.value)}
                            />
                          </div>
                          <div>
                            <label className="label text-[10px]">Gateway</label>
                            <input
                              className="input py-1 text-xs font-mono"
                              placeholder="10.0.0.1"
                              value={net.gateway}
                              onChange={e => updateNet(i, 'gateway', e.target.value)}
                            />
                          </div>
                        </div>
                      )}
                    </div>
                    <button className="btn-ghost text-red-400 hover:text-red-300 mt-1 shrink-0" onClick={() => removeNetwork(i)}>
                      <X size={13} />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        <div className="shrink-0 pt-3 mt-1 border-t border-steel-800/60">
          {error && <p className="text-sm text-red-400 mb-2">Error: {error}</p>}
          <div className="flex justify-end gap-2">
            <button className="btn-secondary" onClick={onClose} disabled={submitting}>Cancel</button>
            <button
              className="btn-primary"
              onClick={handleSubmit}
              disabled={submitting || !form.name || !form.image}
              data-testid="btn-submit-template"
            >
              {submitting ? <Spinner size={14} /> : (isEdit ? <Pencil size={15} /> : <Plus size={15} />)}
              {isEdit ? 'Save Changes' : 'Create Template'}
            </button>
          </div>
        </div>
      </div>
    </Modal>
  );
}
