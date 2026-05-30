const http = require("http");
const fs = require("fs");
const path = require("path");

const DIST_DIR = path.resolve(__dirname, "../../internal/web/dist");
const DIST_INDEX = path.join(DIST_DIR, "index.html");

const WEBHOOK_TAG_RE = /^[a-z0-9][a-z0-9._:-]*$/;
// normalizeWebhookTags mirrors internal/validate.NormalizeTags: trim,
// lowercase, dedupe, alphabetise; reject empty values and characters outside
// the lowercase tag alphabet. Returns {tags, err} so callers can short-circuit
// on validation failure.
function normalizeWebhookTags(input) {
  if (!Array.isArray(input)) return { tags: null, err: null };
  if (input.length === 0) return { tags: [], err: null };
  const seen = new Set();
  const out = [];
  for (const raw of input) {
    if (typeof raw !== "string") {
      return { tags: null, err: { code: "invalid_webhook", message: "tags must be strings" } };
    }
    const trimmed = raw.trim().toLowerCase();
    if (trimmed === "") {
      return { tags: null, err: { code: "invalid_webhook", message: "tags cannot contain empty values" } };
    }
    if (trimmed.length > 32) {
      return { tags: null, err: { code: "invalid_webhook", message: "tags must be 1-32 characters" } };
    }
    if (!WEBHOOK_TAG_RE.test(trimmed)) {
      return { tags: null, err: { code: "invalid_webhook", message: "tags must contain only lowercase letters, numbers, dots, colons, underscores, or hyphens" } };
    }
    if (seen.has(trimmed)) continue;
    seen.add(trimmed);
    out.push(trimmed);
  }
  out.sort();
  return { tags: out.length ? out : [], err: null };
}

let vmCounter = 0;
let webhookCounter = 0;
let scheduleCounter = 0;
let scheduleRunCounter = 0;
const vms = new Map();
const snapshots = new Map();
const images = new Map();
const templates = new Map();
const portForwards = new Map();
const webhookList = new Map();
const scheduleList = new Map();
// scheduleRuns: scheduleID -> [run, ...] (kept newest-first).
const scheduleRuns = new Map();

function seed() {
  const vm1 = createVM({ name: "web-server", image: "ubuntu-22.04", cpus: 2, ram_mb: 4096, disk_gb: 40 });
  vm1.ip = "192.168.100.10";
  vm1.tags = ["dev"];
  // Named network attachments power the 5.4.36 ?network= filter tests.
  vm1.spec.networks = [{ name: "data-net", mode: "macvtap", host_interface: "eth1" }];
  // Fixed timestamps so the 5.4.30 created-at time-range filter tests are
  // deterministic. vm-1 sits before 2026-05-10, vm-2 after — letting the GUI
  // boundary at 2026-05-10 split them cleanly.
  vm1.created_at = "2026-05-05T00:00:00Z";
  vm1.updated_at = "2026-05-05T00:00:00Z";
  const vm2 = createVM({ name: "db-server", image: "rocky-9", cpus: 4, ram_mb: 8192, disk_gb: 100 });
  vm2.state = "stopped";
  vm2.ip = "192.168.100.11";
  vm2.spec.networks = [{ name: "storage-net", mode: "bridge", bridge: "br-storage" }];
  vm2.created_at = "2026-05-15T00:00:00Z";
  vm2.updated_at = "2026-05-15T00:00:00Z";
  snapshots.set(vm1.id, [
    {
      id: `${vm1.id}/before-deploy`,
      vm_id: vm1.id,
      name: "before-deploy",
      description: "checkpoint before May deploy",
      created_at: "2026-05-05T00:00:00Z",
    },
    { id: `${vm1.id}/auto-2026-05-06`, vm_id: vm1.id, name: "auto-2026-05-06", created_at: "2026-05-06T00:00:00Z" },
    { id: `${vm1.id}/auto-2026-05-07`, vm_id: vm1.id, name: "auto-2026-05-07", created_at: "2026-05-07T00:00:00Z" },
  ]);
  // Seed a few port forwards so bulk-delete UI tests have rows to act on.
  portForwards.set(vm1.id, [
    { id: "pf-seed-ssh", vm_id: vm1.id, host_port: 2222, guest_port: 22, guest_ip: vm1.ip, protocol: "tcp", description: "ssh-jumpbox" },
    { id: "pf-seed-http", vm_id: vm1.id, host_port: 8080, guest_port: 80, guest_ip: vm1.ip, protocol: "tcp" },
  ]);
  images.set("img-1", {
    id: "img-1", name: "ubuntu-base", path: "/images/ubuntu-base.qcow2",
    size_bytes: 1073741824, format: "qcow2", source_vm: vm1.id,
    description: "Stock Ubuntu cloud image", tags: ["ubuntu", "stable"],
    // Fixed timestamps so the 5.4.29 since/until UI tests can filter
    // deterministically. img-1 is older, img-2 is newer.
    created_at: "2026-05-05T12:00:00Z", updated_at: "2026-05-05T12:00:00Z",
  });
  images.set("img-2", {
    id: "img-2", name: "rocky-experimental", path: "/images/rocky-experimental.qcow2",
    size_bytes: 2147483648, format: "qcow2",
    description: "Lab build", tags: ["rocky", "experimental"],
    created_at: "2026-05-12T12:00:00Z", updated_at: "2026-05-12T12:00:00Z",
  });
  templates.set("tmpl-1", {
    id: "tmpl-1",
    name: "small-ubuntu",
    image: "/images/ubuntu-base.qcow2",
    cpus: 1,
    ram_mb: 1024,
    disk_gb: 10,
    description: "Small Ubuntu template",
    tags: ["starter", "ubuntu"],
    default_user: "ubuntu",
    networks: [],
    created_at: "2026-05-05T12:00:00Z",
    updated_at: "2026-05-05T12:00:00Z",
  });
  templates.set("tmpl-2", {
    id: "tmpl-2",
    name: "big-rocky",
    image: "/images/rocky9.qcow2",
    cpus: 8,
    ram_mb: 16384,
    disk_gb: 200,
    description: "Big Rocky template",
    tags: ["prod", "rocky"],
    default_user: "root",
    networks: [{ name: "data-net", mode: "macvtap", host_interface: "eth1" }],
    created_at: "2026-05-15T12:00:00Z",
    updated_at: "2026-05-15T12:00:00Z",
  });

  // Seed a couple of schedules so the GUI has rows to render / filter / edit.
  scheduleList.set("sch-1", {
    id: "sch-1",
    name: "nightly-snapshot",
    vm_id: vm1.id,
    tag_selector: null,
    action: "snapshot",
    cron_spec: "0 0 2 * * *",
    timezone: "UTC",
    enabled: true,
    catch_up_policy: "skip",
    max_concurrent: 1,
    retention_count: 7,
    params: {},
    created_at: "2026-05-05T00:00:00Z",
    updated_at: "2026-05-05T00:00:00Z",
    last_fired_at: "2026-05-23T02:00:00Z",
    last_result: "success",
    next_fire_at: "2026-05-25T02:00:00Z",
  });
  scheduleList.set("sch-2", {
    id: "sch-2",
    name: "weekend-shutdown",
    vm_id: "",
    tag_selector: ["dev"],
    action: "stop",
    cron_spec: "0 0 3 * * 0",
    timezone: "America/New_York",
    enabled: false,
    catch_up_policy: "run_once",
    max_concurrent: 0,
    retention_count: 0,
    params: {},
    created_at: "2026-05-10T00:00:00Z",
    updated_at: "2026-05-10T00:00:00Z",
    last_fired_at: null,
    last_result: "",
    next_fire_at: null,
  });
  scheduleList.set("sch-3", {
    id: "sch-3",
    name: "weekly-health-check",
    vm_id: "",
    tag_selector: null,
    action: "snapshot",
    cron_spec: "0 30 4 * * 1",
    timezone: "UTC",
    enabled: true,
    catch_up_policy: "skip",
    max_concurrent: 1,
    retention_count: 0,
    params: {},
    created_at: "2026-05-12T00:00:00Z",
    updated_at: "2026-05-12T00:00:00Z",
    last_fired_at: null,
    last_result: "",
    next_fire_at: "2026-05-26T04:30:00Z",
  });
  scheduleRuns.set("sch-1", [
    {
      id: "run-4",
      schedule_id: "sch-1",
      vm_id: vm2.id,
      started_at: "2026-05-24T02:00:00Z",
      finished_at: "2026-05-24T02:00:06Z",
      status: "success",
    },
    {
      id: "run-2",
      schedule_id: "sch-1",
      vm_id: vm1.id,
      started_at: "2026-05-23T02:00:00Z",
      finished_at: "2026-05-23T02:00:05Z",
      status: "success",
    },
    {
      id: "run-1",
      schedule_id: "sch-1",
      vm_id: vm1.id,
      started_at: "2026-05-22T02:00:00Z",
      finished_at: "2026-05-22T02:00:04Z",
      status: "success",
    },
    {
      id: "run-3",
      schedule_id: "sch-1",
      vm_id: vm1.id,
      started_at: "2026-05-21T02:00:00Z",
      finished_at: "2026-05-21T02:00:03Z",
      status: "error",
      error: "libvirt: snapshot failed",
    },
  ]);
  scheduleRuns.set("sch-2", []);
}

function resetState() {
  vmCounter = 0;
  webhookCounter = 0;
  scheduleCounter = 0;
  scheduleRunCounter = 0;
  vms.clear();
  snapshots.clear();
  images.clear();
  templates.clear();
  portForwards.clear();
  webhookList.clear();
  scheduleList.clear();
  scheduleRuns.clear();
  seed();
}

function createVM(spec) {
  vmCounter++;
  const id = `vm-${vmCounter}`;
  const vm = {
    id, name: spec.name,
    spec: { name: spec.name, image: spec.image || "ubuntu", cpus: spec.cpus || 2, ram_mb: spec.ram_mb || 2048, disk_gb: spec.disk_gb || 20, ssh_pub_key: spec.ssh_pub_key || "", default_user: spec.default_user || "", networks: spec.networks || [], auto_start: !!spec.auto_start, locked: !!spec.locked },
    state: "running", ip: "", disk_path: `/var/lib/vmsmith/vms/${id}/disk.qcow2`,
    created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
  };
  vms.set(id, vm);
  snapshots.set(id, snapshots.get(id) || []);
  portForwards.set(id, []);
  return vm;
}

function parseBody(req) {
  return new Promise((resolve) => {
    let data = "";
    req.on("data", (chunk) => (data += chunk));
    req.on("end", () => { try { resolve(JSON.parse(data)); } catch { resolve({}); } });
  });
}

