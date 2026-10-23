// SPDX-License-Identifier: LGPL-3.0-or-later

// The acts of the overview video, in order (see video/overview/README.md).
// Each act is a chapter. Terminal acts play the shots of record.py; their
// captions change at given input lines. The web UI act plays the beats of
// record_ui.mjs.

(function () {
  "use strict";
  const { el, css, P, eo, eio, eback, lerp, clamp, span, show, REC } = V;
  const files = REC.files || {};
  const SIDE = { x: 100, y: 214, w: 510 };

  // The background grid drifts slowly.
  const grid = document.getElementById("grid");
  V.draw(t => { grid.style.transform = `translate(${-(t * 6) % 40}px,${-(t * 3.5) % 40}px)`; });

  const term = V.terminal({ x: 654, y: 214, title: "~/sind-demo — bash" });
  const shot = (id, o) => { const s = V.shot(term, id, o); term.showFor(s.start - 0.2, s.end + 0.6); return s; };
  const card = o => V.card({ x: SIDE.x, y: SIDE.y, w: SIDE.w, ...o });
  const pre = s => `<pre>${s}</pre>`;

  // ------------------------------------------------------------ 1 · Open
  V.chapter("Open", 0);
  {
    const t0 = V.t, end = t0 + 15;
    const L = V.logo({ k: 2.0, cx: 960, cy: 450 });
    // the tiles become one cluster: controller, db, submitter, four workers
    const ROW = [[4, "#0D9488", 1, "controller", "slurmctld"], [5, "#14B8A6", 0.82, "db", "slurmdbd"], [3, "#14B8A6", 0.82, "submitter", "sbatch · srun"],
      [0, "#0EA5E9", 0.92, "worker-0", "slurmd"], [1, "#0EA5E9", 0.92, "worker-1", "slurmd"], [2, "#0EA5E9", 0.92, "worker-2", "slurmd"], [6, "#0EA5E9", 0.92, "worker-3", "slurmd"]];
    const rs = 124, rg = 30, rw = ROW.length * rs + (ROW.length - 1) * rg, rx0 = (1920 - rw) / 2, ry = 230;
    const target = {};
    ROW.forEach(([idx, col, op], j) => { target[idx] = { x: rx0 + j * (rs + rg), y: ry, col, op, j }; });
    const labels = ROW.map(([, , , name, svc], j) => {
      const l = el("div", "lbl"); css(l, { left: (rx0 + j * (rs + rg) - 30) + "px", top: (ry + rs + 18) + "px", width: (rs + 60) + "px" }); l.textContent = name;
      const c = el("div", "lbl", null, `<span class="chip">${svc}</span>`); css(c, { left: (rx0 + j * (rs + rg) - 50) + "px", top: (ry + rs + 54) + "px", width: (rs + 100) + "px" });
      return [l, c];
    });
    const lines = [
      [t0 + 6.6, "Real Slurm clusters,", 560],
      [t0 + 7.6, "in Docker containers on your machine.", 636],
      [t0 + 10.4, 'For development, tests and CI. From the <span style="color:var(--sky)">terminal</span> or the <span style="color:var(--teal)">browser</span>.', 760],
    ].map(([at, html, y]) => { const b = el("div", "big", null, html); css(b, { top: y + "px" }); if (y > 700) b.style.fontSize = "40px"; return [at, b]; });
    const promise = t0 + 5.6, out = end - 0.8;
    L.tiles.forEach((o, i) => V.sound(t0 + 0.25 + i * 0.13 + 0.32, "tick"));
    V.sound(t0 + 1.75, "whoosh"); V.sound(promise, "whoosh");
    V.say(t0 + 1.9, "open-1"); V.say(t0 + 6.5, "open-2"); V.say(t0 + 10.5, "open-3");
    V.draw(t => {
      const on = t < end + 0.1;
      L.g.style.display = on ? "" : "none";
      labels.forEach(([l, c]) => { l.style.display = c.style.display = on ? "" : "none"; });
      lines.forEach(([, b]) => { b.style.display = on ? "" : "none"; });
      if (!on) return;
      const gone = eio(P(t, out, end));
      const m = eio(P(t, promise - 0.4, promise + 1.0));
      L.tiles.forEach((o, i) => {
        const a = t0 + 0.25 + i * 0.13, d = eback(P(t, a, a + 0.5));
        let x = o.x, y = lerp(o.y - 260, o.y, d), s = L.size, op = o.op * P(t, a, a + 0.15), col = o.col;
        const tg = target[i];
        if (tg) {
          const mm = eio(clamp(m * 1.15 - tg.j * 0.03));
          x = lerp(x, tg.x, mm); y = lerp(y, tg.y, mm); s = lerp(L.size, rs, mm); op = lerp(op, tg.op, mm);
        } else op *= 1 - eo(P(t, promise - 0.5, promise + 0.2));
        css(o.d, { left: x + "px", top: (y - gone * 100) + "px", width: s + "px", height: s + "px", background: col, opacity: op * (1 - gone) });
      });
      const wv = eo(P(t, t0 + 1.7, t0 + 2.4)) * (1 - eio(P(t, promise - 0.7, promise - 0.1)));
      css(L.word, { left: (L.wx + (1 - eo(P(t, t0 + 1.7, t0 + 2.4))) * 60) + "px", top: (L.y0 + 22 * L.k) + "px", opacity: wv });
      const tv = eo(P(t, t0 + 2.5, t0 + 3.3)) * (1 - eio(P(t, promise - 0.7, promise - 0.1)));
      css(L.tag, { left: (L.wx + 6 * L.k) + "px", top: (L.y0 + 150 * L.k) + "px", opacity: tv, letterSpacing: lerp(10, 4.2, eo(P(t, t0 + 2.5, t0 + 3.6))) * L.k / 2 + "px" });
      labels.forEach(([l, c], j) => {
        const v = eo(P(t, promise + 0.9 + j * 0.08, promise + 1.4 + j * 0.08)) * (1 - gone);
        css(l, { opacity: v, transform: `translateY(${(1 - v) * 10 - gone * 100}px)` });
        css(c, { opacity: v, transform: `translateY(${(1 - v) * 10 - gone * 100}px)` });
      });
      lines.forEach(([at, b]) => { const v = eo(P(t, at, at + 0.6)) * (1 - gone); css(b, { opacity: v, transform: `translateY(${(1 - v) * 24 - gone * 60}px)` }); });
    });
    V.poster = t0 + 12.5;
    V.t = end;
  }

  // ------------------------------------------------------------ 2 · Start
  V.chapter("Start", 1);
  {
    const a = V.t;
    shot("install", { caps: { 0: ["install", "Install with mise. Docker is the only dependency."] } });
    card({ title: "mise", at: a + 0.4, until: V.t, html: "mise installs the sind binary of the latest release from GitHub, and keeps it on your <code>PATH</code>." });
    shot("doctor", { caps: { 0: ["doctor", "<code>sind doctor</code> checks the host first."] }, ticks: true });
    const c = V.t;
    shot("create", {
      caps: { 0: ["create", "Describe a cluster in YAML. Create it with one command."], 1: ["get", "See your clusters and their nodes."] },
      maxReal: 7,
    });
    card({ title: "dev.yaml", at: c - 0.2, until: V.t, html: pre(V.yaml(files["dev.yaml"] || "")) });
  }

  // ------------------------------------------------------------ 3 · Use Slurm
  V.chapter("Use Slurm", 2);
  {
    const a = V.t;
    let at = {};
    shot("slurm", {
      caps: {
        0: ["enter", "<code>sind enter</code> opens a shell on the submitter."],
        2: ["data", "The host's working directory is <code>/data</code> on every node."],
        3: ["submit", "Submit jobs. Watch the queue."],
        5: ["accounting", "The db node keeps the job accounting."],
        7: ["exec", "Or run one command from the host."],
      },
      onInput: (i, inp) => { at[i] = inp.start; },
    });
    card({ title: "job.sh", at: at[2] - 0.2, until: at[5] - 0.2, html: pre(V.yaml(files["job.sh"] || "")) });
    card({ title: "the cluster", at: a + 0.4, until: at[2] - 0.2, html: pre(V.yaml((files["dev.yaml"] || "").split("\n").slice(0, 4).join("\n"))) });
  }

  // ------------------------------------------------------------ 4 · Reach nodes
  V.chapter("Reach nodes", 2);
  {
    let at = {};
    const s = shot("ssh", {
      caps: { 0: ["ssh", "<code>sind ssh</code> reaches any node."], 2: ["relay", "sind publishes no ports. A relay on the mesh carries SSH."] },
      onInput: (i, inp) => { at[i] = inp.start; },
    });
    // the relay: this host, the realm's SSH relay, the node
    const d = V.diagram({ at: at[2] - 0.4, until: s.end + 0.3 });
    const x = SIDE.x + 40, w = SIDE.w - 80, t = at[2];
    const host = d.box(x, 300, w, 90, "this host", "ssh, scp, your tools", t);
    d.area(x - 20, 470, w + 40, 330, "REALM MESH", t + 0.4, "#14b8a6");
    const relay = d.box(x, 500, w, 90, "sind-ssh", "SSH relay, the only way in", t + 0.6);
    const node = d.box(x, 680, w, 90, "worker-2.dev", "a node on its cluster network", t + 1.0, { stroke: "#0ea5e9" });
    const e1 = d.edge([host.cx, host.y + host.h], [relay.cx, relay.y], t + 0.6, "#2dd4bf", 0.5, { straight: true });
    const e2 = d.edge([relay.cx, relay.y + relay.h], [node.cx, node.y], t + 1.1, "#38bdf8", 0.5, { straight: true });
    d.pulse(e1, t + 1.8, 0.8); d.pulse(e2, t + 2.5, 0.8);

    const b = V.t;
    shot("sshconfig", { caps: { 0: ["sshconfig", "Or use your own ssh: include the config that sind writes."], 2: ["scp", "Then ssh and scp reach each node by name."] } });
    card({ title: "~/.ssh/config", at: b, until: V.t, html: pre('<span class="k">Include</span> <span class="v">~/.local/state/sind/*/ssh_config</span>') + '<p style="margin:14px 0 0">One line, for every cluster of every realm.</p>' });
    const c = V.t;
    shot("forward", { caps: { 0: ["forward", "Port forwards reach the services on the nodes."] } });
    card({ title: "port forward", at: c, until: V.t, html: pre('<span class="v">localhost:8080</span>  →  <span class="k">api.dev:6820</span>') + '<p style="margin:14px 0 0">slurmrestd on the api node, on your machine.</p>' });
    const e = V.t;
    shot("dns", { caps: { 0: ["dns", "With systemd-resolved, the host resolves each node by name."] } });
    card({ title: "host DNS", at: e, until: V.t, html: pre('<span class="k">*.sind.sind</span>  →  <span class="v">sind-dns</span>') + '<p style="margin:14px 0 0">systemd-resolved asks the realm\'s DNS. <code>sind doctor</code> prints the polkit rule that allows it.</p>' });
  }

  // ------------------------------------------------------------ 5 · Operate
  V.chapter("Operate", 3);
  {
    shot("logs", { caps: { 0: ["logs", "Logs of a container, or of one service, followed live."] } });
    const w = V.t;
    shot("workers", { caps: { 0: ["workers", "Add workers to a running cluster."] }, maxReal: 7 });
    card({ title: "sind create worker", at: w, until: V.t, html: "New workers join the cluster as it runs. Slurm takes them into its partition." });
    let at = {};
    const p = shot("power", {
      caps: { 0: ["failures", "Simulate failures: freeze, cut or reboot nodes."], 1: ["frozen", "Slurm marks the frozen node as not responding."], 3: ["nodesets", "Node expressions select many nodes at once."] },
      onInput: (i, inp) => { at[i] = inp.start; if (i === 1) V.sound(inp.out, "fail"); },
    });
    card({ title: "node expressions", at: at[3] - 0.2, until: p.end, html: pre('<span class="v">worker-[0-3].dev</span>   worker-0 … worker-3\n<span class="v">worker-[0-6/2]</span>     every second worker\n<span class="v">worker-[0-3]!worker-2</span>') });
    shot("delworkers", { caps: { 0: ["delworkers", "Remove workers when you are done."] } });
  }

  // ------------------------------------------------------------ 6 · Slurm services
  V.chapter("Slurm services", 2);
  {
    const r = V.t;
    shot("rest", { caps: { 0: ["rest", "A REST API with JWT: slurmrestd on the api node."] } });
    card({ title: "dev.yaml", at: r, until: V.t, html: pre(V.yaml("nodes:\n  - api   # slurmrestd")) });
    const u = V.t;
    shot("users", { caps: { 0: ["users", "Users, groups and Slurm accounts, from the config."] } });
    const src = files["dev.yaml"] || "";
    card({ title: "dev.yaml", at: u, until: V.t, html: pre(V.yaml(src.slice(src.indexOf("groups:")).trim())) });
    const f = V.t;
    shot("failover", {
      caps: { 0: ["backup", "A backup controller takes over…"], 1: ["failover", "… when the primary controller fails."] },
      // the primary is stopped, and the backup (backup*) runs slurmctld
      marks: { 2: [/^controller\.ha /, /^controller-backup\.ha /] }, holds: { 2: 3.6 },
      onInput: (i, inp) => { if (i === 1) V.sound(inp.out, "fail"); if (i === 2) V.sound(inp.out, "done"); },
    });
    card({ title: "ha.yaml", at: f, until: V.t, html: pre(V.yaml(files["ha.yaml"] || "")) });
    const m = V.t;
    shot("monitoring", { caps: { 0: ["monitoring", "Monitoring: Prometheus, Loki and Grafana for each realm."] } });
    card({ title: "dev.yaml", at: m, until: V.t, html: pre(V.yaml("monitoring: true")) });
    // Grafana, from the browser recording
    const g = V.browser({ scale: 0.86, y: 214, url: "grafana.sind.sind:3000" });
    const gp = g.play("grafana", "end", { beats: { grafana: { cap: ["grafana", "The Slurm dashboards of the cluster, in Grafana."] } }, cursor: false, lead: 0.3 });
    if (!gp) V.hold(0.5);
    // images
    const rel = (REC.slurm || ["26.05", "25.11"]);
    V.cap(V.t, ["images", `Slurm ${rel.join(" and ")}, on amd64 and arm64. CVMFS on request.`]);
    const i0 = V.t;
    const chips = rel.map(r => `<span class="chip">sind-node:${r}</span>`).join("") + '<span class="chip">linux/amd64</span><span class="chip">linux/arm64</span>';
    V.hold(7);
    V.card({ x: 560, y: 380, w: 800, title: "images", at: i0, until: V.t, dy: 20, dx: 0, html: chips + pre(V.yaml("defaults:\n  image: ghcr.io/gsi-hpc/sind-node:" + rel[rel.length - 1] + "\nstorage:\n  cvmfs: true   # /cvmfs on every node")) });
  }

  // ------------------------------------------------------------ 7 · Realms
  V.chapter("Realms", 1);
  {
    let at = {};
    const s = shot("realm", { caps: { 0: ["realm", "A realm keeps its clusters apart, for parallel work and CI."] }, maxReal: 7, onInput: (i, inp) => { at[i] = inp.start; } });
    const d = V.diagram({ at: s.start, until: s.end + 0.3 });
    const t = s.start + 0.3, x = SIDE.x + 10, w = SIDE.w - 20;
    [["realm sind", 280, ["dev", "ha"], "#14b8a6"], ["realm ci", 590, ["default"], "#38bdf8"]].forEach(([name, y, cl, col], k) => {
      const tt = k ? (at[0] ?? t) + 1 : t;
      d.area(x, y, w, 250, name.toUpperCase(), tt, col);
      d.box(x + 20, y + 24, 230, 84, "mesh", "DNS · SSH relay", tt + 0.2);
      cl.forEach((c, j) => d.box(x + 280, y + 24 + j * 104, w - 300, 84, c, "cluster", tt + 0.4 + j * 0.15, { stroke: col }));
    });
    const c = V.t;
    shot("connect", { caps: { 0: ["connect", "Connect other containers, such as kind nodes, to the realm's networks."] } });
    card({ title: "sind connect container", at: c, until: V.t, html: "The container joins the mesh and every cluster network of the realm, and reaches each node by its address. The kind guide forwards the realm's DNS zone too." });
  }

  // ------------------------------------------------------------ 8 · Web UI
  V.chapter("Web UI", 3);
  {
    shot("ui", { caps: { 0: ["ui", "<code>sind ui</code> opens in your browser. Only you can open it."] }, holds: { 0: 2.2 }, linger: 0.2 });
    const b = V.browser({ scale: 0.9, y: 196, url: "127.0.0.1 · sind ui" });
    const follow = (zoom, extra = {}) => ({ zoom, follow: true, ...extra });
    b.play("overview", "grafana", {
      lead: 0.1,
      beats: {
        overview: { cap: ["overview", "Overview: all realms, clusters and nodes. Live."] },
        dialog: { cap: ["dialog", "New cluster: a form, a plan and the exact command."], say: "name" },
        api: follow(1.3, { say: "api" }), workers: follow(1.3, { say: "workers3" }),
        slurmconf: follow(1.25, { say: "slurmconf" }), yaml: follow(1.25, { say: "yaml" }), create: { say: "createbtn" },
        run: { cap: ["run", "Each step and each node, as it happens."], len: 9 },
        failed: { cap: ["failed", "When a run fails, sind ui says why and what to do."], zoom: 1.1 },
        cluster: { cap: ["health", "The health of each service on each node."] },
        nodes: { cap: ["nodeset", "Select nodes as a node set, then power them."], ...follow(1.25) },
        power: { zoom: 1.12 },
        logs: { cap: ["uilogs", "Logs: a container or one service, followed live."], ...follow(1.15) },
        connect: { cap: ["uiconnect", "Connect: the ssh and exec commands, ready to copy."], ...follow(1.3) },
        mesh: { cap: ["pages", "Mesh and DNS, topology, monitoring and doctor."] },
        jump: { cap: ["jump", "Press / to jump to any node or page. Press ? for help."], ...follow(1.3) },
        help: { zoom: 1 },
        theme: { cap: ["theme", "Light or dark."] },
      },
      sounds: [["rundone", "done", 0.2], ["failed", "fail", 1.6]],
    });
  }

  // ------------------------------------------------------------ 9 · Automation
  V.chapter("Automation", 2);
  {
    const a = V.t;
    V.cap(a, ["action", "In CI: one step with GSI-HPC/sind-action."]);
    V.hold(7);
    V.card({ x: 460, y: 300, w: 1000, title: ".github/workflows/ci.yml", at: a, until: V.t + 0.5, dx: 0, dy: 24,
      html: pre(V.yaml("- uses: GSI-HPC/sind-action@v2\n  with:\n    clusters: |\n      - test/cluster.yml\n- run: sind exec -- srun hostname")) +
        '<p style="margin:18px 0 0">The action installs sind, checks the runner, creates the clusters and waits for Slurm. Its cleanup step deletes them.</p>' });
    V.sound(a + 0.3, "whoosh");
    shot("plain", { caps: { 0: ["plain", "Plain progress lines and a JSON event log, for CI and scripts."] } });
  }

  // ------------------------------------------------------------ 10 · Close
  V.chapter("Close", 0);
  {
    shot("delete", { caps: { 0: ["delete", "Delete everything with one command."] }, maxReal: 5 });
    V.cap(V.t, null);
    const t0 = V.t + 0.3;
    V.say(t0 + 2.6, "bye");
    const end = Math.max(t0 + 8, V.voiceFree + 2.2);
    const L = V.logo({ k: 1.7, cx: 960, cy: 420 });
    const links = el("div", "links", null, '<span style="color:var(--dim)">docs</span>&nbsp; gsi-hpc.github.io/sind<br><span style="color:var(--dim)">code</span>&nbsp; github.com/GSI-HPC/sind');
    css(links, { top: "650px" });
    const black = el("div", "abs"); css(black, { inset: "0", background: "#000", opacity: 0, zIndex: 5 });
    L.tiles.forEach((o, i) => V.sound(t0 + 0.3 + i * 0.07 + 0.25, "tick"));
    V.draw(t => {
      const on = t > t0 - 0.05;
      L.g.style.display = links.style.display = on ? "" : "none";
      if (on) {
        L.tiles.forEach((o, i) => {
          const a = t0 + 0.3 + i * 0.07, d = eback(P(t, a, a + 0.45));
          css(o.d, { left: o.x + "px", top: o.y + "px", opacity: o.op * P(t, a, a + 0.12), transform: `scale(${lerp(0.3, 1, d)})` });
        });
        const wv = eo(P(t, t0 + 1.1, t0 + 1.8));
        css(L.word, { left: (L.wx + (1 - wv) * 50) + "px", top: (L.y0 + 22 * L.k) + "px", opacity: wv });
        css(L.tag, { left: (L.wx + 6 * L.k) + "px", top: (L.y0 + 150 * L.k) + "px", opacity: eo(P(t, t0 + 1.7, t0 + 2.4)), letterSpacing: (4.2 * L.k / 2) + "px" });
        const lv = eo(P(t, t0 + 2.5, t0 + 3.2));
        css(links, { opacity: lv, transform: `translateY(${(1 - lv) * 16}px)` });
      }
      black.style.opacity = eio(P(t, end - 1.4, end - 0.1));
    });
    V.t = end;
    V.chapter("end", 0); // closes the last chapter for the chapter bar
  }

  V.finish();
})();
