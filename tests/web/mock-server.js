const http = require("http");
const fs = require("fs");
const path = require("path");

const DIST_DIR = path.resolve(__dirname, "../../internal/web/dist");
const DIST_INDEX = path.join(DIST_DIR, "index.html");

let vmCounter = 0;
const vms = new Map();
const snapshots = new Map();
const images = new Map();
const templates = new Map();
const portForwards = new Map();

function seed() {
  const vm1 = createVM({ name: "web-server", image: "ubuntu-22.04", cpus: 2, ram_mb: 4096, disk_gb: 40 });
  vm1.ip = "192.168.100.10";
  const vm2 = createVM({ name: "db-server", image: "rocky-9", cpus: 4, ram_mb: 8192, disk_gb: 100 });
  vm2.state = "stopped";
  vm2.ip = "192.168.100.11";
  snapshots.set(vm1.id, [
    { id: `${vm1.id}/before-deploy`, vm_id: vm1.id, name: "before-deploy", created_at: new Date().toISOString() },
  ]);
  images.set("img-1", {
    id: "img-1", name: "ubuntu-base", path: "/images/ubuntu-base.qcow2",
    size_bytes: 1073741824, format: "qcow2", source_vm: vm1.id, created_at: new Date().toISOString(),
  });
  templates.set("tmpl-1", {
    id: "tmpl-1",
    name: "small-ubuntu",
    image: "/images/ubuntu-base.qcow2",
    cpus: 1,
    ram_mb: 1024,
    disk_gb: 12,
    description: "Small Ubuntu template",
    tags: ["starter", "ubuntu"],
    default_user: "ubuntu",
    networks: [],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  });
}

function resetState() {
  vmCounter = 0;
  vms.clear();
  snapshots.clear();
  images.clear();
  templates.clear();
  portForwards.clear();
  seed();
}

function createVM(spec) {
  vmCounter++;
  const id = `vm-${vmCounter}`;
  const vm = {
    id, name: spec.name,
    spec: { name: spec.name, image: spec.image || "ubuntu", cpus: spec.cpus || 2, ram_mb: spec.ram_mb || 2048, disk_gb: spec.disk_gb || 20, ssh_pub_key: spec.ssh_pub_key || "", default_user: spec.default_user || "", networks: spec.networks || [] },
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

  // API routes
  if (p === "/api/v1/vms" && method === "GET") {
    const list = [...vms.values()];
    return json(res, 200, list, { "X-Total-Count": String(list.length) });
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
    vm.updated_at = new Date().toISOString();
    return json(res, 200, vm);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)$/)) && method === "DELETE") {
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
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots$/)) && method === "GET") {
    return json(res, 200, snapshots.get(m[1]) || []);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots$/)) && method === "POST") {
    const body = await parseBody(req);
    const vmId = m[1];
    const snap = { id: `${vmId}/${body.name}`, vm_id: vmId, name: body.name, created_at: new Date().toISOString() };
    const list = snapshots.get(vmId) || [];
    list.push(snap); snapshots.set(vmId, list);
    return json(res, 201, snap);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots\/([^/]+)\/restore$/)) && method === "POST") {
    return json(res, 200, { status: "restored" });
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/snapshots\/([^/]+)$/)) && method === "DELETE") {
    const list = (snapshots.get(m[1]) || []).filter(s => s.name !== m[2]);
    snapshots.set(m[1], list); res.writeHead(204); return res.end();
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/ports$/)) && method === "GET") {
    return json(res, 200, portForwards.get(m[1]) || []);
  }
  if ((m = p.match(/^\/api\/v1\/vms\/([^/]+)\/ports$/)) && method === "POST") {
    const body = await parseBody(req);
    const vm = vms.get(m[1]);
    const pf = { id: `pf-${Date.now()}`, vm_id: m[1], host_port: body.host_port, guest_port: body.guest_port, guest_ip: vm?.ip || "192.168.100.10", protocol: body.protocol || "tcp" };
    const list = portForwards.get(m[1]) || []; list.push(pf); portForwards.set(m[1], list);
    return json(res, 201, pf);
  }
  if (p === "/api/v1/images" && method === "GET") {
    const list = [...images.values()];
    return json(res, 200, list, { "X-Total-Count": String(list.length) });
  }
  if (p === "/api/v1/templates" && method === "GET") {
    const list = [...templates.values()];
    return json(res, 200, list, { "X-Total-Count": String(list.length) });
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
    const entries = [
      { ts: new Date().toISOString(), level: "info", source: "daemon", msg: "vmSmith daemon listening", fields: { addr: "0.0.0.0:8080" } },
      { ts: new Date().toISOString(), level: "info", source: "api", msg: "GET /api/v1/vms", fields: { status_code: "200", duration_ms: "1" } },
      { ts: new Date().toISOString(), level: "info", source: "cli", msg: "vm list", fields: {} },
      { ts: new Date().toISOString(), level: "warn", source: "daemon", msg: "port forward restore skipped", fields: { error: "iptables not available" } },
      { ts: new Date().toISOString(), level: "error", source: "api", msg: "POST /api/v1/vms", fields: { status_code: "500", duration_ms: "5" } },
    ];
    const level = url.searchParams.get("level") || "debug";
    const limit = parseInt(url.searchParams.get("limit") || "200", 10);
    const source = url.searchParams.get("source") || "";
    const levelOrder = { debug: 0, info: 1, warn: 2, error: 3 };
    const minLevel = levelOrder[level] ?? 0;
    let filtered = entries.filter(e => (levelOrder[e.level] ?? 0) >= minLevel);
    if (source) filtered = filtered.filter(e => e.source === source);
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
      { id: "evt-3", type: "vm_started", source: "libvirt", severity: "info", vm_id: "vm-1", message: "VM 'web-server-prod' started", created_at: new Date(Date.now() - 30_000).toISOString() },
      { id: "evt-2", type: "vm_created", source: "app",     severity: "info", vm_id: "vm-1", message: "VM 'web-server-prod' created", created_at: new Date(Date.now() - 60_000).toISOString() },
      { id: "evt-1", type: "vm_stopped", source: "libvirt", severity: "warn", vm_id: "vm-2", message: "VM 'database-staging' stopped unexpectedly", created_at: new Date(Date.now() - 120_000).toISOString() },
    ];
    const vmFilter = (url.searchParams.get("vm_id") || "").trim();
    const sourceFilter = (url.searchParams.get("source") || "").trim();
    const severityFilter = (url.searchParams.get("severity") || "").trim();
    const typeFilter = (url.searchParams.get("type") || "").trim();
    let filtered = allEvents.filter(e =>
      (!vmFilter || e.vm_id === vmFilter) &&
      (!sourceFilter || e.source === sourceFilter) &&
      (!severityFilter || e.severity === severityFilter) &&
      (!typeFilter || e.type === typeFilter)
    );
    const total = filtered.length;
    return json(res, 200, filtered, { "X-Total-Count": String(total) });
  }
  if (p === "/api/v1/host/interfaces" && method === "GET") {
    return json(res, 200, [
      { name: "eth0", ips: ["10.21.100.101/24"], mac: "52:54:00:00:00:01", is_up: true, is_physical: true },
      { name: "eth1", ips: ["192.168.1.16/24"], mac: "52:54:00:00:00:02", is_up: true, is_physical: true },
    ]);
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