function json(res, status, data, headers = {}) {
  res.writeHead(status, { "Content-Type": "application/json", ...headers });
  res.end(JSON.stringify(data));
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  const p = url.pathname;
  const method = req.method;

  // Build info — public, mirrors the daemon's GET /api/version.
  if (p === "/api/version" && method === "GET") {
    return json(res, 200, {
      version: "v0.0.0-mock",
      commit: "mockcommit",
      build_date: "2026-05-06T00:00:00Z",
      go_version: "go1.22.0",
      os: "linux",
      arch: "amd64",
    }, { "Cache-Control": "no-store" });
  }

  // API routes
  if (p === "/api/v1/vms" && method === "GET") {
    const sortField = url.searchParams.get("sort") || "id";
    const order = url.searchParams.get("order") || "asc";
    const search = (url.searchParams.get("search") || "").trim().toLowerCase();
    const imageFilter = (url.searchParams.get("image") || "").trim().toLowerCase();
    const defaultUserFilter = (url.searchParams.get("default_user") || "").trim().toLowerCase();
    const networkFilter = (url.searchParams.get("network") || "").trim().toLowerCase();
    const parseTristate = (name) => {
      const raw = (url.searchParams.get(name) || "").trim().toLowerCase();
      if (raw === "") return { set: false, value: false };
      if (raw === "true" || raw === "1") return { set: true, value: true };
      if (raw === "false" || raw === "0") return { set: true, value: false };
      return { set: true, value: false, invalid: true };
    };
    const autoStart = parseTristate("auto_start");
    if (autoStart.invalid) {
      return json(res, 400, { code: "invalid_auto_start", message: "auto_start must be 'true' or 'false'" });
    }
    const locked = parseTristate("locked");
    if (locked.invalid) {
      return json(res, 400, { code: "invalid_locked", message: "locked must be 'true' or 'false'" });
    }
    // since / until: inclusive RFC3339 time-range filter on created_at;
    // whitespace-trimmed, empty disables, invalid -> 400, VMs with zero
    // created_at filtered OUT whenever any bound is set (mirrors the API).
    const parseTime = (name) => {
      const raw = (url.searchParams.get(name) || "").trim();
      if (raw === "") return { set: false, value: null };
      const ts = new Date(raw);
      if (Number.isNaN(ts.getTime())) return { set: false, value: null, invalid: true };
      return { set: true, value: ts };
    };
    const since = parseTime("since");
    if (since.invalid) {
      return json(res, 400, { code: "invalid_since", message: "since must be a valid RFC3339 timestamp" });
    }
    const until = parseTime("until");
    if (until.invalid) {
      return json(res, 400, { code: "invalid_until", message: "until must be a valid RFC3339 timestamp" });
    }
    // min_cpus / max_cpus: inclusive non-negative integer range filter on
    // spec.cpus; non-numeric/negative value -> 400; whitespace-only disables.
    const parseCount = (raw, name) => {
      const v = (raw || "").trim();
      if (v === "") return { set: false };
      if (!/^\d+$/.test(v)) {
        return { invalid: true, code: `invalid_${name}`, msg: `${name} must be a non-negative integer` };
      }
      return { set: true, value: Number(v) };
    };
    const minCpusP = parseCount(url.searchParams.get("min_cpus"), "min_cpus");
    if (minCpusP.invalid) return json(res, 400, { code: minCpusP.code, message: minCpusP.msg });
    const maxCpusP = parseCount(url.searchParams.get("max_cpus"), "max_cpus");
    if (maxCpusP.invalid) return json(res, 400, { code: maxCpusP.code, message: maxCpusP.msg });
    const minRamP = parseCount(url.searchParams.get("min_ram_mb"), "min_ram_mb");
    if (minRamP.invalid) return json(res, 400, { code: minRamP.code, message: minRamP.msg });
    const maxRamP = parseCount(url.searchParams.get("max_ram_mb"), "max_ram_mb");
    if (maxRamP.invalid) return json(res, 400, { code: maxRamP.code, message: maxRamP.msg });
    const minDiskP = parseCount(url.searchParams.get("min_disk_gb"), "min_disk_gb");
    if (minDiskP.invalid) return json(res, 400, { code: minDiskP.code, message: minDiskP.msg });
    const maxDiskP = parseCount(url.searchParams.get("max_disk_gb"), "max_disk_gb");
    if (maxDiskP.invalid) return json(res, 400, { code: maxDiskP.code, message: maxDiskP.msg });
    if (!["id", "name", "created_at", "state"].includes(sortField)) {
      return json(res, 400, { code: "invalid_sort", message: "sort must be one of: id, name, created_at, state" });
    }
    if (!["asc", "desc"].includes(order)) {
      return json(res, 400, { code: "invalid_order", message: "order must be 'asc' or 'desc'" });
    }
    let list = [...vms.values()];
    if (imageFilter) {
      list = list.filter(vm => String(vm.spec?.image || "").toLowerCase() === imageFilter);
    }
    if (search) {
      list = list.filter(vm => {
        if (vm.name && String(vm.name).toLowerCase().includes(search)) return true;
        if (vm.description && String(vm.description).toLowerCase().includes(search)) return true;
        return Array.isArray(vm.tags) && vm.tags.some(t => String(t).toLowerCase().includes(search));
      });
    }
    if (defaultUserFilter) {
      list = list.filter(vm => {
        const du = vm?.spec?.default_user || vm?.default_user || "";
        const effective = du === "" ? "root" : String(du).toLowerCase();
        return effective === defaultUserFilter;
      });
    }
    if (networkFilter) {
      list = list.filter(vm => {
        const nets = Array.isArray(vm?.spec?.networks) ? vm.spec.networks : [];
        return nets.some(n => String(n?.name || "").toLowerCase() === networkFilter);
      });
    }
    if (autoStart.set) {
      list = list.filter(vm => !!(vm.spec && vm.spec.auto_start) === autoStart.value);
    }
    if (locked.set) {
      list = list.filter(vm => !!(vm.spec && vm.spec.locked) === locked.value);
    }
    if (since.set || until.set) {
      list = list.filter(vm => {
        if (!vm.created_at) return false;
        const t = new Date(vm.created_at);
        if (Number.isNaN(t.getTime())) return false;
        if (since.set && t < since.value) return false;
        if (until.set && t > until.value) return false;
        return true;
      });
    }
    if (minCpusP.set || maxCpusP.set) {
      list = list.filter(vm => {
        const cpus = (vm.spec && vm.spec.cpus) || 0;
        if (minCpusP.set && cpus < minCpusP.value) return false;
        if (maxCpusP.set && cpus > maxCpusP.value) return false;
        return true;
      });
    }
    if (minRamP.set || maxRamP.set) {
      list = list.filter(vm => {
        const ram = (vm.spec && vm.spec.ram_mb) || 0;
        if (minRamP.set && ram < minRamP.value) return false;
        if (maxRamP.set && ram > maxRamP.value) return false;
        return true;
      });
    }
    if (minDiskP.set || maxDiskP.set) {
      list = list.filter(vm => {
        const disk = (vm.spec && vm.spec.disk_gb) || 0;
        if (minDiskP.set && disk < minDiskP.value) return false;
        if (maxDiskP.set && disk > maxDiskP.value) return false;
        return true;
      });
    }
    const cmp = (a, b) => {
      let l;
      switch (sortField) {
        case "name":       l = a.name.toLowerCase().localeCompare(b.name.toLowerCase()); break;
        case "created_at": l = (a.created_at || "").localeCompare(b.created_at || ""); break;
        case "state":      l = (a.state || "").localeCompare(b.state || ""); break;
        default:           l = 0;
      }
      if (l === 0) l = a.id.localeCompare(b.id); // tiebreak on id
      return order === "desc" ? -l : l;
    };
    list.sort(cmp);
    return json(res, 200, list, { "X-Total-Count": String(list.length) });
  }
  if (p === "/api/v1/vms/stats/top" && method === "GET") {
    const metric = url.searchParams.get("metric") || "cpu";
    const limit = Math.max(1, parseInt(url.searchParams.get("limit") || "5", 10) || 5);
    const state = url.searchParams.get("state") || "running";
    const list = [...vms.values()].filter(v => state === "all" || v.state === "running");
    // Synthesize a deterministic value per VM based on name length and metric
    // so the dashboard shows a stable, ranked leaderboard in mock tests.
    const seedFor = (name) => {
      let h = 0;
      for (const ch of name) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
      return h;
    };
    const metricValue = (vm) => {
      const seed = seedFor(vm.name);
      switch (metric) {
        case "cpu": return (seed % 100) + (seed % 10) / 10;
        case "mem": return (seed % 4096) + 256;
        case "disk_read":
        case "disk_write":
        case "net_rx":
        case "net_tx": return seed % 5_000_000;
        default: return seed % 100;
      }
    };
    const items = list.map(vm => ({
      vm_id: vm.id, name: vm.name, state: vm.state, value: metricValue(vm),
    }));
    items.sort((a, b) => b.value - a.value || (a.vm_id < b.vm_id ? -1 : 1));
    return json(res, 200, { metric, limit, state, items: items.slice(0, limit) });
  }
  if (p === "/api/v1/vms" && method === "POST") {
    const spec = await parseBody(req);
    const vm = createVM(spec);
    vm.ip = `192.168.100.${10 + vmCounter}`;
    return json(res, 201, vm);
  }

  let m;
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)$/)) && method === "GET") {
    const vm = vms.get(m[1]);
    return vm ? json(res, 200, vm) : json(res, 404, { error: "not found" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)$/)) && method === "PATCH") {
    const vm = vms.get(m[1]);
    if (!vm) return json(res, 404, { error: "not found" });
    const body = await parseBody(req);
    if (body.cpus > 0) vm.spec.cpus = body.cpus;
    if (body.ram_mb > 0) vm.spec.ram_mb = body.ram_mb;
    if (body.disk_gb > 0) {
      if (body.disk_gb < vm.spec.disk_gb) return json(res, 500, { error: "disk can only grow" });
      vm.spec.disk_gb = body.disk_gb;
    }
    if (body.nat_static_ip) {
      // Accept plain IP or CIDR; normalise to x.x.x.x
      const ipStr = body.nat_static_ip.replace(/\/\d+$/, "");
      vm.spec.nat_static_ip = `${ipStr}/24`;
      vm.ip = ipStr;
    }
    if (typeof body.auto_start === "boolean") {
      vm.spec.auto_start = body.auto_start;
    }
    if (typeof body.locked === "boolean") {
      vm.spec.locked = body.locked;
    }
    vm.updated_at = new Date().toISOString();
    return json(res, 200, vm);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)$/)) && method === "DELETE") {
    const vm = vms.get(m[1]);
    if (vm && vm.spec && vm.spec.locked) {
      return json(res, 409, { code: "vm_locked", message: "vm is locked; unlock it before deleting" });
    }
    vms.delete(m[1]); res.writeHead(204); return res.end();
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/start$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (vm) { vm.state = "running"; return json(res, 200, { status: "started" }); }
    return json(res, 404, { error: "not found" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/stop$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (vm) { vm.state = "stopped"; return json(res, 200, { status: "stopped" }); }
    return json(res, 404, { error: "not found" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/force-stop$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (!vm) return json(res, 404, { code: "resource_not_found", message: "not found" });
    if (vm.state === "stopped") {
      return json(res, 409, { code: "vm_already_stopped", message: "vm is already stopped" });
    }
    vm.state = "stopped";
    return json(res, 200, { status: "force_stopped" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/restart$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (vm) { vm.state = "running"; return json(res, 200, { status: "restarted" }); }
    return json(res, 404, { error: "not found" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/reboot$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (!vm) return json(res, 404, { code: "resource_not_found", message: "vm not found" });
    if (vm.state !== "running") return json(res, 409, { code: "vm_not_running", message: "vm must be running to reboot" });
    return json(res, 200, { status: "rebooted" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/suspend$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (!vm) return json(res, 404, { code: "resource_not_found", message: "vm not found" });
    if (vm.state === "paused") return json(res, 409, { code: "vm_already_paused", message: "vm is already paused" });
    if (vm.state !== "running") return json(res, 409, { code: "vm_not_running", message: "vm must be running to suspend" });
    vm.state = "paused";
    return json(res, 200, { status: "suspended" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/resume$/)) && method === "POST") {
    const vm = vms.get(m[1]);
    if (!vm) return json(res, 404, { code: "resource_not_found", message: "vm not found" });
    if (vm.state !== "paused") return json(res, 409, { code: "vm_not_paused", message: "vm must be paused to resume" });
    vm.state = "running";
    return json(res, 200, { status: "resumed" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/clone$/)) && method === "POST") {
    const source = vms.get(m[1]);
    if (!source) return json(res, 404, { error: "not found" });
    const body = await parseBody(req);
    const vm = createVM({
      name: body.name || `${source.name}-clone`,
      image: source.spec.image,
      cpus: source.spec.cpus,
      ram_mb: source.spec.ram_mb,
      disk_gb: source.spec.disk_gb,
      ssh_pub_key: source.spec.ssh_pub_key,
      default_user: source.spec.default_user,
      networks: source.spec.networks,
    });
    vm.state = "stopped";
    vm.ip = "";
    return json(res, 200, vm);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/stats$/)) && method === "GET") {
    const vmId = m[1];
    const vm = vms.get(vmId);
    if (!vm) return json(res, 404, { code: "resource_not_found", message: `vm "${vmId}" not found` });
    const intervalSeconds = 10;
    const historySize = 360;
    const now = Date.now();
    // Synthesize a deterministic 6-sample history so tests don't rely on
    // wall-clock values. Sample N is `now - (5-N)*interval` so the most
    // recent sample is `now`. Stopped VMs return frozen history with no
    // `current` to mirror the daemon's stale-sample behavior.
    const history = [];
    for (let i = 0; i < 6; i++) {
      const ts = new Date(now - (5 - i) * intervalSeconds * 1000).toISOString();
      history.push({
        timestamp: ts,
        cpu_percent: 10 + i * 5,
        mem_used_mb: 512 + i * 16,
        mem_avail_mb: 1024 - i * 16,
        disk_read_bps: 1024 * (i + 1),
        disk_write_bps: 2048 * (i + 1),
        net_rx_bps: 4096 * (i + 1),
        net_tx_bps: 8192 * (i + 1),
      });
    }
    const current = vm.state === "running" ? history[history.length - 1] : null;
    return json(res, 200, {
      vm_id: vmId,
      state: vm.state,
      last_sampled_at: history[history.length - 1].timestamp,
      current,
      history,
      interval_seconds: intervalSeconds,
      history_size: historySize,
    });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/stats\/stream$/)) && method === "GET") {
    const vmId = m[1];
    const vm = vms.get(vmId);
    if (!vm) return json(res, 404, { code: "resource_not_found", message: `vm "${vmId}" not found` });
    if (vm.state !== "running") {
      // Mirror the daemon: stream still opens but no samples are emitted while
      // the VM is not running. The frontend should show the empty state.
      res.writeHead(200, {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        "Connection": "keep-alive",
      });
      const hb = setInterval(() => res.write(": keepalive\n\n"), 1000);
      req.on("close", () => clearInterval(hb));
      return;
    }
    res.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      "Connection": "keep-alive",
      "X-Accel-Buffering": "no",
    });
    let counter = 0;
    const writeFrame = () => {
      // First frame mirrors the REST-seeded current sample (cpu_percent 35%
      // = 10 + 5*5). Subsequent frames bump each metric so the chart and the
      // 5-min average advance over time, but tests asserting the seeded
      // baseline still see "35.0%" on initial load.
      const sample = {
        timestamp: new Date().toISOString(),
        cpu_percent: 35 + counter,
        mem_used_mb: 600 + counter * 4,
        mem_avail_mb: 900 - counter * 4,
        disk_read_bps: 8192 * (counter + 1),
        disk_write_bps: 4096 * (counter + 1),
        net_rx_bps: 32768 * (counter + 1),
        net_tx_bps: 16384 * (counter + 1),
      };
      counter += 1;
      const id = String(Date.now());
      res.write(`id: ${id}\nevent: vm.stats\ndata: ${JSON.stringify(sample)}\n\n`);
    };
    writeFrame();
    const tick = setInterval(writeFrame, 600);
    const hb = setInterval(() => res.write(": keepalive\n\n"), 1500);
    req.on("close", () => { clearInterval(tick); clearInterval(hb); });
    return;
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots$/)) && method === "GET") {
    const sortField = url.searchParams.get("sort") || "id";
    const order = url.searchParams.get("order") || "asc";
    if (!["id", "name", "created_at"].includes(sortField)) {
      return json(res, 400, { code: "invalid_sort", message: "sort must be one of: id, name, created_at" });
    }
    if (!["asc", "desc"].includes(order)) {
      return json(res, 400, { code: "invalid_order", message: "order must be 'asc' or 'desc'" });
    }
    let list = [...(snapshots.get(m[1]) || [])];
    // Tag filter is applied before search so the post-filter total reflects
    // the intersection (matches the API contract).
    const tagFilter = (url.searchParams.get("tag") || "").trim().toLowerCase();
    if (tagFilter) {
      list = list.filter(s => (s.tags || []).some(t => String(t).toLowerCase() === tagFilter));
    }
    // since / until: inclusive RFC3339 time-range filter on created_at;
    // invalid value → 400; whitespace-only disables; zero/missing
    // created_at filtered OUT whenever any bound is set (mirrors the API).
    const parseTime = (raw, name) => {
      const v = (raw || "").trim();
      if (v === "") return { set: false };
      const t = new Date(v);
      if (Number.isNaN(t.getTime())) {
        return { invalid: true, code: `invalid_${name}`, msg: `${name} must be a valid RFC3339 timestamp` };
      }
      return { set: true, value: t };
    };
    const sinceP = parseTime(url.searchParams.get("since"), "since");
    if (sinceP.invalid) return json(res, 400, { code: sinceP.code, message: sinceP.msg });
    const untilP = parseTime(url.searchParams.get("until"), "until");
    if (untilP.invalid) return json(res, 400, { code: untilP.code, message: untilP.msg });
    if (sinceP.set || untilP.set) {
      list = list.filter(s => {
        if (!s.created_at) return false;
        const t = new Date(s.created_at);
        if (Number.isNaN(t.getTime())) return false;
        if (sinceP.set && t < sinceP.value) return false;
        if (untilP.set && t > untilP.value) return false;
        return true;
      });
    }
    const search = (url.searchParams.get("search") || "").trim().toLowerCase();
    if (search) {
      list = list.filter(s => {
        if ((s.name || "").toLowerCase().includes(search)) return true;
        if (s.description && s.description.toLowerCase().includes(search)) return true;
        if ((s.tags || []).some(t => String(t).toLowerCase().includes(search))) return true;
        return false;
      });
    }
    const cmp = (a, b) => {
      let l;
      switch (sortField) {
        case "name":       l = (a.name || "").toLowerCase().localeCompare((b.name || "").toLowerCase()); break;
        case "created_at": l = (a.created_at || "").localeCompare(b.created_at || ""); break;
        default:           l = 0; // id == vmID/name, so handled by tiebreak
      }
      if (l === 0) l = (a.name || "").localeCompare(b.name || "");
      return order === "desc" ? -l : l;
    };
    list.sort(cmp);
    res.setHeader("X-Total-Count", String(list.length));
    return json(res, 200, list);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots$/)) && method === "POST") {
    const body = await parseBody(req);
    const vmId = m[1];
    if (!body.name || !String(body.name).trim()) {
      return json(res, 400, { error: { code: "invalid_name", message: "snapshot name is required" } });
    }
    if (typeof body.description === "string" && body.description.length > 1024) {
      return json(res, 400, { error: { code: "invalid_description", message: "description must be at most 1024 characters" } });
    }
    // Normalise tags client-mirror style: trim + lowercase + dedupe +
    // alphabetise.  The mock-server is permissive about the tag alphabet
    // (the daemon's Go validator is the source of truth for that); the
    // Playwright + JSDOM tests only need to verify the wire shape, the
    // tag-chip rendering, and the search/filter wiring.
    let normalisedTags = null;
    if (Array.isArray(body.tags)) {
      normalisedTags = [...new Set(body.tags.map(t => String(t).trim().toLowerCase()).filter(Boolean))].sort();
    }
    const snap = {
      id: `${vmId}/${body.name}`,
      vm_id: vmId,
      name: body.name,
      created_at: new Date().toISOString(),
    };
    if (body.description) snap.description = body.description;
    if (normalisedTags && normalisedTags.length > 0) snap.tags = normalisedTags;
    const list = snapshots.get(vmId) || [];
    list.push(snap); snapshots.set(vmId, list);
    return json(res, 201, snap);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots\/bulk_delete$/)) && method === "POST") {
    const vmId = m[1];
    const body = await parseBody(req);
    const names = Array.isArray(body.names) ? body.names.filter(n => typeof n === "string" && n.trim() !== "") : [];
    const prefix = typeof body.prefix === "string" ? body.prefix.trim() : "";
    if (!names.length && !prefix) {
      return json(res, 400, { code: "invalid_bulk_request", message: "exactly one of names or prefix must be provided" });
    }
    if (names.length && prefix) {
      return json(res, 400, { code: "invalid_bulk_request", message: "names and prefix are mutually exclusive" });
    }
    const existing = snapshots.get(vmId) || [];
    const targets = prefix ? existing.filter(s => s.name.startsWith(prefix)).map(s => s.name) : names;
    const remaining = existing.filter(s => !targets.includes(s.name));
    snapshots.set(vmId, remaining);
    const existingNames = new Set(existing.map(s => s.name));
    const results = targets.map(n => existingNames.has(n)
      ? { name: n, success: true }
      : { name: n, success: false, code: "resource_not_found", message: "snapshot not found" });
    return json(res, 200, { results });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots\/([^/]+)\/restore$/)) && method === "POST") {
    return json(res, 200, { status: "restored" });
  }
  // PATCH /api/v1/vms/{vmID}/snapshots/{snapName}: edit description and/or
  // tags. Pointer semantics: undefined/missing = no change. Empty string
  // clears description; empty array clears tags.
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots\/([^/]+)$/)) && method === "PATCH") {
    const list = snapshots.get(m[1]) || [];
    const snap = list.find(s => s.name === m[2]);
    if (!snap) return json(res, 404, { error: { code: "resource_not_found", message: "snapshot not found" } });
    const patch = await parseBody(req);
    if (typeof patch.description === "string") {
      const trimmed = patch.description.trim();
      if (trimmed.length > 1024) {
        return json(res, 400, { error: { code: "invalid_description", message: "description too long" } });
      }
      snap.description = trimmed;
    }
    if (Array.isArray(patch.tags)) {
      const normalised = [...new Set(patch.tags.map(t => String(t).trim().toLowerCase()).filter(Boolean))].sort();
      if (normalised.length === 0) {
        delete snap.tags;
      } else {
        snap.tags = normalised;
      }
    }
    return json(res, 200, snap);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots\/([^/]+)$/)) && method === "DELETE") {
    const list = (snapshots.get(m[1]) || []).filter(s => s.name !== m[2]);
    snapshots.set(m[1], list); res.writeHead(204); return res.end();
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/ports$/)) && method === "GET") {
    const sortField = (url.searchParams.get("sort") || "id").toLowerCase().trim();
    const order = (url.searchParams.get("order") || "asc").toLowerCase().trim();
    const allowedSort = ["id", "host_port", "guest_port", "protocol", "description"];
    if (!allowedSort.includes(sortField)) {
      return json(res, 400, { code: "invalid_sort", message: "sort must be one of: id, host_port, guest_port, protocol, description" });
    }
    if (!["asc", "desc"].includes(order)) {
      return json(res, 400, { code: "invalid_order", message: "order must be 'asc' or 'desc'" });
    }
    const protocolFilter = (url.searchParams.get("protocol") || "").trim().toLowerCase();
    if (protocolFilter && protocolFilter !== "tcp" && protocolFilter !== "udp") {
      return json(res, 400, { code: "invalid_protocol", message: "protocol must be 'tcp' or 'udp'" });
    }
    // Host-port range filter (5.4.47): inclusive [min, max] on host_port.
    const parsePortBound = (raw, name) => {
      const v = (raw || "").trim();
      if (v === "") return { set: false };
      const n = Number(v);
      if (!Number.isInteger(n) || n < 0) return { err: name };
      return { set: true, value: n };
    };
    const minHostPort = parsePortBound(url.searchParams.get("min_host_port"), "min_host_port");
    if (minHostPort.err) {
      return json(res, 400, { code: "invalid_min_host_port", message: "min_host_port must be a non-negative integer port number" });
    }
    const maxHostPort = parsePortBound(url.searchParams.get("max_host_port"), "max_host_port");
    if (maxHostPort.err) {
      return json(res, 400, { code: "invalid_max_host_port", message: "max_host_port must be a non-negative integer port number" });
    }
    // Guest-port range filter (5.4.49): inclusive [min, max] on guest_port.
    const minGuestPort = parsePortBound(url.searchParams.get("min_guest_port"), "min_guest_port");
    if (minGuestPort.err) {
      return json(res, 400, { code: "invalid_min_guest_port", message: "min_guest_port must be a non-negative integer port number" });
    }
    const maxGuestPort = parsePortBound(url.searchParams.get("max_guest_port"), "max_guest_port");
    if (maxGuestPort.err) {
      return json(res, 400, { code: "invalid_max_guest_port", message: "max_guest_port must be a non-negative integer port number" });
    }
    let list = (portForwards.get(m[1]) || []).slice();
    const tagFilter = (url.searchParams.get("tag") || "").trim().toLowerCase();
    if (tagFilter) {
      list = list.filter(pf => Array.isArray(pf.tags) && pf.tags.some(t => t === tagFilter));
    }
    if (protocolFilter) {
      list = list.filter(pf => (pf.protocol || "").toLowerCase() === protocolFilter);
    }
    if (minHostPort.set) list = list.filter(pf => (pf.host_port || 0) >= minHostPort.value);
    if (maxHostPort.set) list = list.filter(pf => (pf.host_port || 0) <= maxHostPort.value);
    if (minGuestPort.set) list = list.filter(pf => (pf.guest_port || 0) >= minGuestPort.value);
    if (maxGuestPort.set) list = list.filter(pf => (pf.guest_port || 0) <= maxGuestPort.value);
    const search = (url.searchParams.get("search") || "").trim().toLowerCase();
    if (search) {
      list = list.filter(pf => {
        if (pf.description && pf.description.toLowerCase().includes(search)) return true;
        if ((pf.protocol || "").toLowerCase().includes(search)) return true;
        if (String(pf.host_port || "").includes(search)) return true;
        if (String(pf.guest_port || "").includes(search)) return true;
        if (pf.guest_ip && pf.guest_ip.toLowerCase().includes(search)) return true;
        if (Array.isArray(pf.tags) && pf.tags.some(t => t.includes(search))) return true;
        return false;
      });
    }
    const cmp = (a, b) => {
      let l;
      switch (sortField) {
        case "host_port":
          if (a.host_port !== b.host_port) l = a.host_port < b.host_port ? -1 : 1;
          else l = (a.id || "").localeCompare(b.id || "");
          break;
        case "guest_port":
          if (a.guest_port !== b.guest_port) l = a.guest_port < b.guest_port ? -1 : 1;
          else l = (a.id || "").localeCompare(b.id || "");
          break;
        case "protocol":
          if ((a.protocol || "") !== (b.protocol || "")) l = (a.protocol || "") < (b.protocol || "") ? -1 : 1;
          else l = (a.id || "").localeCompare(b.id || "");
          break;
        case "description": {
          const ad = (a.description || "").toLowerCase();
          const bd = (b.description || "").toLowerCase();
          if (ad !== bd) l = ad < bd ? -1 : 1;
          else l = (a.id || "").localeCompare(b.id || "");
          break;
        }
        default: // id
          l = (a.id || "").localeCompare(b.id || "");
      }
      return order === "desc" ? -l : l;
    };
    list.sort(cmp);
    const total = list.length;
    // Pagination — `per_page` (with `limit` as a synonym) and `page` mirror
    // the API's parsePagination contract. Pagination is applied after filter
    // + sort so X-Total-Count reflects the post-filter / pre-pagination
    // population.
    const perPageRaw = url.searchParams.get("per_page") || url.searchParams.get("limit") || "";
    const pageRaw = url.searchParams.get("page") || "";
    const perPage = Number.parseInt(perPageRaw.trim(), 10);
    let page = Number.parseInt(pageRaw.trim(), 10);
    if (Number.isFinite(perPage) && perPage > 0) {
      if (!Number.isFinite(page) || page < 1) page = 1;
      const start = (page - 1) * perPage;
      list = start >= list.length ? [] : list.slice(start, start + perPage);
    }
    return json(res, 200, list, { "X-Total-Count": String(total) });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/ports\/bulk_delete$/)) && method === "POST") {
    const vmId = m[1];
    const body = await parseBody(req);
    const ids = Array.isArray(body.ids) ? body.ids.filter(x => typeof x === "string" && x.trim() !== "") : [];
    const proto = typeof body.protocol === "string" ? body.protocol.trim().toLowerCase() : "";
    if (!ids.length && !proto) {
      return json(res, 400, { code: "invalid_bulk_request", message: "exactly one of ids or protocol must be provided" });
    }
    if (ids.length && proto) {
      return json(res, 400, { code: "invalid_bulk_request", message: "ids and protocol are mutually exclusive" });
    }
    if (proto && proto !== "tcp" && proto !== "udp") {
      return json(res, 400, { code: "invalid_bulk_request", message: "protocol must be 'tcp' or 'udp'" });
    }
    const existing = portForwards.get(vmId) || [];
    const known = new Set(existing.map(pf => pf.id));
    const targets = proto ? existing.filter(pf => pf.protocol === proto).map(pf => pf.id) : ids;
    const remaining = existing.filter(pf => !targets.includes(pf.id));
    portForwards.set(vmId, remaining);
    const results = targets.map(id => known.has(id)
      ? { id, success: true }
      : { id, success: false, code: "resource_not_found", message: "port forward not found" });
    return json(res, 200, { results });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/ports$/)) && method === "POST") {
    const body = await parseBody(req);
    if (typeof body.description === "string" && body.description.length > 256) {
      return json(res, 400, { code: "invalid_port_forward", message: "description must be at most 256 characters" });
    }
    let normalizedTags;
    if (Array.isArray(body.tags)) {
      const seen = new Set();
      normalizedTags = [];
      for (const t of body.tags) {
        const lower = String(t || "").trim().toLowerCase();
        if (!lower) {
          return json(res, 400, { code: "invalid_port_forward", message: "tags cannot contain empty values" });
        }
        if (lower.length > 32) {
          return json(res, 400, { code: "invalid_port_forward", message: "tags must be 1-32 characters" });
        }
        if (!/^[a-z0-9][a-z0-9._:-]*$/.test(lower)) {
          return json(res, 400, { code: "invalid_port_forward", message: "tags must contain only lowercase letters, numbers, dots, colons, underscores, or hyphens" });
        }
        if (seen.has(lower)) continue;
        seen.add(lower);
        normalizedTags.push(lower);
      }
      normalizedTags.sort();
    }
    const vm = vms.get(m[1]);
    const pf = {
      id: `pf-${Date.now()}`,
      vm_id: m[1],
      host_port: body.host_port,
      guest_port: body.guest_port,
      guest_ip: vm?.ip || "192.168.100.10",
      protocol: body.protocol || "tcp",
    };
    if (body.description) {
      pf.description = body.description;
    }
    if (normalizedTags && normalizedTags.length > 0) {
      pf.tags = normalizedTags;
    }
    const list = portForwards.get(m[1]) || []; list.push(pf); portForwards.set(m[1], list);
    return json(res, 201, pf);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/ports\/([^/]+)$/)) && method === "PATCH") {
    const vmId = m[1];
    const portId = m[2];
    const body = await parseBody(req);
    const list = portForwards.get(vmId) || [];
    const pf = list.find(x => x.id === portId);
    if (!pf) {
      return json(res, 404, { code: "resource_not_found", message: "port forward not found" });
    }
    if (typeof body.description === "string") {
      if (body.description.length > 256) {
        return json(res, 400, { code: "invalid_port_forward", message: "description must be at most 256 characters" });
      }
      pf.description = body.description.trim();
    }
    if (Array.isArray(body.tags)) {
      const seen = new Set();
      const normalizedTags = [];
      for (const t of body.tags) {
        const lower = String(t || "").trim().toLowerCase();
        if (!lower) {
          return json(res, 400, { code: "invalid_port_forward", message: "tags cannot contain empty values" });
        }
        if (lower.length > 32) {
          return json(res, 400, { code: "invalid_port_forward", message: "tags must be 1-32 characters" });
        }
        if (!/^[a-z0-9][a-z0-9._:-]*$/.test(lower)) {
          return json(res, 400, { code: "invalid_port_forward", message: "tags must contain only lowercase letters, numbers, dots, colons, underscores, or hyphens" });
        }
        if (seen.has(lower)) continue;
        seen.add(lower);
        normalizedTags.push(lower);
      }
      normalizedTags.sort();
      if (normalizedTags.length === 0) {
        delete pf.tags;
      } else {
        pf.tags = normalizedTags;
      }
    }
    return json(res, 200, pf);
  }
  if (p === "/api/v1/images" && method === "GET") {
    const sortField = url.searchParams.get("sort") || "id";
    const order = url.searchParams.get("order") || "asc";
    if (!["id", "name", "size", "created_at"].includes(sortField)) {
      return json(res, 400, { code: "invalid_sort", message: "sort must be one of: id, name, size, created_at" });
    }
    if (!["asc", "desc"].includes(order)) {
      return json(res, 400, { code: "invalid_order", message: "order must be 'asc' or 'desc'" });
    }
    let list = [...images.values()];
    const tag = (url.searchParams.get("tag") || "").trim().toLowerCase();
    if (tag) {
      list = list.filter(img => (img.tags || []).some(t => String(t).toLowerCase() === tag));
    }
    const sourceVM = (url.searchParams.get("source_vm") || "").trim().toLowerCase();
    if (sourceVM) {
      list = list.filter(img => String(img.source_vm || "").toLowerCase() === sourceVM);
    }
    // since / until: inclusive RFC3339 time-range filter on created_at;
    // invalid value → 400; whitespace-only disables; zero/missing
    // created_at filtered OUT whenever any bound is set (mirrors the API).
    const parseTime = (raw, name) => {
      const v = (raw || "").trim();
      if (v === "") return { set: false };
      const t = new Date(v);
      if (Number.isNaN(t.getTime())) {
        return { invalid: true, code: `invalid_${name}`, msg: `${name} must be a valid RFC3339 timestamp` };
      }
      return { set: true, value: t };
    };
    const sinceP = parseTime(url.searchParams.get("since"), "since");
    if (sinceP.invalid) return json(res, 400, { code: sinceP.code, message: sinceP.msg });
    const untilP = parseTime(url.searchParams.get("until"), "until");
    if (untilP.invalid) return json(res, 400, { code: untilP.code, message: untilP.msg });
    if (sinceP.set || untilP.set) {
      list = list.filter(img => {
        if (!img.created_at) return false;
        const t = new Date(img.created_at);
        if (Number.isNaN(t.getTime())) return false;
        if (sinceP.set && t < sinceP.value) return false;
        if (untilP.set && t > untilP.value) return false;
        return true;
      });
    }
    // min_size / max_size: inclusive non-negative byte-range filter on
    // size_bytes; non-numeric/negative value → 400; whitespace-only disables.
    const parseSize = (raw, name) => {
      const v = (raw || "").trim();
      if (v === "") return { set: false };
      if (!/^\d+$/.test(v)) {
        return { invalid: true, code: `invalid_${name}`, msg: `${name} must be a non-negative integer number of bytes` };
      }
      return { set: true, value: Number(v) };
    };
    const minSizeP = parseSize(url.searchParams.get("min_size"), "min_size");
    if (minSizeP.invalid) return json(res, 400, { code: minSizeP.code, message: minSizeP.msg });
    const maxSizeP = parseSize(url.searchParams.get("max_size"), "max_size");
    if (maxSizeP.invalid) return json(res, 400, { code: maxSizeP.code, message: maxSizeP.msg });
    if (minSizeP.set || maxSizeP.set) {
      list = list.filter(img => {
        const size = img.size_bytes || 0;
        if (minSizeP.set && size < minSizeP.value) return false;
        if (maxSizeP.set && size > maxSizeP.value) return false;
        return true;
      });
    }
    const search = (url.searchParams.get("search") || "").trim().toLowerCase();
    if (search) {
      list = list.filter(img => {
        if ((img.name || "").toLowerCase().includes(search)) return true;
        if ((img.description || "").toLowerCase().includes(search)) return true;
        return (img.tags || []).some(t => String(t).toLowerCase().includes(search));
      });
    }
    const cmp = (a, b) => {
      let l;
      switch (sortField) {
        case "name":       l = (a.name || "").toLowerCase().localeCompare((b.name || "").toLowerCase()); break;
        case "size":       l = (a.size_bytes || 0) - (b.size_bytes || 0); break;
        case "created_at": l = (a.created_at || "").localeCompare(b.created_at || ""); break;
        default:           l = 0;
      }
      if (l === 0) l = (a.id || "").localeCompare(b.id || "");
      return order === "desc" ? -l : l;
    };
    list.sort(cmp);
    return json(res, 200, list, { "X-Total-Count": String(list.length) });
  }
  if (p === "/api/v1/images/bulk_delete" && method === "POST") {
    const body = await parseBody(req);
    const ids = Array.isArray(body.ids) ? body.ids.filter(s => typeof s === "string" && s.trim() !== "") : [];
    const tag = typeof body.tag === "string" ? body.tag.trim() : "";
    if (!ids.length && !tag) {
      return json(res, 400, { code: "invalid_bulk_request", message: "exactly one of ids or tag must be provided" });
    }
    if (ids.length && tag) {
      return json(res, 400, { code: "invalid_bulk_request", message: "ids and tag are mutually exclusive" });
    }
    let targets = ids;
    if (tag) {
      const lc = tag.toLowerCase();
      targets = [...images.values()]
        .filter(img => (img.tags || []).some(t => String(t).toLowerCase() === lc))
        .map(img => img.id);
    }
    const results = targets.map(id => {
      if (images.has(id)) {
        images.delete(id);
        return { id, success: true };
      }
      return { id, success: false, code: "resource_not_found", message: "image not found" };
    });
    return json(res, 200, { results });
  }
  {
    const m = p.match(/^\/api\/v1\/images\/([^/]+)$/);
    if (m && method === "PATCH") {
      const id = decodeURIComponent(m[1]);
      const img = images.get(id);
      if (!img) return json(res, 404, { code: "resource_not_found", error: "image not found" });
      const body = await parseBody(req);
      if (typeof body.description === "string") {
        const trimmed = body.description.trim();
        if (trimmed.length > 1024) {
          return json(res, 400, { code: "invalid_spec", error: "description must be 1024 characters or fewer" });
        }
        if (trimmed && trimmed !== (img.description || "")) {
          img.description = trimmed;
        }
      }
      if (Array.isArray(body.tags)) {
        const tags = [...new Set(body.tags.map(t => String(t).trim().toLowerCase()).filter(Boolean))].sort();
        img.tags = tags;
      }
      img.updated_at = new Date().toISOString();
      images.set(id, img);
      return json(res, 200, img);
    }
  }
  if (p === "/api/v1/templates" && method === "POST") {
    const spec = await parseBody(req);
    const name = typeof spec.name === "string" ? spec.name.trim() : "";
    const image = typeof spec.image === "string" ? spec.image.trim() : "";
    if (!name) return json(res, 400, { code: "invalid_name", message: "template name is required" });
    if (!image) return json(res, 400, { code: "invalid_image", message: "image is required" });
    const dup = [...templates.values()].some(t => String(t.name).toLowerCase() === name.toLowerCase());
    if (dup) return json(res, 400, { code: "invalid_name", message: `template name "${name}" already exists` });
    const id = `tmpl-${Date.now()}`;
    const now = new Date().toISOString();
    const tpl = {
      id,
      name,
      image,
      cpus: spec.cpus || 0,
      ram_mb: spec.ram_mb || 0,
      disk_gb: spec.disk_gb || 0,
      description: typeof spec.description === "string" ? spec.description.trim() : "",
      tags: Array.isArray(spec.tags) ? spec.tags.slice() : [],
      default_user: typeof spec.default_user === "string" ? spec.default_user.trim() : "",
      networks: Array.isArray(spec.networks) ? spec.networks.slice() : [],
      created_at: now,
      updated_at: now,
    };
    templates.set(id, tpl);
    return json(res, 201, tpl);
  }
  if (p === "/api/v1/templates" && method === "GET") {
    let list = [...templates.values()];
    // min_cpus / max_cpus and min_ram_mb / max_ram_mb: inclusive non-negative
    // integer range filters on the template's `cpus` / `ram_mb` fields;
    // non-numeric/negative value -> 400; whitespace-only disables
    // (mirrors the VM list filters, 5.4.44 / 5.4.48).
    const parseTplCount = (raw, name) => {
      const v = (raw || "").trim();
      if (v === "") return { set: false };
      if (!/^\d+$/.test(v)) {
        return { invalid: true, code: `invalid_${name}`, msg: `${name} must be a non-negative integer` };
      }
      return { set: true, value: Number(v) };
    };
    const tplMinCpus = parseTplCount(url.searchParams.get("min_cpus"), "min_cpus");
    if (tplMinCpus.invalid) return json(res, 400, { code: tplMinCpus.code, message: tplMinCpus.msg });
    const tplMaxCpus = parseTplCount(url.searchParams.get("max_cpus"), "max_cpus");
    if (tplMaxCpus.invalid) return json(res, 400, { code: tplMaxCpus.code, message: tplMaxCpus.msg });
    const tplMinRam = parseTplCount(url.searchParams.get("min_ram_mb"), "min_ram_mb");
    if (tplMinRam.invalid) return json(res, 400, { code: tplMinRam.code, message: tplMinRam.msg });
    const tplMaxRam = parseTplCount(url.searchParams.get("max_ram_mb"), "max_ram_mb");
    if (tplMaxRam.invalid) return json(res, 400, { code: tplMaxRam.code, message: tplMaxRam.msg });
    const tplMinDisk = parseTplCount(url.searchParams.get("min_disk_gb"), "min_disk_gb");
    if (tplMinDisk.invalid) return json(res, 400, { code: tplMinDisk.code, message: tplMinDisk.msg });
    const tplMaxDisk = parseTplCount(url.searchParams.get("max_disk_gb"), "max_disk_gb");
    if (tplMaxDisk.invalid) return json(res, 400, { code: tplMaxDisk.code, message: tplMaxDisk.msg });
    const tag = (url.searchParams.get("tag") || "").trim();
    if (tag) {
      const lc = tag.toLowerCase();
      list = list.filter(t => (t.tags || []).some(x => String(x).toLowerCase() === lc));
    }
    const image = (url.searchParams.get("image") || "").trim().toLowerCase();
    if (image) {
      list = list.filter(t => String(t.image || "").toLowerCase() === image);
    }
    const defaultUser = (url.searchParams.get("default_user") || "").trim().toLowerCase();
    if (defaultUser) {
      list = list.filter(t => String(t.default_user || "").toLowerCase() === defaultUser);
    }
    // network: case-insensitive exact-match (any-of) against networks[].name.
    const network = (url.searchParams.get("network") || "").trim().toLowerCase();
    if (network) {
      list = list.filter(t => Array.isArray(t.networks) && t.networks.some(n => String(n.name || "").toLowerCase() === network));
    }
    // since / until: inclusive RFC3339 time-range filter on created_at;
    // invalid value → 400; whitespace-only disables; zero/missing
    // created_at filtered OUT whenever any bound is set (mirrors the API).
    const parseTplTime = (raw, name) => {
      const v = (raw || "").trim();
      if (v === "") return { set: false };
      const t = new Date(v);
      if (Number.isNaN(t.getTime())) {
        return { invalid: true, code: `invalid_${name}`, msg: `${name} must be a valid RFC3339 timestamp` };
      }
      return { set: true, value: t };
    };
    const tplSince = parseTplTime(url.searchParams.get("since"), "since");
    if (tplSince.invalid) return json(res, 400, { code: tplSince.code, message: tplSince.msg });
    const tplUntil = parseTplTime(url.searchParams.get("until"), "until");
    if (tplUntil.invalid) return json(res, 400, { code: tplUntil.code, message: tplUntil.msg });
    if (tplSince.set || tplUntil.set) {
      list = list.filter(t => {
        if (!t.created_at) return false;
        const ts = new Date(t.created_at);
        if (Number.isNaN(ts.getTime())) return false;
        if (tplSince.set && ts < tplSince.value) return false;
        if (tplUntil.set && ts > tplUntil.value) return false;
        return true;
      });
    }
    if (tplMinCpus.set || tplMaxCpus.set) {
      list = list.filter(t => {
        const cpus = Number(t.cpus) || 0;
        if (tplMinCpus.set && cpus < tplMinCpus.value) return false;
        if (tplMaxCpus.set && cpus > tplMaxCpus.value) return false;
        return true;
      });
    }
    if (tplMinRam.set || tplMaxRam.set) {
      list = list.filter(t => {
        const ram = Number(t.ram_mb) || 0;
        if (tplMinRam.set && ram < tplMinRam.value) return false;
        if (tplMaxRam.set && ram > tplMaxRam.value) return false;
        return true;
      });
    }
    if (tplMinDisk.set || tplMaxDisk.set) {
      list = list.filter(t => {
        const disk = Number(t.disk_gb) || 0;
        if (tplMinDisk.set && disk < tplMinDisk.value) return false;
        if (tplMaxDisk.set && disk > tplMaxDisk.value) return false;
        return true;
      });
    }
    const search = (url.searchParams.get("search") || "").trim().toLowerCase();
    if (search) {
      list = list.filter(t => {
        if (t.name && String(t.name).toLowerCase().includes(search)) return true;
        if (t.description && String(t.description).toLowerCase().includes(search)) return true;
        return Array.isArray(t.tags) && t.tags.some(x => String(x).toLowerCase().includes(search));
      });
    }
    const sort = (url.searchParams.get("sort") || "id").trim().toLowerCase();
    const order = (url.searchParams.get("order") || "asc").trim().toLowerCase();
    const allowedSort = ["id", "name", "created_at"];
    if (!allowedSort.includes(sort)) {
      return json(res, 400, { error: "sort must be one of: id, name, created_at", code: "invalid_sort" });
    }
    if (order !== "asc" && order !== "desc") {
      return json(res, 400, { error: "order must be 'asc' or 'desc'", code: "invalid_order" });
    }
    const desc = order === "desc";
    list.sort((a, b) => {
      let cmp = 0;
      if (sort === "name") {
        cmp = String(a.name || "").toLowerCase().localeCompare(String(b.name || "").toLowerCase());
        if (cmp === 0) cmp = String(a.id).localeCompare(String(b.id));
      } else if (sort === "created_at") {
        cmp = String(a.created_at || "").localeCompare(String(b.created_at || ""));
        if (cmp === 0) cmp = String(a.id).localeCompare(String(b.id));
      } else {
        cmp = String(a.id).localeCompare(String(b.id));
      }
      return desc ? -cmp : cmp;
    });
    return json(res, 200, list, { "X-Total-Count": String(list.length) });
  }
  // PATCH /api/v1/templates/{id}: edit description and/or tags. Mirror server
  // PATCH semantics — empty `description` or missing `tags` means "no change";
  // an explicit `tags: []` clears the tag set.
  {
    const m = p.match(/^\/api\/v1\/templates\/(tmpl-[^/]+)$/);
    if (m && method === "PATCH") {
      const tpl = templates.get(m[1]);
      if (!tpl) return json(res, 404, { error: "resource_not_found", code: "resource_not_found" });
      const patch = await parseBody(req);
      if (typeof patch.description === "string" && patch.description.trim() !== "") {
        tpl.description = patch.description.trim();
      }
      if (Array.isArray(patch.tags)) {
        tpl.tags = patch.tags.slice();
      }
      tpl.updated_at = new Date().toISOString();
      templates.set(tpl.id, tpl);
      return json(res, 200, tpl);
    }
  }
  if (p === "/api/v1/templates/bulk_delete" && method === "POST") {
    const body = await parseBody(req);
    const ids = Array.isArray(body.ids) ? body.ids.filter(s => typeof s === "string" && s.trim() !== "") : [];
    const tag = typeof body.tag === "string" ? body.tag.trim() : "";
    if (!ids.length && !tag) {
      return json(res, 400, { code: "invalid_bulk_request", message: "exactly one of ids or tag must be provided" });
    }
    if (ids.length && tag) {
      return json(res, 400, { code: "invalid_bulk_request", message: "ids and tag are mutually exclusive" });
    }
    let targets = ids;
    if (tag) {
      const lc = tag.toLowerCase();
      targets = [...templates.values()]
        .filter(tpl => (tpl.tags || []).some(t => String(t).toLowerCase() === lc))
        .map(tpl => tpl.id);
    }
    const results = targets.map(id => {
      if (templates.has(id)) {
        templates.delete(id);
        return { id, success: true };
      }
      return { id, success: false, code: "resource_not_found", message: "template not found" };
    });
    return json(res, 200, { results });
  }
  if (p === "/api/v1/quotas/usage" && method === "GET") {
    const list = [...vms.values()];
    const totals = list.reduce((acc, vm) => {
      acc.cpus += vm.spec.cpus || 0;
      acc.ram_mb += vm.spec.ram_mb || 0;
      acc.disk_gb += vm.spec.disk_gb || 0;
      return acc;
    }, { cpus: 0, ram_mb: 0, disk_gb: 0 });
    return json(res, 200, {
      vms: { used: list.length, limit: 0 },
      cpus: { used: totals.cpus, limit: 0 },
      ram_mb: { used: totals.ram_mb, limit: 0 },
      disk_gb: { used: totals.disk_gb, limit: 0 },
    });
  }
  if (p === "/api/v1/logs" && method === "GET") {
    // Use a fixed base timestamp so the sort tests get deterministic
    // chronological ordering across entries even when the host clock
    // ticks faster than the loop construction.
    const baseTs = Date.now();
    const ts = (offsetMs) => new Date(baseTs + offsetMs).toISOString();
    const entries = [
      { ts: ts(0), level: "info", source: "daemon", msg: "vmSmith daemon listening", fields: { addr: "0.0.0.0:8080" } },
      { ts: ts(1), level: "info", source: "api", msg: "GET /api/v1/vms", fields: { status_code: "200", duration_ms: "1", vm_id: "vm-1" } },
      { ts: ts(2), level: "info", source: "cli", msg: "vm list", fields: {} },
      { ts: ts(3), level: "warn", source: "daemon", msg: "port forward restore skipped", fields: { error: "iptables not available", vm_id: "vm-2" } },
      { ts: ts(4), level: "error", source: "api", msg: "POST /api/v1/vms", fields: { status_code: "500", duration_ms: "5", vm_id: "vm-1" } },
    ];
    // Sort whitelist mirrors internal/api/log_sort.go.
    const allowedSort = new Set(["timestamp", "level", "source"]);
    const allowedOrder = new Set(["asc", "desc"]);
    let sortField = (url.searchParams.get("sort") || "").trim().toLowerCase();
    let order = (url.searchParams.get("order") || "").trim().toLowerCase();
    if (sortField === "") sortField = "timestamp";
    else if (!allowedSort.has(sortField)) {
      return json(res, 400, { code: "invalid_sort", message: "sort must be one of: timestamp, level, source" });
    }
    if (order === "") order = "asc";
    else if (!allowedOrder.has(order)) {
      return json(res, 400, { code: "invalid_order", message: "order must be 'asc' or 'desc'" });
    }
    const level = url.searchParams.get("level") || "debug";
    const limit = parseInt(url.searchParams.get("limit") || "200", 10);
    const source = url.searchParams.get("source") || "";
    const vmIDFilter = (url.searchParams.get("vm_id") || "").trim();
    const search = (url.searchParams.get("search") || "").trim().toLowerCase();
    // 5.4.34 — time-range bounds. The daemon's parseTimeRangeParam returns
    // 400 on garbage; mirror that contract here so the frontend can exercise
    // the error path.
    const parseLogTime = (raw, name) => {
      const v = (raw || "").trim();
      if (v === "") return { set: false };
      const t = new Date(v);
      if (Number.isNaN(t.getTime())) {
        return { invalid: true, code: `invalid_${name}`, msg: `${name} must be a valid RFC3339 timestamp` };
      }
      return { set: true, value: t.getTime() };
    };
    const sinceLogP = parseLogTime(url.searchParams.get("since"), "since");
    if (sinceLogP.invalid) return json(res, 400, { code: sinceLogP.code, message: sinceLogP.msg });
    const untilLogP = parseLogTime(url.searchParams.get("until"), "until");
    if (untilLogP.invalid) return json(res, 400, { code: untilLogP.code, message: untilLogP.msg });
    const levelOrder = { debug: 0, info: 1, warn: 2, error: 3 };
    const minLevel = levelOrder[level] ?? 0;
    let filtered = entries.filter(e => (levelOrder[e.level] ?? 0) >= minLevel);
    if (source) filtered = filtered.filter(e => e.source === source);
    if (vmIDFilter) {
      // Mirror internal/logger.EntryMatchesVMID: exact-match on the
      // structured `vm_id` field only. Case-sensitive (VM IDs are
      // opaque `vm-<unix-nano>` strings).
      filtered = filtered.filter(e => e.fields && e.fields.vm_id === vmIDFilter);
    }
    if (sinceLogP.set) {
      // Mirror internal/logger.Entries: since is strict `>` (entries
      // STRICTLY after the bound), not at-or-after.
      filtered = filtered.filter(e => new Date(e.ts).getTime() > sinceLogP.value);
    }
    if (untilLogP.set) {
      // 5.4.34 — until is INCLUSIVE (at-or-before), matching the
      // snapshot/image/VM/template/webhook time-range family.
      filtered = filtered.filter(e => new Date(e.ts).getTime() <= untilLogP.value);
    }
    if (search) {
      // Mirror internal/logger.EntryMatchesSearch: message + source + level +
      // every field VALUE; field keys are intentionally excluded.
      filtered = filtered.filter(e => {
        if (e.msg && e.msg.toLowerCase().includes(search)) return true;
        if (e.source && e.source.toLowerCase().includes(search)) return true;
        if (e.level && e.level.toLowerCase().includes(search)) return true;
        if (e.fields) {
          for (const v of Object.values(e.fields)) {
            if (v && String(v).toLowerCase().includes(search)) return true;
          }
        }
        return false;
      });
    }
    // Mirror internal/logger.SortEntries: level uses severity rank
    // (debug<info<warn<error), source is case-insensitive, timestamp
    // tiebreaks ascending so identical-key entries stay deterministic.
    const desc = order === "desc";
    filtered.sort((a, b) => {
      let less;
      if (sortField === "level") {
        const ra = levelOrder[a.level] ?? -1, rb = levelOrder[b.level] ?? -1;
        if (ra !== rb) less = ra < rb;
        else if (a.ts !== b.ts) less = a.ts < b.ts;
        else less = (a.source || "").toLowerCase() < (b.source || "").toLowerCase();
      } else if (sortField === "source") {
        const sa = (a.source || "").toLowerCase(), sb = (b.source || "").toLowerCase();
        if (sa !== sb) less = sa < sb;
        else if (a.ts !== b.ts) less = a.ts < b.ts;
        else less = (a.level || "") < (b.level || "");
      } else {
        // timestamp
        if (a.ts !== b.ts) less = a.ts < b.ts;
        else less = (a.source || "").toLowerCase() < (b.source || "").toLowerCase();
      }
      const sign = less ? -1 : 1;
      return desc ? -sign : sign;
    });
    const total = filtered.length;
    if (filtered.length > limit) filtered = filtered.slice(filtered.length - limit);
    return json(res, 200, { entries: filtered, total }, { "X-Total-Count": String(total) });
  }
  if (p === "/api/v1/events/stream" && method === "GET") {
    // Mock SSE stream: emit a couple of canned events then keep the connection
    // open with a comment heartbeat so the frontend's onopen → STATE_LIVE
    // transition fires deterministically.
    res.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      "Connection": "keep-alive",
      "X-Accel-Buffering": "no",
    });
    const liveEvents = [
      { id: "100", type: "vm.started", source: "libvirt", severity: "info", vm_id: "vm-1", message: "started", occurred_at: new Date().toISOString() },
      { id: "101", type: "vm.created", source: "app",     severity: "info", vm_id: "vm-2", message: "created", occurred_at: new Date().toISOString() },
    ];
    for (const evt of liveEvents) {
      res.write(`id: ${evt.id}\nevent: ${evt.type}\ndata: ${JSON.stringify(evt)}\n\n`);
    }
    const hb = setInterval(() => res.write(": keepalive\n\n"), 1000);
    req.on("close", () => clearInterval(hb));
    return;
  }
  if (p === "/api/v1/events" && method === "GET") {
    const allEvents = [
      { id: "evt-3", type: "vm_started", source: "libvirt", severity: "info", vm_id: "vm-1", actor: "system",    message: "VM 'web-server-prod' started",            created_at: new Date(Date.now() - 30_000).toISOString() },
      { id: "evt-2", type: "vm_created", source: "app",     severity: "info", vm_id: "vm-1", actor: "ops-alice", resource_id: "tpl-rocky9-base", message: "VM 'web-server-prod' created", attributes: { template: "rocky9-base" }, created_at: new Date(Date.now() - 60_000).toISOString() },
      { id: "evt-1", type: "vm_stopped", source: "libvirt", severity: "warn", vm_id: "vm-2", actor: "system",    resource_id: "img-2",          message: "VM 'database-staging' stopped unexpectedly", created_at: new Date(Date.now() - 120_000).toISOString() },
      // evt-0 deliberately omits actor / attributes / resource_id / vm_id so
      // the Activity disclosure (hasDetails gate in web/src/pages/Activity.jsx)
      // is exercised for events that should NOT render a chevron. The type is
      // chosen to sort after vm_stopped in asc and the id is the lowest so the
      // existing 5.4.16 default-sort and sort=type/source assertions keep
      // working unchanged.
      { id: "evt-0", type: "vm_template_synced", source: "app", severity: "info", message: "Daily template sync completed", created_at: new Date(Date.now() - 180_000).toISOString() },
    ];
    // Sort whitelist mirrors internal/api/event_sort.go.
    const allowedSort = new Set(["id", "occurred_at", "type", "source", "severity"]);
    const allowedOrder = new Set(["asc", "desc"]);
    let sortField = (url.searchParams.get("sort") || "").trim().toLowerCase();
    let order = (url.searchParams.get("order") || "").trim().toLowerCase();
    if (sortField === "") sortField = "id";
    else if (!allowedSort.has(sortField)) {
      return json(res, 400, { code: "invalid_sort", message: "sort must be one of: id, occurred_at, type, source, severity" });
    }
    if (order === "") order = "desc";
    else if (!allowedOrder.has(order)) {
      return json(res, 400, { code: "invalid_order", message: "order must be 'asc' or 'desc'" });
    }

    const vmFilter = (url.searchParams.get("vm_id") || "").trim();
    const sourceFilter = (url.searchParams.get("source") || "").trim();
    const severityFilter = (url.searchParams.get("severity") || "").trim();
    // Severity floor (info < warn < error): events ranked at-or-above the
    // value match. Mirrors pkg/types.EventMeetsMinSeverity. Unknown values
    // are a 400 — same contract as the daemon.
    const severityRanks = { info: 0, warn: 1, error: 2 };
    const minSeverityFilter = (url.searchParams.get("min_severity") || "").trim().toLowerCase();
    if (minSeverityFilter && !(minSeverityFilter in severityRanks)) {
      return json(res, 400, { code: "invalid_min_severity", message: "min_severity must be one of: info, warn, error" });
    }
    const typeFilter = (url.searchParams.get("type") || "").trim();
    // Actor is case-sensitive exact-match (mirrors the API contract): the
    // raw value is trimmed but NOT lowercased; matching uses `===` not
    // localeCompare. Empty disables the filter.
    const actorFilter = (url.searchParams.get("actor") || "").trim();
    // resource_id is whitespace-trimmed but not lowercased — IDs are opaque
    // server-issued strings (e.g. snap-1747..., img-1747...) and the
    // case-sensitive contract mirrors the API.
    const resourceIDFilter = (url.searchParams.get("resource_id") || "").trim();
    // Case-insensitive prefix match on the event's Type field (e.g.
    // "snapshot." matches every snapshot.* subtype). Mirrors the
    // daemon's lower-then-HasPrefix contract.
    const typePrefixFilter = (url.searchParams.get("type_prefix") || "").trim().toLowerCase();
    const searchFilter = (url.searchParams.get("search") || "").trim().toLowerCase();
    const matchesSearch = (e) => {
      if (!searchFilter) return true;
      const haystacks = [e.message, e.type, e.source, e.severity, e.actor, e.vm_id, e.resource_id];
      for (const h of haystacks) {
        if (h && String(h).toLowerCase().includes(searchFilter)) return true;
      }
      if (e.attributes) {
        for (const v of Object.values(e.attributes)) {
          if (v && String(v).toLowerCase().includes(searchFilter)) return true;
        }
      }
      return false;
    };
    let filtered = allEvents.filter(e =>
      (!vmFilter || e.vm_id === vmFilter) &&
      (!sourceFilter || e.source === sourceFilter) &&
      (!severityFilter || e.severity === severityFilter) &&
      (!minSeverityFilter || (severityRanks[(e.severity || "").toLowerCase()] ?? 0) >= severityRanks[minSeverityFilter]) &&
      (!typeFilter || e.type === typeFilter) &&
      (!actorFilter || e.actor === actorFilter) &&
      (!resourceIDFilter || e.resource_id === resourceIDFilter) &&
      (!typePrefixFilter || (e.type && String(e.type).toLowerCase().startsWith(typePrefixFilter))) &&
      matchesSearch(e)
    );

    // Mirror pkg/types.SortEvents — all comparators tiebreak on `id`.
    const desc = order === "desc";
    const ts = (e) => {
      const v = e.occurred_at || e.created_at;
      return v ? new Date(v).getTime() : 0;
    };
    filtered.sort((a, b) => {
      const aID = a.id || "";
      const bID = b.id || "";
      const cmpID = aID < bID ? -1 : aID > bID ? 1 : 0;
      let cmp = 0;
      switch (sortField) {
        case "occurred_at": {
          cmp = ts(a) - ts(b);
          if (cmp === 0) cmp = cmpID;
          break;
        }
        case "type": {
          const at = (a.type || "").toLowerCase();
          const bt = (b.type || "").toLowerCase();
          cmp = at < bt ? -1 : at > bt ? 1 : 0;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        case "source": {
          const as = (a.source || "").toLowerCase();
          const bs = (b.source || "").toLowerCase();
          cmp = as < bs ? -1 : as > bs ? 1 : 0;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        case "severity": {
          const as = (a.severity || "").toLowerCase();
          const bs = (b.severity || "").toLowerCase();
          cmp = as < bs ? -1 : as > bs ? 1 : 0;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        default: // "id"
          cmp = cmpID;
      }
      return desc ? -cmp : cmp;
    });

    const total = filtered.length;
    return json(res, 200, filtered, { "X-Total-Count": String(total) });
  }
  if (p === "/api/v1/webhooks" && method === "GET") {
    const needle = (url.searchParams.get("search") || "").trim().toLowerCase();
    const tagFilter = (url.searchParams.get("tag") || "").trim().toLowerCase();
    const eventTypeFilter = (url.searchParams.get("event_type") || "").trim().toLowerCase();
    // Delivery-status filter (5.4.35) — mirror IsValidWebhookDeliveryStatus.
    const deliveryStatusFilter = (url.searchParams.get("delivery_status") || "").trim().toLowerCase();
    if (deliveryStatusFilter && !["never", "healthy", "failing"].includes(deliveryStatusFilter)) {
      return json(res, 400, { code: "invalid_delivery_status", message: "delivery_status must be one of: never, healthy, failing" });
    }
    // Active filter (5.4.37) — tristate boolean, mirror parseTristateBoolParam.
    const activeRaw = (url.searchParams.get("active") || "").trim().toLowerCase();
    let activeFilter = null;
    if (activeRaw === "true" || activeRaw === "1") activeFilter = true;
    else if (activeRaw === "false" || activeRaw === "0") activeFilter = false;
    else if (activeRaw !== "") {
      return json(res, 400, { code: "invalid_active", message: "active must be 'true' or 'false'" });
    }
    // Whitelisted sort + order, mirroring internal/api/webhook_sort.go.
    const allowedSort = new Set(["id", "url", "created_at", "last_delivery_at"]);
    const allowedOrder = new Set(["asc", "desc"]);
    let sortField = (url.searchParams.get("sort") || "").trim().toLowerCase();
    let order = (url.searchParams.get("order") || "").trim().toLowerCase();
    if (sortField === "") sortField = "id";
    else if (!allowedSort.has(sortField)) {
      return json(res, 400, { code: "invalid_sort", message: "sort must be one of: id, url, created_at, last_delivery_at" });
    }
    if (order === "") order = "asc";
    else if (!allowedOrder.has(order)) {
      return json(res, 400, { code: "invalid_order", message: "order must be 'asc' or 'desc'" });
    }

    // Time-range filter on created_at — mirror snapshotInTimeRange. Whitespace
    // trimmed; invalid → 400 invalid_since / invalid_until; zero-time excluded
    // when any bound is set. (5.4.32)
    const sinceRaw = (url.searchParams.get("since") || "").trim();
    const untilRaw = (url.searchParams.get("until") || "").trim();
    let sinceTime = null;
    let untilTime = null;
    if (sinceRaw) {
      const t = new Date(sinceRaw);
      if (isNaN(t.getTime())) {
        return json(res, 400, { code: "invalid_since", message: "since must be a valid RFC3339 timestamp" });
      }
      sinceTime = t;
    }
    if (untilRaw) {
      const t = new Date(untilRaw);
      if (isNaN(t.getTime())) {
        return json(res, 400, { code: "invalid_until", message: "until must be a valid RFC3339 timestamp" });
      }
      untilTime = t;
    }

    let hooks = [...webhookList.values()];
    if (tagFilter) {
      hooks = hooks.filter((wh) => Array.isArray(wh.tags)
        && wh.tags.some((t) => typeof t === "string" && t.toLowerCase() === tagFilter));
    }
    if (eventTypeFilter) {
      // Mirror internal/api/handlers_webhook.go::webhookSubscribedToEventType:
      // case-insensitive exact-match on the event_types filter list. Catch-all
      // webhooks (empty event_types) are NOT matched.
      hooks = hooks.filter((wh) => Array.isArray(wh.event_types)
        && wh.event_types.some((t) => typeof t === "string" && t.trim().toLowerCase() === eventTypeFilter));
    }
    if (sinceTime || untilTime) {
      hooks = hooks.filter((wh) => {
        if (!wh.created_at) return false; // zero-time excluded when any bound set
        const ct = new Date(wh.created_at);
        if (isNaN(ct.getTime()) || ct.getTime() === 0) return false;
        if (sinceTime && ct.getTime() < sinceTime.getTime()) return false;
        if (untilTime && ct.getTime() > untilTime.getTime()) return false;
        return true;
      });
    }
    if (deliveryStatusFilter) {
      // Mirror pkg/types.WebhookDeliveryStatus (5.4.35).
      hooks = hooks.filter((wh) => {
        const lastAt = wh.last_delivery_at && new Date(wh.last_delivery_at);
        const hasLast = lastAt && !isNaN(lastAt.getTime()) && lastAt.getTime() !== 0;
        const status = typeof wh.last_status === "number" ? wh.last_status : 0;
        const lastErr = typeof wh.last_error === "string" ? wh.last_error : "";
        let classification = "never";
        if (hasLast) {
          classification = (lastErr === "" && status >= 200 && status < 300) ? "healthy" : "failing";
        }
        return classification === deliveryStatusFilter;
      });
    }
    if (activeFilter !== null) {
      hooks = hooks.filter((wh) => Boolean(wh.active) === activeFilter);
    }
    if (needle) {
      // Mirror pkg/types.WebhookMatchesSearch: URL + description + event_types
      // + tags. Secret, ID, and last_error are intentionally excluded from
      // the haystack.
      hooks = hooks.filter((wh) => {
        if (typeof wh.url === "string" && wh.url.toLowerCase().includes(needle)) return true;
        if (typeof wh.description === "string" && wh.description !== "" && wh.description.toLowerCase().includes(needle)) return true;
        if (Array.isArray(wh.event_types)) {
          for (const et of wh.event_types) {
            if (typeof et === "string" && et.toLowerCase().includes(needle)) return true;
          }
        }
        if (Array.isArray(wh.tags)) {
          for (const t of wh.tags) {
            if (typeof t === "string" && t.toLowerCase().includes(needle)) return true;
          }
        }
        return false;
      });
    }

    // Mirror pkg/types.SortWebhooks. All comparators tiebreak on `id`.
    const desc = order === "desc";
    hooks.sort((a, b) => {
      const aID = a.id || "";
      const bID = b.id || "";
      const cmpID = aID < bID ? -1 : aID > bID ? 1 : 0;
      let cmp = 0;
      switch (sortField) {
        case "url": {
          const au = (a.url || "").toLowerCase();
          const bu = (b.url || "").toLowerCase();
          cmp = au < bu ? -1 : au > bu ? 1 : 0;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        case "created_at": {
          const at = a.created_at ? new Date(a.created_at).getTime() : 0;
          const bt = b.created_at ? new Date(b.created_at).getTime() : 0;
          cmp = at - bt;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        case "last_delivery_at": {
          // never-delivered (empty / RFC3339 zero) sorts last in asc, first in desc
          const az = !a.last_delivery_at || a.last_delivery_at === "" ||
            new Date(a.last_delivery_at).getTime() <= 0;
          const bz = !b.last_delivery_at || b.last_delivery_at === "" ||
            new Date(b.last_delivery_at).getTime() <= 0;
          if (az !== bz) {
            cmp = az ? 1 : -1;
            break;
          }
          const at = a.last_delivery_at ? new Date(a.last_delivery_at).getTime() : 0;
          const bt = b.last_delivery_at ? new Date(b.last_delivery_at).getTime() : 0;
          cmp = at - bt;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        default: // "id"
          cmp = cmpID;
      }
      return desc ? -cmp : cmp;
    });

    // Pagination. parsePagination accepts `page` + `per_page` (or `limit` as
    // a synonym for `per_page`). X-Total-Count reflects the post-filter /
    // pre-pagination population so the GUI can render a stable page indicator.
    const total = hooks.length;
    const pageRaw = parseInt(url.searchParams.get("page") || "1", 10);
    const perPageRaw = parseInt(
      url.searchParams.get("per_page") || url.searchParams.get("limit") || "0",
      10,
    );
    const page = isNaN(pageRaw) || pageRaw < 1 ? 1 : pageRaw;
    const perPage = isNaN(perPageRaw) || perPageRaw <= 0 ? 0 : perPageRaw;
    if (perPage > 0) {
      const start = (page - 1) * perPage;
      hooks = hooks.slice(start, start + perPage);
    }
    return json(res, 200, hooks, { "X-Total-Count": String(total) });
  }
  if (p === "/api/v1/webhooks/bulk_delete" && method === "POST") {
    const body = await parseBody(req);
    const eventType = typeof body.event_type === "string" ? body.event_type.trim() : "";
    const cleanedIds = Array.isArray(body.ids)
      ? body.ids.map((s) => (typeof s === "string" ? s.trim() : "")).filter(Boolean)
      : [];
    if (eventType === "" && cleanedIds.length === 0) {
      return json(res, 400, {
        code: "invalid_bulk_request",
        message: "exactly one of ids or event_type must be provided",
      });
    }
    if (eventType !== "" && cleanedIds.length > 0) {
      return json(res, 400, {
        code: "invalid_bulk_request",
        message: "ids and event_type are mutually exclusive",
      });
    }
    let targets = cleanedIds;
    if (eventType !== "") {
      targets = [];
      for (const wh of webhookList.values()) {
        // Explicit-membership match. Catch-all webhooks (no event_types) are
        // NOT swept by the categorical selector — mirror the API contract.
        if (Array.isArray(wh.event_types) && wh.event_types.some((t) => typeof t === "string" && t.trim() === eventType)) {
          targets.push(wh.id);
        }
      }
    }
    const results = [];
    for (const id of targets) {
      if (!webhookList.has(id)) {
        results.push({ id, success: false, code: "resource_not_found", message: "webhook not found" });
        continue;
      }
      webhookList.delete(id);
      results.push({ id, success: true });
    }
    return json(res, 200, { results });
  }
  if (p === "/api/v1/webhooks" && method === "POST") {
    const body = await parseBody(req);
    const url2 = (body.url || "").trim();
    const secret = (body.secret || "").trim();
    if (!url2 || (!url2.startsWith("http://") && !url2.startsWith("https://"))) {
      return json(res, 400, { code: "invalid_url", message: "url must be http(s)" });
    }
    if (!secret) {
      return json(res, 400, { code: "missing_secret", message: "secret is required" });
    }
    const description = typeof body.description === "string" ? body.description.trim() : "";
    if (description.length > 1024) {
      return json(res, 400, { code: "invalid_webhook", message: "description must be 1024 characters or fewer" });
    }
    const { tags, err: tagsErr } = normalizeWebhookTags(body.tags);
    if (tagsErr) return json(res, 400, tagsErr);
    webhookCounter++;
    const wh = {
      id: `wh-${webhookCounter}`,
      url: url2,
      event_types: Array.isArray(body.event_types) ? body.event_types : null,
      description: description || "",
      tags: tags && tags.length ? tags : null,
      active: true,
      created_at: new Date().toISOString(),
    };
    webhookList.set(wh.id, wh);
    return json(res, 201, wh);
  }
  if ((m = p.match(/^\/api\/v1\/webhooks\/([^/]+)$/)) && method === "DELETE") {
    if (!webhookList.has(m[1])) {
      return json(res, 404, { code: "resource_not_found", message: "webhook not found" });
    }
    webhookList.delete(m[1]);
    res.writeHead(204);
    return res.end();
  }
  if ((m = p.match(/^\/api\/v1\/webhooks\/([^/]+)$/)) && method === "PATCH") {
    const wh = webhookList.get(m[1]);
    if (!wh) {
      return json(res, 404, { code: "resource_not_found", message: "webhook not found" });
    }
    const body = await parseBody(req);
    const hasURL = Object.prototype.hasOwnProperty.call(body, "url");
    const hasSecret = Object.prototype.hasOwnProperty.call(body, "secret");
    const hasEventTypes = Object.prototype.hasOwnProperty.call(body, "event_types");
    const hasActive = Object.prototype.hasOwnProperty.call(body, "active");
    const hasDescription = Object.prototype.hasOwnProperty.call(body, "description");
    const hasTags = Object.prototype.hasOwnProperty.call(body, "tags");
    if (!hasURL && !hasSecret && !hasEventTypes && !hasActive && !hasDescription && !hasTags) {
      return json(res, 400, { code: "noop_update", message: "no fields to update" });
    }
    if (hasURL) {
      const next = (body.url || "").trim();
      if (!next || (!next.startsWith("http://") && !next.startsWith("https://"))) {
        return json(res, 400, { code: "invalid_url", message: "url must be http(s)" });
      }
      wh.url = next;
    }
    if (hasSecret) {
      const next = (body.secret || "").trim();
      if (!next) {
        return json(res, 400, { code: "missing_secret", message: "secret cannot be empty" });
      }
      // Server-side rotation; secret is never echoed back.
    }
    if (hasEventTypes) {
      if (!Array.isArray(body.event_types)) {
        return json(res, 400, { code: "invalid_event_types", message: "event_types must be an array" });
      }
      const next = body.event_types
        .map((s) => (typeof s === "string" ? s.trim() : ""))
        .filter(Boolean);
      wh.event_types = next.length ? next : null;
    }
    if (hasActive) {
      wh.active = Boolean(body.active);
    }
    if (hasDescription) {
      const next = typeof body.description === "string" ? body.description.trim() : "";
      if (next.length > 1024) {
        return json(res, 400, { code: "invalid_webhook", message: "description must be 1024 characters or fewer" });
      }
      wh.description = next;
    }
    if (hasTags) {
      const { tags, err: tagsErr } = normalizeWebhookTags(body.tags);
      if (tagsErr) return json(res, 400, tagsErr);
      wh.tags = tags && tags.length ? tags : null;
    }
    webhookList.set(wh.id, wh);
    return json(res, 200, wh);
  }
  if ((m = p.match(/^\/api\/v1\/webhooks\/([^/]+)\/test$/)) && method === "POST") {
    const wh = webhookList.get(m[1]);
    if (!wh) {
      return json(res, 404, { code: "resource_not_found", message: "webhook not found" });
    }
    // Synthesize a successful test result: receivers whose URL contains "fail"
    // probe as a failure so tests can exercise both branches deterministically.
    const fail = wh.url.includes("fail");
    const now = new Date().toISOString();
    const result = fail
      ? { success: false, status_code: 500, error: "HTTP 500", duration_ms: 12, attempted_at: now, event_id: `wh-test-${Date.now()}` }
      : { success: true, status_code: 204, duration_ms: 14, attempted_at: now, event_id: `wh-test-${Date.now()}` };
    wh.last_delivery_at = now;
    if (result.success) {
      wh.last_status = result.status_code;
      delete wh.last_error;
    } else {
      wh.last_status = result.status_code || 0;
      wh.last_error = result.error;
    }
    webhookList.set(wh.id, wh);
    return json(res, 200, result);
  }
  // --- Schedules (5.2.9) ---
  if (p === "/api/v1/schedules" && method === "GET") {
    const vmIdFilter = (url.searchParams.get("vm_id") || "").trim();
    const tagSelectorFilter = (url.searchParams.get("tag_selector") || "").trim().toLowerCase();
    const actionFilter = (url.searchParams.get("action") || "").trim();
    const catchUpFilter = (url.searchParams.get("catch_up_policy") || "").trim().toLowerCase();
    if (catchUpFilter !== "" && !["skip", "run_once", "run_all"].includes(catchUpFilter)) {
      return json(res, 400, { code: "invalid_catch_up_policy", message: "catch_up_policy must be one of: skip, run_once, run_all" });
    }
    // Case-sensitive exact-match against stored Timezone (mirrors the API).
    // Whitespace-trimmed; empty disables the filter; no default-fallback.
    const timezoneFilter = (url.searchParams.get("timezone") || "").trim();
    const needle = (url.searchParams.get("search") || "").trim().toLowerCase();
    const enabledRaw = (url.searchParams.get("enabled") || "").trim().toLowerCase();
    let enabledFilter = null;
    if (enabledRaw !== "") {
      if (enabledRaw === "true" || enabledRaw === "1") enabledFilter = true;
      else if (enabledRaw === "false" || enabledRaw === "0") enabledFilter = false;
      else return json(res, 400, { code: "invalid_enabled", message: "enabled must be 'true' or 'false'" });
    }
    const allowedSort = new Set(["id", "name", "created_at", "next_fire_at"]);
    const allowedOrder = new Set(["asc", "desc"]);
    let sortField = (url.searchParams.get("sort") || "").trim().toLowerCase();
    let order = (url.searchParams.get("order") || "").trim().toLowerCase();
    if (sortField === "") sortField = "id";
    else if (!allowedSort.has(sortField)) {
      return json(res, 400, { code: "invalid_sort", message: "sort must be one of: id, name, created_at, next_fire_at" });
    }
    if (order === "") order = "asc";
    else if (!allowedOrder.has(order)) {
      return json(res, 400, { code: "invalid_order", message: "order must be 'asc' or 'desc'" });
    }
    // since / until: inclusive RFC3339 time-range filter on created_at;
    // invalid → 400 invalid_since/invalid_until; a schedule with a zero
    // created_at filtered OUT whenever any bound is set (mirrors the API).
    const parseTime = (name) => {
      const raw = (url.searchParams.get(name) || "").trim();
      if (raw === "") return { set: false, value: null };
      const ts = new Date(raw);
      if (Number.isNaN(ts.getTime())) return { set: false, value: null, invalid: true };
      return { set: true, value: ts };
    };
    const since = parseTime("since");
    if (since.invalid) {
      return json(res, 400, { code: "invalid_since", message: "since must be a valid RFC3339 timestamp" });
    }
    const until = parseTime("until");
    if (until.invalid) {
      return json(res, 400, { code: "invalid_until", message: "until must be a valid RFC3339 timestamp" });
    }

    let list = [...scheduleList.values()];
    if (vmIdFilter) list = list.filter((s) => (s.vm_id || "") === vmIdFilter);
    if (tagSelectorFilter) {
      list = list.filter((s) =>
        Array.isArray(s.tag_selector) &&
        s.tag_selector.some((t) => typeof t === "string" && t.toLowerCase() === tagSelectorFilter),
      );
    }
    if (actionFilter) list = list.filter((s) => (s.action || "") === actionFilter);
    if (catchUpFilter) list = list.filter((s) => ((s.catch_up_policy || "skip").toLowerCase()) === catchUpFilter);
    if (timezoneFilter) list = list.filter((s) => (s.timezone || "") === timezoneFilter);
    if (enabledFilter !== null) list = list.filter((s) => Boolean(s.enabled) === enabledFilter);
    if (since.set || until.set) {
      list = list.filter((s) => {
        if (!s.created_at) return false;
        const t = new Date(s.created_at);
        if (Number.isNaN(t.getTime())) return false;
        if (since.set && t < since.value) return false;
        if (until.set && t > until.value) return false;
        return true;
      });
    }
    if (needle) {
      list = list.filter((s) => {
        if (typeof s.name === "string" && s.name.toLowerCase().includes(needle)) return true;
        if (typeof s.action === "string" && s.action.toLowerCase().includes(needle)) return true;
        if (typeof s.vm_id === "string" && s.vm_id.toLowerCase().includes(needle)) return true;
        if (Array.isArray(s.tag_selector)) {
          for (const t of s.tag_selector) {
            if (typeof t === "string" && t.toLowerCase().includes(needle)) return true;
          }
        }
        return false;
      });
    }

    const desc = order === "desc";
    list.sort((a, b) => {
      const aID = a.id || "";
      const bID = b.id || "";
      const cmpID = aID < bID ? -1 : aID > bID ? 1 : 0;
      let cmp = 0;
      switch (sortField) {
        case "name": {
          const an = (a.name || "").toLowerCase();
          const bn = (b.name || "").toLowerCase();
          cmp = an < bn ? -1 : an > bn ? 1 : 0;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        case "created_at": {
          const at = a.created_at ? new Date(a.created_at).getTime() : 0;
          const bt = b.created_at ? new Date(b.created_at).getTime() : 0;
          cmp = at - bt;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        case "next_fire_at": {
          // null next_fire sorts last in asc, first in desc.
          const az = !a.next_fire_at;
          const bz = !b.next_fire_at;
          if (az !== bz) { cmp = az ? 1 : -1; break; }
          const at = a.next_fire_at ? new Date(a.next_fire_at).getTime() : 0;
          const bt = b.next_fire_at ? new Date(b.next_fire_at).getTime() : 0;
          cmp = at - bt;
          if (cmp === 0) cmp = cmpID;
          break;
        }
        default:
          cmp = cmpID;
      }
      return desc ? -cmp : cmp;
    });

    const total = list.length;
    const pageRaw = parseInt(url.searchParams.get("page") || "1", 10);
    const perPageRaw = parseInt(url.searchParams.get("per_page") || url.searchParams.get("limit") || "0", 10);
    const page = isNaN(pageRaw) || pageRaw < 1 ? 1 : pageRaw;
    const perPage = isNaN(perPageRaw) || perPageRaw <= 0 ? 0 : perPageRaw;
    if (perPage > 0) {
      const start = (page - 1) * perPage;
      list = list.slice(start, start + perPage);
    }
    return json(res, 200, list, { "X-Total-Count": String(total) });
  }
  if (p === "/api/v1/schedules" && method === "POST") {
    const body = await parseBody(req);
    const name = typeof body.name === "string" ? body.name.trim() : "";
    if (!name || name.length > 128) {
      return json(res, 400, { code: "invalid_name", message: "name must be 1-128 characters" });
    }
    const action = typeof body.action === "string" ? body.action.trim() : "";
    if (!["snapshot", "start", "stop", "restart"].includes(action)) {
      return json(res, 400, { code: "invalid_action", message: "action must be one of: snapshot, start, stop, restart" });
    }
    const cronSpec = typeof body.cron_spec === "string" ? body.cron_spec.trim() : "";
    if (!cronSpec || cronSpec.split(/\s+/).length !== 6) {
      return json(res, 400, { code: "invalid_cron_spec", message: "cron_spec must be a 6-field cron expression" });
    }
    const vmId = typeof body.vm_id === "string" ? body.vm_id.trim() : "";
    const tagSelector = Array.isArray(body.tag_selector)
      ? body.tag_selector.map((t) => (typeof t === "string" ? t.trim().toLowerCase() : "")).filter(Boolean)
      : [];
    if (vmId && tagSelector.length) {
      return json(res, 400, { code: "invalid_target", message: "vm_id and tag_selector are mutually exclusive" });
    }
    const catchUp = typeof body.catch_up_policy === "string" && body.catch_up_policy !== "" ? body.catch_up_policy : "skip";
    if (!["skip", "run_once", "run_all"].includes(catchUp)) {
      return json(res, 400, { code: "invalid_catch_up_policy", message: "catch_up_policy must be one of: skip, run_once, run_all" });
    }
    scheduleCounter++;
    const now = new Date().toISOString();
    const schedule = {
      id: `sch-new-${scheduleCounter}`,
      name,
      vm_id: vmId,
      tag_selector: tagSelector.length ? tagSelector : null,
      action,
      cron_spec: cronSpec,
      timezone: typeof body.timezone === "string" ? body.timezone.trim() : "",
      enabled: body.enabled === undefined ? true : Boolean(body.enabled),
      catch_up_policy: catchUp,
      max_concurrent: Number.isFinite(body.max_concurrent) ? body.max_concurrent : 0,
      retention_count: Number.isFinite(body.retention_count) ? body.retention_count : 0,
      params: body.params && typeof body.params === "object" ? body.params : {},
      created_at: now,
      updated_at: now,
      last_fired_at: null,
      last_result: "",
      next_fire_at: now,
    };
    scheduleList.set(schedule.id, schedule);
    scheduleRuns.set(schedule.id, []);
    return json(res, 201, schedule);
  }
  if ((m = p.match(/^\/api\/v1\/schedules\/([^/]+)\/runs$/)) && method === "GET") {
    if (!scheduleList.has(m[1])) {
      return json(res, 404, { code: "resource_not_found", message: "schedule not found" });
    }
    const statusFilter = (url.searchParams.get("status") || "").trim().toLowerCase();
    const validStatuses = ["running", "success", "error", "skipped"];
    if (statusFilter && !validStatuses.includes(statusFilter)) {
      return json(res, 400, { code: "invalid_status", message: "status must be one of: running, success, error, skipped" });
    }
    const vmIDFilter = (url.searchParams.get("vm_id") || "").trim();
    const since = (url.searchParams.get("since") || "").trim();
    const until = (url.searchParams.get("until") || "").trim();
    const sinceMs = since ? Date.parse(since) : NaN;
    const untilMs = until ? Date.parse(until) : NaN;
    if (since && isNaN(sinceMs)) {
      return json(res, 400, { code: "invalid_since", message: "since must be a valid RFC3339 timestamp" });
    }
    if (until && isNaN(untilMs)) {
      return json(res, 400, { code: "invalid_until", message: "until must be a valid RFC3339 timestamp" });
    }
    const all = (scheduleRuns.get(m[1]) || []).filter((run) => {
      if (statusFilter && String(run.status).toLowerCase() !== statusFilter) return false;
      if (vmIDFilter && String(run.vm_id || "") !== vmIDFilter) return false;
      if (since || until) {
        const startMs = run.started_at ? Date.parse(run.started_at) : NaN;
        if (isNaN(startMs)) return false;
        if (!isNaN(sinceMs) && startMs < sinceMs) return false;
        if (!isNaN(untilMs) && startMs > untilMs) return false;
      }
      return true;
    });
    const total = all.length;
    const pageRaw = parseInt(url.searchParams.get("page") || "1", 10);
    const perPageRaw = parseInt(url.searchParams.get("per_page") || url.searchParams.get("limit") || "0", 10);
    const page = isNaN(pageRaw) || pageRaw < 1 ? 1 : pageRaw;
    const perPage = isNaN(perPageRaw) || perPageRaw <= 0 ? 0 : perPageRaw;
    let runs = all;
    if (perPage > 0) {
      const start = (page - 1) * perPage;
      runs = all.slice(start, start + perPage);
    }
    return json(res, 200, runs, { "X-Total-Count": String(total) });
  }
  if ((m = p.match(/^\/api\/v1\/schedules\/([^/]+)\/run-now$/)) && method === "POST") {
    const schedule = scheduleList.get(m[1]);
    if (!schedule) {
      return json(res, 404, { code: "resource_not_found", message: "schedule not found" });
    }
    scheduleRunCounter++;
    const now = new Date().toISOString();
    const run = {
      id: `run-now-${scheduleRunCounter}`,
      schedule_id: schedule.id,
      vm_id: schedule.vm_id || "",
      started_at: now,
      finished_at: now,
      status: "success",
    };
    const existing = scheduleRuns.get(schedule.id) || [];
    scheduleRuns.set(schedule.id, [run, ...existing]);
    schedule.last_fired_at = now;
    schedule.last_result = "success";
    schedule.updated_at = now;
    scheduleList.set(schedule.id, schedule);
    return json(res, 200, schedule);
  }
  if ((m = p.match(/^\/api\/v1\/schedules\/([^/]+)$/)) && method === "GET") {
    const schedule = scheduleList.get(m[1]);
    if (!schedule) {
      return json(res, 404, { code: "resource_not_found", message: "schedule not found" });
    }
    return json(res, 200, schedule);
  }
  if ((m = p.match(/^\/api\/v1\/schedules\/([^/]+)$/)) && method === "PATCH") {
    const schedule = scheduleList.get(m[1]);
    if (!schedule) {
      return json(res, 404, { code: "resource_not_found", message: "schedule not found" });
    }
    const body = await parseBody(req);
    const editable = ["name", "vm_id", "tag_selector", "action", "cron_spec", "timezone", "enabled", "catch_up_policy", "max_concurrent", "retention_count", "params"];
    const present = editable.filter((k) => Object.prototype.hasOwnProperty.call(body, k));
    if (present.length === 0) {
      return json(res, 400, { code: "noop_update", message: "no fields to update" });
    }
    if (Object.prototype.hasOwnProperty.call(body, "name")) {
      const next = typeof body.name === "string" ? body.name.trim() : "";
      if (!next || next.length > 128) return json(res, 400, { code: "invalid_name", message: "name must be 1-128 characters" });
      schedule.name = next;
    }
    if (Object.prototype.hasOwnProperty.call(body, "action")) {
      const next = typeof body.action === "string" ? body.action.trim() : "";
      if (!["snapshot", "start", "stop", "restart"].includes(next)) {
        return json(res, 400, { code: "invalid_action", message: "invalid action" });
      }
      schedule.action = next;
    }
    if (Object.prototype.hasOwnProperty.call(body, "cron_spec")) {
      const next = typeof body.cron_spec === "string" ? body.cron_spec.trim() : "";
      if (!next || next.split(/\s+/).length !== 6) {
        return json(res, 400, { code: "invalid_cron_spec", message: "cron_spec must be a 6-field cron expression" });
      }
      schedule.cron_spec = next;
    }
    if (Object.prototype.hasOwnProperty.call(body, "vm_id")) {
      schedule.vm_id = typeof body.vm_id === "string" ? body.vm_id.trim() : "";
    }
    if (Object.prototype.hasOwnProperty.call(body, "tag_selector")) {
      const next = Array.isArray(body.tag_selector)
        ? body.tag_selector.map((t) => (typeof t === "string" ? t.trim().toLowerCase() : "")).filter(Boolean)
        : [];
      schedule.tag_selector = next.length ? next : null;
    }
    if (schedule.vm_id && schedule.tag_selector && schedule.tag_selector.length) {
      return json(res, 400, { code: "invalid_target", message: "vm_id and tag_selector are mutually exclusive" });
    }
    if (Object.prototype.hasOwnProperty.call(body, "timezone")) {
      schedule.timezone = typeof body.timezone === "string" ? body.timezone.trim() : "";
    }
    if (Object.prototype.hasOwnProperty.call(body, "enabled")) {
      schedule.enabled = Boolean(body.enabled);
    }
    if (Object.prototype.hasOwnProperty.call(body, "catch_up_policy")) {
      const next = body.catch_up_policy;
      if (!["skip", "run_once", "run_all"].includes(next)) {
        return json(res, 400, { code: "invalid_catch_up_policy", message: "invalid catch_up_policy" });
      }
      schedule.catch_up_policy = next;
    }
    if (Object.prototype.hasOwnProperty.call(body, "max_concurrent")) {
      schedule.max_concurrent = Number.isFinite(body.max_concurrent) ? body.max_concurrent : 0;
    }
    if (Object.prototype.hasOwnProperty.call(body, "retention_count")) {
      schedule.retention_count = Number.isFinite(body.retention_count) ? body.retention_count : 0;
    }
    if (Object.prototype.hasOwnProperty.call(body, "params")) {
      schedule.params = body.params && typeof body.params === "object" ? body.params : {};
    }
    schedule.updated_at = new Date().toISOString();
    scheduleList.set(schedule.id, schedule);
    return json(res, 200, schedule);
  }
  if ((m = p.match(/^\/api\/v1\/schedules\/([^/]+)$/)) && method === "DELETE") {
    if (!scheduleList.has(m[1])) {
      return json(res, 404, { code: "resource_not_found", message: "schedule not found" });
    }
    scheduleList.delete(m[1]);
    scheduleRuns.delete(m[1]);
    res.writeHead(204);
    return res.end();
  }

  if (p === "/api/v1/host/interfaces" && method === "GET") {
    return json(res, 200, [
      { name: "eth0", ips: ["10.21.100.101/24"], mac: "52:54:00:00:00:01", is_up: true, is_physical: true },
      { name: "eth1", ips: ["192.168.1.16/24"], mac: "52:54:00:00:00:02", is_up: true, is_physical: true },
    ]);
  }
  if (p === "/api/v1/host/stats" && method === "GET") {
    return json(res, 200, {
      vm_count: vms.length,
      cpu: { used: 12, total: 100, available: 88, percentage: 12 },
      ram: { used: 4 * 1024 * 1024 * 1024, total: 8 * 1024 * 1024 * 1024, available: 4 * 1024 * 1024 * 1024, percentage: 50 },
      disk: { used: 20 * 1024 * 1024 * 1024, total: 100 * 1024 * 1024 * 1024, available: 80 * 1024 * 1024 * 1024, percentage: 20 },
      event_stream_connections: 2,
    });
  }

  // Serve the real built SPA when available, otherwise fall back to the lightweight test HTML.
  if (!p.startsWith("/api/")) {
    if (p === "/" || p === "/index.html") {
      resetState();
    }

    if (fs.existsSync(DIST_INDEX)) {
      const reqPath = p === "/" ? "/index.html" : p;
      const filePath = path.join(DIST_DIR, reqPath.replace(/^\//, ""));
      if (filePath.startsWith(DIST_DIR) && fs.existsSync(filePath) && fs.statSync(filePath).isFile()) {
        const ext = path.extname(filePath);
        const contentType = {
          ".html": "text/html",
          ".js": "application/javascript",
          ".css": "text/css",
          ".json": "application/json",
          ".svg": "image/svg+xml",
          ".png": "image/png",
          ".ico": "image/x-icon",
        }[ext] || "application/octet-stream";
        res.writeHead(200, { "Content-Type": contentType });
        return res.end(fs.readFileSync(filePath));
      }

      res.writeHead(200, { "Content-Type": "text/html" });
      return res.end(fs.readFileSync(DIST_INDEX, "utf8"));
    }

    const htmlPath = path.join(__dirname, "test-gui.html");
    if (fs.existsSync(htmlPath)) {
      res.writeHead(200, { "Content-Type": "text/html" });
      return res.end(fs.readFileSync(htmlPath, "utf8"));
    }
  }

  json(res, 404, { error: `not found: ${method} ${p}` });
});

resetState();
const PORT = parseInt(process.env.PORT || "4173", 10);
server.listen(PORT, () => console.log(`Mock vmSmith server on http://localhost:${PORT}`));
