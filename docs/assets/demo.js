/* manygit — browser demo.
 *
 * A port of the real TUI's interaction model. The keymap follows
 * internal/tui/update.go's handleKey; the rendering follows internal/tui/view.go
 * (syncGlyph, renderRow, tabBar, centerBlock, window). The git is fake; the keys
 * are not.
 */
(function () {
  "use strict";

  /* ------------------------------------------------------------- constants */

  // themeList, verbatim from internal/tui/theme.go
  var THEMES = ["default", "serika_dark", "dracula", "nord", "catppuccin", "8008"];
  // harness.All, from internal/harness/harness.go
  var HARNESSES = [
    { name: "claude", installed: true },
    { name: "codex", installed: false }
  ];
  // newsDayOptions, from internal/tui/settings.go
  var NEWS_DAYS = [1, 3, 7, 14];
  // maxDepthOptions, from internal/tui/settings.go. config.Default() ships 3.
  var MAX_DEPTHS = [1, 2, 3, 4, 5];

  var SK_THEME = 0, SK_HARNESS = 1, SK_NEWSDAYS = 2, SK_MAXDEPTH = 3, SK_GLYPH = 4, SK_EDITOR = 5;

  var STORE = "manygit.theme";
  var ROOT = "~/code";

  /* ------------------------------------------------------------------ data */

  function repo(g, n, b, o) {
    o = o || {};
    return {
      g: g, n: n, b: b,
      dirty: o.dirty || 0,
      ahead: o.ahead || 0,
      behind: o.behind || 0,
      remote: o.remote !== false,
      up: o.up !== false,
      tag: o.tag || "",
      // New() leaves every repo unloaded; Init() is what loads and fetches them.
      // boot() replays that, so these are the pre-Init values.
      loaded: false,
      fetching: false
    };
  }

  // Sorted by group then name — matching discover.Discover's sort order.
  // "(root)" is discover's group for a repo sitting directly in the root, and it
  // sorts before the named folders. Depths vary so the `?` scan-depth setting has
  // something to actually do: 1 finds only dotfiles, 3 reaches infra/edge.
  var REPOS = [
    repo("(root)", "dotfiles", "main", { tag: "v1.0.0" }),
    repo("apps", "api-gateway", "main", { dirty: 3, ahead: 2, tag: "v2.3.1" }),
    repo("apps", "billing-worker", "main", { tag: "v1.9.0" }),
    repo("apps", "mobile-client", "main", { behind: 4, tag: "v5.2.0" }),
    repo("apps", "web-dashboard", "feat/usage-chart", { dirty: 7, tag: "v4.1.2" }),
    repo("infra", "ci-actions", "main", { up: false }),
    repo("infra", "k8s-manifests", "staging", { behind: 2, tag: "2024.11" }),
    repo("infra", "runbooks", "main", { remote: false, up: false }),
    repo("infra", "terraform-live", "main", {}),
    repo("infra/edge", "edge-proxy", "main", { dirty: 2, tag: "v0.4.1" }),
    repo("packages", "design-system", "main", { tag: "v12.0.1" }),
    repo("packages", "eslint-config", "main", {}),
    repo("packages", "sdk-js", "release/2.4", { ahead: 1, behind: 3, tag: "v2.4.0-rc1" }),
    repo("packages", "telemetry", "main", { dirty: 1, tag: "v0.8.4" })
  ];
  REPOS.forEach(function (r) {
    // discover.Repo.Group is the parent dir relative to the root, or "(root)".
    // Depth follows from it: a repo in the root is 1, one in "apps" is 2, one in
    // "infra/edge" is 3.
    r.depth = r.g === "(root)" ? 1 : r.g.split("/").length + 1;
    r.path = r.g === "(root)" ? ROOT + "/" + r.n : ROOT + "/" + r.g + "/" + r.n;
  });

  // discovered() is what MaxDepth gates: the scan depth decides which repos
  // *exist*, exactly as discover.Discover's MaxDepth does. It is not a view
  // filter — `/` and `F` narrow what this returns, never the other way round.
  function discovered() {
    return REPOS.filter(function (r) { return r.depth <= S.maxDepth; });
  }

  var SCRIPTS = [
    { name: "bootstrap.sh" },
    { name: "scripts/check-versions.sh" },
    { name: "scripts/sync-all.sh" },
    { name: "test.sh" }
  ];

  var BRANCHES = {
    "api-gateway": ["feat/rate-limit", "fix/timeout-retry", "chore/go-1.24"],
    "web-dashboard": ["feat/usage-chart", "main", "fix/legend-overflow"],
    "sdk-js": ["release/2.4", "main", "feat/streaming"],
    "k8s-manifests": ["staging", "main", "prod"]
  };

  function uniq(a) {
    return a.filter(function (v, i) { return a.indexOf(v) === i; });
  }

  // `git branch --all` never lists the same ref twice, so neither may this. The
  // seed lists below can name the current branch themselves — checking out
  // feat/wip on a repo whose fallback is [r.b, "feat/wip"] used to yield
  // ["feat/wip", "feat/wip"] and render two rows both marked (current).
  function branchesFor(r) {
    var locals = uniq(BRANCHES[r.n] ? BRANCHES[r.n].slice() : [r.b, "feat/wip"]);
    if (locals.indexOf(r.b) < 0) locals.unshift(r.b);
    var out = locals.map(function (n) {
      return { name: n, remote: false, current: n === r.b };
    });
    if (!r.remote) return out;
    var rem = uniq(locals.concat(["release/1.x", "dependabot/npm_and_yarn/lodash-4.17.21", "revert-118-hotfix"]));
    rem.forEach(function (n) {
      out.push({ name: "origin/" + n, remote: true, current: false });
    });
    return out;
  }

  // Colored `git log --graph --oneline --decorate` output. gfx = the graph
  // spine; commits carry a hash so the cursor can snap to them (graphSel).
  function graphFor(r) {
    var head = r.b, tag = r.tag || "v1.0.0", feat = (BRANCHES[r.n] || [])[1] || "feat/wip";
    return [
      { gfx: "* ", hash: "a3f21b8", refs: [["cy", "HEAD -> " + head], ["gr", "origin/" + head]], subj: "Add a retry budget to upstream calls" },
      { gfx: "* ", hash: "9c7e410", refs: [], subj: "Bump express to 4.19.2" },
      { gfx: "|\\  " },
      { gfx: "| * ", hash: "4d1a992", refs: [["rd", "origin/" + feat]], subj: "Sketch the token-bucket limiter" },
      { gfx: "| * ", hash: "b0e8d55", refs: [], subj: "Move the router table off the hot path" },
      { gfx: "|/  " },
      { gfx: "* ", hash: "77b0c31", refs: [["yl", "tag: " + tag]], subj: "Release " + tag },
      { gfx: "* ", hash: "1e9a034", refs: [], subj: "Drop the vendored logger" },
      { gfx: "* ", hash: "c52f7d1", refs: [], subj: "Split config loading out of main" },
      { gfx: "* ", hash: "8b40a6e", refs: [], subj: "Initial import" }
    ];
  }

  var WIP_FILES = {
    "api-gateway": [
      { s: "M", p: "internal/proxy/router.go" },
      { s: "M", p: "internal/proxy/router_test.go" },
      { s: "??", p: ".env.local" }
    ],
    "web-dashboard": [
      { s: "M", p: "src/panels/UsageChart.tsx" },
      { s: "M", p: "src/panels/UsageChart.test.tsx" },
      { s: "M", p: "src/lib/format.ts" },
      { s: "A", p: "src/panels/Legend.tsx" },
      { s: "D", p: "src/panels/OldChart.tsx" },
      { s: "R", p: "src/hooks/useSeries.ts" },
      { s: "??", p: "src/panels/scratch.tsx" }
    ],
    "telemetry": [{ s: "M", p: "exporter/otlp.go" }]
  };

  // Files touched by a given commit (the graph → changes drill-down).
  var COMMIT_FILES = {
    a3f21b8: [{ s: "M", p: "internal/proxy/router.go" }, { s: "M", p: "internal/proxy/budget.go" }],
    "9c7e410": [{ s: "M", p: "package.json" }, { s: "M", p: "package-lock.json" }],
    "4d1a992": [{ s: "A", p: "internal/limit/bucket.go" }, { s: "A", p: "internal/limit/bucket_test.go" }],
    b0e8d55: [{ s: "M", p: "internal/proxy/table.go" }],
    "77b0c31": [{ s: "M", p: "CHANGELOG.md" }],
    "1e9a034": [{ s: "D", p: "vendor/logger/logger.go" }],
    c52f7d1: [{ s: "A", p: "internal/config/config.go" }, { s: "M", p: "main.go" }],
    "8b40a6e": [{ s: "A", p: "main.go" }]
  };

  var DIFF = [
    ["", "diff --git a/internal/proxy/router.go b/internal/proxy/router.go"],
    ["", "index 8a1f0c3..b7d4e91 100644"],
    ["", "--- a/internal/proxy/router.go"],
    ["", "+++ b/internal/proxy/router.go"],
    ["cy", "@@ -42,9 +42,17 @@ func (r *Router) Handle(w http.ResponseWriter, req *http.Request) {"],
    ["", " \troute, ok := r.match(req.URL.Path)"],
    ["", " \tif !ok {"],
    ["", " \t\thttp.NotFound(w, req)"],
    ["", " \t\treturn"],
    ["", " \t}"],
    ["rd", "-\tr.upstream.Do(route, w, req)"],
    ["gr", "+\tif !r.limiter.Allow(route.Key) {"],
    ["gr", '+\t\tw.Header().Set("Retry-After", "1")'],
    ["gr", '+\t\thttp.Error(w, "rate limited", http.StatusTooManyRequests)'],
    ["gr", "+\t\treturn"],
    ["gr", "+\t}"],
    ["gr", "+"],
    ["gr", "+\tif err := r.upstream.Do(route, w, req); err != nil {"],
    ["gr", '+\t\tr.log.Warn("upstream failed", "route", route.Key, "err", err)'],
    ["gr", "+\t}"],
    ["", " }"]
  ];

  // `my PRs` — all authored by the signed-in user, which is what gh search
  // --author=@me returns.
  var PR_MINE = [
    { num: 412, author: "rabeeh-ta", title: "Retry budget for upstream calls", repo: "api-gateway", draft: false },
    { num: 77, author: "rabeeh-ta", title: "Streaming responses in the JS SDK", repo: "sdk-js", draft: true },
    { num: 39, author: "rabeeh-ta", title: "Drop node 18 from the test matrix", repo: "ci-actions", draft: false }
  ];

  // `review requests`. Ordered so pressing enter down the list walks all three
  // real outcomes of checkoutPR(): a clean repo checks out, a dirty one is
  // skipped with a reason, and one whose repo isn't in the tree says so.
  var PR_REVIEW = [
    { num: 55, author: "Sinu00", title: "Design tokens: a dark-mode pass", repo: "design-system", draft: false },
    { num: 91, author: "zameel7", title: "Bump the cluster to Kubernetes 1.30", repo: "k8s-manifests", draft: false },
    { num: 128, author: "nihxdr", title: "Split the OTLP exporter out of core", repo: "telemetry", draft: false },
    { num: 143, author: "rafeehcp", title: "Drop the vendored logger", repo: "api-gateway", draft: true },
    { num: 12, author: "Sinu00", title: "Cache the route table between reloads", repo: "docs-site", draft: false }
  ];

  var NEWS = [
    "api-gateway landed a token-bucket rate limiter; the retry budget is next",
    "web-dashboard is mid-refactor on the usage chart — 7 files still dirty",
    "sdk-js has diverged from origin/release/2.4: 1 ahead, 3 behind",
    "k8s-manifests fell 2 behind staging after the 1.30 bump"
  ];

  // Per-script output, so running bootstrap.sh doesn't print sync-all.sh's log.
  var SCRIPT_OUT = {
    "scripts/sync-all.sh": [
      "==> apps/api-gateway",
      "    skipped — uncommitted changes",
      "==> apps/billing-worker",
      "    Already up to date.",
      "==> apps/mobile-client",
      "    Updating 7c31a09..e44b210",
      "    Fast-forward",
      "     src/screens/Home.tsx | 24 ++++++++++++------",
      "     1 file changed, 16 insertions(+), 8 deletions(-)",
      "==> apps/web-dashboard",
      "    skipped — uncommitted changes",
      "==> infra/ci-actions",
      "    Already up to date.",
      "==> infra/k8s-manifests",
      "    Updating 4a09c12..d81ff30",
      "    Fast-forward",
      "     base/deployment.yaml | 6 +++---",
      "     1 file changed, 3 insertions(+), 3 deletions(-)",
      "==> infra/runbooks",
      "    skipped — no remote",
      "==> infra/terraform-live",
      "    Already up to date.",
      "==> packages/design-system",
      "    Already up to date.",
      "==> packages/eslint-config",
      "    Already up to date.",
      "==> packages/sdk-js",
      "    Already up to date.",
      "==> packages/telemetry",
      "    skipped — uncommitted changes",
      "",
      "done. 12 repos · 2 updated · 3 skipped"
    ],
    "bootstrap.sh": [
      "==> checking toolchain",
      "    go1.24.4  node v20.11.1  gh 2.62.0",
      "==> apps/api-gateway",
      "    go mod download",
      "==> apps/web-dashboard",
      "    npm ci — 412 packages in 6.2s",
      "==> apps/mobile-client",
      "    npm ci — 733 packages in 11.8s",
      "==> packages/design-system",
      "    npm ci — 96 packages in 1.9s",
      "==> infra/runbooks",
      "    skipped — nothing to install",
      "",
      "done. 12 repos · 4 bootstrapped · 8 nothing to do"
    ],
    "scripts/check-versions.sh": [
      "REPO                  DECLARED   LOCK       DRIFT",
      "api-gateway           go1.24     go1.24     -",
      "billing-worker        go1.24     go1.24     -",
      "mobile-client         node20     node20     -",
      "web-dashboard         node20     node18     yes",
      "design-system         node20     node20     -",
      "eslint-config         node20     node20     -",
      "sdk-js                node20     node18     yes",
      "telemetry             go1.24     go1.23     yes",
      "",
      "done. 3 repos drifted from the declared toolchain"
    ],
    "test.sh": [
      "==> packages/design-system",
      "    PASS  src/tokens.test.ts (14 tests)",
      "    PASS  src/Button.test.tsx (9 tests)",
      "==> packages/sdk-js",
      "    PASS  test/client.test.ts (22 tests)",
      "==> packages/telemetry",
      "    ok    manygit/telemetry/exporter  0.412s",
      "==> apps/api-gateway",
      "    ok    api-gateway/internal/proxy  1.902s",
      "    ok    api-gateway/internal/limit  0.077s",
      "",
      "done. 4 repos · 47 tests · 0 failures"
    ]
  };
  var SCRIPT_FALLBACK = ["", "done."];

  /* ----------------------------------------------------------------- state */

  var S = {
    cursor: 0,
    focus: "repos", // repos | scripts | branches | bottom
    topView: "branches", // branches | prs
    bottomView: "graph", // graph | changes | output

    filter: "",
    filtering: false,
    filterPanel: "repos", // repos | scripts | branches | prs
    filterAttention: false,

    showHelp: false, showKeys: false, showGraph: false, showNews: false,
    showTags: false, zoomed: false,

    settingsCursor: 0,
    editingOpenCmd: false,
    openCmdBuf: "",

    branchCursor: 0,
    scriptCursor: 0,
    prCursor: 0,
    prShowReview: false,

    graphSel: 0,
    graphOffset: 0,
    changeCursor: 0,
    changeShowDiff: false,
    changeDiffOff: 0,

    outputLines: [],
    outputTitle: "",
    outputOffset: 0,
    outputRunning: false,
    outputRun: 0,

    statusLine: "",
    statusGen: 0,
    newsIndex: 0,
    newsOffset: 0,

    confirmDiscard: false,
    confirmFull: false,
    confirmName: "",

    // gh availability, resolved by ghProbeCmd after Init. Until it returns, the
    // PRs pane says "checking GitHub..." and the badge/indicator are absent.
    ghProbed: false,
    ghInstalled: false,
    ghAvailable: false,
    ghUser: "",
    prLoaded: false,

    // top-bar news. Empty until the harness summarises; newsLoading drives the
    // "summarizing commits..." note next to the repo count.
    newsFeed: [],
    newsLoading: false,

    booted: false,

    // config (the ? screen writes these; theme + glyphs persist)
    theme: "serika_dark",
    harness: "claude",
    newsDays: 3,
    maxDepth: 3,
    glyphs: "unicode",
    openCmd: "code"
  };

  var branches = [];
  var graph = [];
  var changeFiles = [];
  var changeDiff = [];

  /* ------------------------------------------------------------- utilities */

  function esc(s) {
    return String(s).replace(/[&<>"]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
    });
  }
  function sp(cls, s) { return '<span class="' + cls + '">' + esc(s) + "</span>"; }
  var d = function (s) { return sp("d", s); };
  var gr = function (s) { return sp("gr", s); };
  var yl = function (s) { return sp("yl", s); };
  var cy = function (s) { return sp("cy", s); };
  var mg = function (s) { return sp("mg", s); };
  var og = function (s) { return sp("og", s); };
  var rd = function (s) { return sp("rd", s); };
  var gp = function (s) { return sp("gp", s); };
  var cur = function (s) { return sp("cur", s); };

  // The @author in the PRs pane. These are real GitHub logins, so the handle
  // links to the profile — a real <a>, so middle-click, cmd-click and "copy link
  // address" all behave. It is styled to look exactly like the plain .gp text it
  // replaces; the cursor on hover is the only tell.
  function ghUser(login) {
    return '<a class="gp gh" href="https://github.com/' + encodeURIComponent(login) +
      '" target="_blank" rel="noopener noreferrer" title="@' + esc(login) + ' on GitHub">' +
      esc("@" + login) + "</a>";
  }

  // window(), ported verbatim from view.go
  function win(n, keep, h) {
    var start = 0;
    if (keep >= h) start = keep - h + 1;
    var end = start + h;
    if (end > n) end = n;
    if (start > n) start = n;
    return [start, end];
  }
  function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }

  /* --------------------------------------------------------- view-model fns */

  function needsAttention(r) { return r.dirty > 0 || r.ahead > 0 || r.behind > 0; }

  // currentBranch is the branch label a row shows: "detached" when the head is,
  // otherwise the branch name. `/` matches this string, so it has to be the same
  // one renderRow draws.
  function currentBranch(r) { return r.detached ? "detached" : r.b; }

  // repoHaystack is the text `/` matches a repo row against: what the row shows —
  // its name and current branch, plus the latest tag while `t` has tags inline.
  // It matches the full values rather than the width-truncated ones renderRow
  // draws, so results never depend on how wide the terminal happens to be.
  //
  // The group header and the dirty/sync cells are deliberately left out: `F`
  // already filters on attention state, and folding it into `/` would give "ok"
  // two meanings.
  function repoHaystack(r) {
    var s = r.n + " " + currentBranch(r);
    if (S.showTags) s += " " + (r.tag || "");
    return s.toLowerCase();
  }

  function visibleRepos() {
    var needle = S.filterPanel === "repos" ? S.filter.toLowerCase() : "";
    var all = discovered();
    if (!needle && !S.filterAttention) return all;
    return all.filter(function (r) {
      if (needle && repoHaystack(r).indexOf(needle) < 0) return false;
      if (S.filterAttention && !needsAttention(r)) return false;
      return true;
    });
  }
  function curRepo() {
    var v = visibleRepos();
    return S.cursor >= 0 && S.cursor < v.length ? v[S.cursor] : null;
  }
  function visibleScripts() {
    if (S.filterPanel !== "scripts" || !S.filter) return SCRIPTS;
    var n = S.filter.toLowerCase();
    return SCRIPTS.filter(function (s) { return s.name.toLowerCase().indexOf(n) >= 0; });
  }
  function visibleBranches() {
    if (S.filterPanel !== "branches" || !S.filter) return branches;
    var n = S.filter.toLowerCase();
    return branches.filter(function (b) { return b.name.toLowerCase().indexOf(n) >= 0; });
  }
  function activePRs() { return S.prShowReview ? PR_REVIEW : PR_MINE; }
  function visiblePRs() {
    var prs = activePRs();
    if (S.filterPanel !== "prs" || !S.filter) return prs;
    var n = S.filter.toLowerCase();
    return prs.filter(function (p) {
      return (p.repo + " " + p.title + " " + p.author).toLowerCase().indexOf(n) >= 0;
    });
  }
  // graphSel 0 == WIP; 1..N == commits
  function graphCommits() { return graph.filter(function (g) { return g.hash; }); }
  function selectedRef() {
    var c = graphCommits();
    return S.graphSel <= 0 || S.graphSel - 1 >= c.length ? "" : c[S.graphSel - 1].hash;
  }

  function loadContext() {
    var r = curRepo();
    if (!r) { branches = []; graph = []; return; }
    branches = branchesFor(r);
    graph = graphFor(r);
    S.graphSel = 0;
    S.graphOffset = 0;
    if (S.branchCursor >= visibleBranches().length) S.branchCursor = 0;
    if (S.bottomView === "changes") loadChanges();
  }
  function loadChanges() {
    var r = curRepo();
    if (!r) { changeFiles = []; return; }
    var ref = selectedRef();
    changeFiles = ref ? (COMMIT_FILES[ref] || []) : (WIP_FILES[r.n] || []);
    S.changeCursor = 0;
    S.changeShowDiff = false;
  }

  // plain strips our own markup so the status line can be announced as text
  function plain(html) {
    var t = document.createElement("div");
    t.innerHTML = html;
    return (t.textContent || "").trim();
  }

  var statusTimer = null;
  function setStatus(html) {
    S.statusLine = html;
    S.statusGen++;
    // The terminal rebuilds its innerHTML wholesale, so nothing inside it can be
    // a stable live region. Mirror the status into one that lives outside it —
    // this is the only feedback a screen-reader user gets from the demo.
    if (el.say) el.say.textContent = plain(html);
    if (statusTimer) clearTimeout(statusTimer);
    var gen = S.statusGen;
    statusTimer = setTimeout(function () {
      if (gen === S.statusGen) { S.statusLine = ""; render(); }
    }, 4000); // statusTTL
    return html;
  }

  /* ------------------------------------------------------------- rendering */

  // syncGlyph, ported from view.go
  function syncGlyph(r) {
    if (!r.loaded) return d(".");
    if (r.fetching) return d("~");
    var uni = S.glyphs === "unicode";
    var up = uni ? "↑" : "+", dn = uni ? "↓" : "-";
    if (!r.up) return r.remote ? rd("!") : d("no-remote");
    if (r.ahead > 0 && r.behind > 0) return mg(up + r.ahead + " " + dn + r.behind);
    if (r.ahead > 0) return yl(up + r.ahead);
    if (r.behind > 0) return cy(dn + r.behind);
    return gr("ok");
  }
  function dirtyBadge(r) { return r.dirty > 0 ? og("*" + r.dirty) : ""; }

  function renderRow(i, r) {
    var on = i === S.cursor && S.focus === "repos";
    var mark = i === S.cursor;
    var name = esc(r.n) + d(" (" + currentBranch(r) + ")") +
      (S.showTags && r.tag ? d(" (" + r.tag + ")") : "");
    return (
      '<div class="row" data-on="' + (on ? 1 : 0) + '" data-mark="' + (mark ? 1 : 0) + '">' +
      '<span class="row__cur">' + (mark ? "> " : "  ") + "</span>" +
      '<span class="row__name">' + name + "</span>" +
      '<span class="row__dirty">' + dirtyBadge(r) + "</span>" +
      '<span class="row__st">' + syncGlyph(r) + "</span>" +
      "</div>"
    );
  }

  // renderRepoBody: group headers interleaved, windowed to keep the cursor visible
  function renderRepos(h) {
    var vis = visibleRepos();
    if (!vis.length) {
      return centerBlock(
        d(S.filterAttention ? "Everything is in sync" : 'No repos match "' + S.filter + '"')
      );
    }
    var lines = [], cursorLine = 0, last = null;
    vis.forEach(function (r, i) {
      if (r.g !== last) { lines.push('<div class="gp">' + esc(r.g) + "</div>"); last = r.g; }
      if (i === S.cursor) cursorLine = lines.length;
      lines.push(renderRow(i, r));
    });
    var w = win(lines.length, cursorLine, h);
    return lines.slice(w[0], w[1]).join("");
  }

  function renderScripts(h) {
    var vs = visibleScripts();
    if (!vs.length) return d('(no scripts match "' + S.filter + '")');
    var w = win(vs.length, S.scriptCursor, h);
    var out = "";
    for (var i = w[0]; i < w[1]; i++) {
      var on = S.focus === "scripts" && i === S.scriptCursor;
      out += "<div>" + (on ? cur("> ") : "  ") + esc(vs[i].name) + "</div>";
    }
    return out;
  }

  function renderBranches(h) {
    var vb = visibleBranches();
    if (!vb.length) {
      // only claim "no match" when the needle is actually this pane's — a Repos
      // filter must not make the branch list report against its needle
      return S.filterPanel === "branches" && S.filter
        ? d('(no branches match "' + S.filter + '")')
        : "";
    }
    var w = win(vb.length, S.branchCursor, h);
    var out = "";
    for (var i = w[0]; i < w[1]; i++) {
      var b = vb[i];
      var on = S.focus === "branches" && i === S.branchCursor;
      out += "<div>" + (on ? cur("> ") : "  ") +
        (b.remote ? d(b.name) : esc(b.name)) +
        (b.current ? gr(" (current)") : "") + "</div>";
    }
    return out;
  }

  function centerBlock(msg, hint) {
    return '<div class="center"><div>' + msg + "</div>" +
      (hint ? "<div>" + hint + "</div>" : "") + "</div>";
  }

  // boot replays Init(): every repo is unloaded and immediately marked fetching,
  // so a row goes "." -> "~" -> its real glyph as the local status read lands and
  // then the fetch returns. Timings stand in for the real work — the *order* is
  // Init's, and the fetch waves are the concurrency semaphore (cfg.Concurrency,
  // 8) letting eight repos through at a time.
  var CONCURRENCY = 8;
  function boot() {
    if (S.booted) return;
    S.booted = true;

    var repos = discovered();
    repos.forEach(function (r) { r.loaded = false; r.fetching = true; });
    branches = [];
    graph = [];
    S.ghProbed = false;
    S.ghAvailable = false;
    S.ghUser = "";
    S.prLoaded = false;
    S.newsFeed = [];
    S.newsLoading = false;
    S.newsIndex = 0;
    render();

    var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduce) { bootFinish(repos); return; } // no reveal; land on the loaded state

    // statusCmd per repo: fast local reads, ungated, so they land quickly
    repos.forEach(function (r, i) {
      setTimeout(function () { r.loaded = true; render(); }, 120 + i * 40);
    });

    // fetchCmd per repo: gated by the semaphore, so they return in waves
    repos.forEach(function (r, i) {
      var wave = Math.floor(i / CONCURRENCY);
      setTimeout(function () { r.fetching = false; render(); }, 640 + wave * 420 + (i % CONCURRENCY) * 55);
    });

    setTimeout(function () { loadContext(); render(); }, 300);           // loadContextCmd
    setTimeout(function () {                                              // ghProbeCmd
      S.ghProbed = true; S.ghInstalled = true; S.ghAvailable = true; S.ghUser = "rabeeh-ta";
      render();
      setTimeout(function () { S.prLoaded = true; render(); }, 260 );    // then both PR lists
    }, 780);
    setTimeout(function () { S.newsLoading = true; render(); }, 900);     // harness summarising
    setTimeout(function () {                                              // newsFeedMsg
      S.newsLoading = false; S.newsFeed = NEWS.slice(); render();
    }, 2100);
  }

  // bootFinish is the settled state, with no reveal — used under reduced motion.
  function bootFinish(repos) {
    repos.forEach(function (r) { r.loaded = true; r.fetching = false; });
    S.ghProbed = true; S.ghInstalled = true; S.ghAvailable = true; S.ghUser = "rabeeh-ta";
    S.prLoaded = true;
    S.newsFeed = NEWS.slice();
    loadContext();
    render();
  }

  // prUnavailableHint explains why the PR pane is empty when gh isn't usable —
  // still probing, not installed, or installed but not signed in.
  function prUnavailableHint() {
    if (!S.ghProbed) return "checking GitHub...";
    if (!S.ghInstalled) return "gh not installed\nsee cli.github.com to enable the PRs tab";
    return "gh found but not signed in\nrun: gh auth login";
  }

  function prEmptyState() {
    if (S.filterPanel === "prs" && S.filter)
      return [d('No PRs match "' + S.filter + '"'), d("esc to clear the filter")];
    if (!S.prLoaded) return [d("Loading PRs..."), ""];
    if (S.prShowReview)
      return [d("You're all caught up"), d("no PRs awaiting your review  ·  m: my PRs")];
    return [d("No open PRs authored by you"), d("m: review requests")];
  }

  function renderPRs(h) {
    if (!S.ghAvailable) {
      return centerBlock(d(prUnavailableHint()).replace(/\n/g, "<br>"));
    }
    var my = "my PRs (" + PR_MINE.length + ")";
    var rev = "review requests (" + PR_REVIEW.length + ")";
    var header = S.prShowReview
      ? " " + d("m: " + my) + d("    ") + gp(rev)
      : " " + gp(my) + d("    ") + d("m: " + rev);

    var prs = visiblePRs();
    if (!prs.length) {
      var e = prEmptyState();
      return "<div>" + header + "</div>" + centerBlock(e[0], e[1]);
    }
    var w = win(prs.length, S.prCursor, Math.max(1, h - 2));
    var out = "<div>" + header + "</div><div>&nbsp;</div>";
    for (var i = w[0]; i < w[1]; i++) {
      var p = prs[i];
      var on = S.focus === "branches" && i === S.prCursor;
      out += "<div>" + (on ? " " + cur("> ") : "   ") +
        yl("#" + p.num) + "  " + ghUser(p.author) + "  " + esc(p.title) +
        (p.draft ? d(" [draft]") : "") + d("  " + p.repo) + "</div>";
    }
    return out;
  }

  function graphLineHTML(g) {
    if (!g.hash) return d(g.gfx);
    var refs = "";
    if (g.refs.length) {
      refs = d(" (") + g.refs.map(function (r) { return sp(r[0], r[1]); }).join(d(", ")) + d(")");
    }
    return d(g.gfx) + yl(g.hash) + refs + " " + esc(g.subj);
  }

  function renderGraph(h) {
    var texts = [yl("WIP (uncommitted changes)")];
    graph.forEach(function (g) { texts.push(graphLineHTML(g)); });
    // selIdx: the render index of the selected entry (0 = WIP)
    var selIdx = 0;
    if (S.graphSel >= 1) {
      var c = graphCommits()[S.graphSel - 1];
      if (c) selIdx = graph.indexOf(c) + 1;
    }
    var w = win(texts.length, selIdx, h);
    var out = "";
    for (var i = w[0]; i < w[1]; i++) {
      var on = S.focus === "bottom" && i === selIdx;
      out += "<div>" + (on ? cur("> ") : "  ") + texts[i] + "</div>";
    }
    return out;
  }

  function colorStatus(s) {
    if (s === "A" || s === "??") return gr(s);
    if (s === "D") return rd(s);
    if (s.charAt(0) === "R") return cy(s);
    return yl(s);
  }

  function renderChanges(h) {
    if (S.changeShowDiff) {
      var w2 = win(changeDiff.length, S.changeDiffOff, h);
      var o2 = "";
      for (var j = w2[0]; j < w2[1]; j++) {
        var ln = changeDiff[j];
        o2 += "<div>" + (ln[0] ? sp(ln[0], ln[1]) : esc(ln[1])) + "</div>";
      }
      return o2;
    }
    if (!changeFiles.length) {
      var ref = selectedRef();
      return centerBlock(d("no changes in " + (ref ? "commit " + ref : "working tree")));
    }
    var w = win(changeFiles.length, S.changeCursor, h);
    var out = "";
    for (var i = w[0]; i < w[1]; i++) {
      var f = changeFiles[i];
      var on = S.focus === "bottom" && i === S.changeCursor;
      var pad = "   ".slice(f.s.length) || " ";
      out += "<div>" + (on ? cur("> ") : "  ") + colorStatus(f.s) + pad + esc(f.p) + "</div>";
    }
    return out;
  }

  function renderOutput(h) {
    if (!S.outputLines.length) {
      return centerBlock(d(S.outputRunning
        ? "running " + S.outputTitle + "..."
        : "run a script from [2] Scripts to see its output here"));
    }
    var w = win(S.outputLines.length, S.outputOffset, h);
    var out = "";
    for (var i = w[0]; i < w[1]; i++) {
      var l = S.outputLines[i];
      // `done.` is tested before `skipped` — the summary line mentions skips and
      // would otherwise be coloured as one.
      var cls = /^\$/.test(l) ? "a"
        : /^==>/.test(l) ? "gp"
        : /^done\./.test(l) ? "gr"
        : /skipped|drifted|yes$/.test(l) ? "og"
        : "";
      out += "<div>" + (cls ? sp(cls, l) : esc(l)) + "</div>";
    }
    return out;
  }

  /* -- tab bars (tabBar in view.go) ---------------------------------------- */

  function tabBar(tabs, active) {
    return tabs.map(function (t, i) {
      return '<span class="tab" data-on="' + (i === active ? 1 : 0) + '">' +
        esc(t.n + " " + t.name) + "</span>";
    }).join('<span class="tabdiv">│</span>');
  }
  function topTabs() {
    return tabBar([{ n: 3, name: "Branches" }, { n: 4, name: "PRs" }], S.topView === "prs" ? 1 : 0);
  }
  function bottomTabs() {
    var o = S.outputRunning ? "Output*" : "Output";
    var idx = S.bottomView === "graph" ? 0 : S.bottomView === "changes" ? 1 : 2;
    return tabBar([{ n: 5, name: "Graph" }, { n: 6, name: "Changes" }, { n: 7, name: o }], idx);
  }
  function topHint() {
    if (S.focus !== "branches") return "";
    var h = S.topView === "prs" ? "enter: checkout   m: toggle list" : "enter: checkout";
    return '<span class="tabhint">' + esc(h) + "</span>";
  }
  function bottomHint() {
    if (S.focus !== "bottom") return "";
    var h = "";
    if (S.bottomView === "graph") h = "enter: its files";
    else if (S.bottomView === "changes" && S.changeShowDiff) h = "esc: back";
    else if (S.bottomView === "changes" && changeFiles.length) h = "enter: diff   esc: back";
    if (!h) return "";
    return '<span class="tabhint">' + esc(h) + "</span>";
  }

  /* -- bars ---------------------------------------------------------------- */

  function prBadge() {
    if (!S.ghAvailable) return "";
    var rev = PR_REVIEW.length, mine = PR_MINE.length;
    if (!rev && !mine) return "";
    var parts = [];
    if (rev) parts.push(yl("review " + rev));
    if (mine) parts.push(d("mine " + mine));
    return gp("PR ") + parts.join("  ");
  }
  function topBarMain() {
    if (S.filtering || S.filter || S.filterAttention) {
      var c = visibleRepos().length + " of " + discovered().length + " repos";
      return d(c) + (S.filterAttention ? "  " + yl("[changed / unsynced]") : "");
    }
    if (!S.newsFeed.length) {
      var count = discovered().length + " repos";
      return d(count) + (S.newsLoading ? d("   summarizing commits...") : "");
    }
    var line = gp("news ") + esc(S.newsFeed[S.newsIndex % S.newsFeed.length]);
    if (S.newsFeed.length > 1) {
      line += d("   (" + ((S.newsIndex % S.newsFeed.length) + 1) + "/" + S.newsFeed.length + ")");
    }
    return line;
  }
  function statusOrFilter() {
    if (S.filtering) return yl("/" + S.filter + "_");
    if (S.statusLine) return S.statusLine;
    var enter = "enter branches";
    if (S.focus === "scripts") enter = "enter run";
    else if (S.focus === "branches") enter = S.topView === "prs" ? "enter checkout PR" : "enter checkout";
    return d(enter + " | z zoom | g graph | n news | t tags | F changed | s sync | p push | d/D discard | o open | r refetch | ? help | q quit");
  }
  function indicators() {
    var harnessOK = HARNESSES.some(function (h) { return h.name === S.harness && h.installed; });
    var hi = S.harness
      ? (harnessOK ? gp("harness: " + S.harness) : d("harness: " + S.harness + " (n/a)"))
      : d("no AI harness");
    // githubIndicator: absent until gh is probed AND authed
    return (S.ghAvailable && S.ghUser ? gp("github: " + S.ghUser) : "") + hi;
  }

  /* -- panes --------------------------------------------------------------- */

  // titledBox / panelStyle: the ? overlay is an untitled full-screen panel
  // (overlayBox), so an empty title renders no label at all.
  function pane(title, focused, body, id) {
    return '<div class="pane" data-focused="' + (focused ? 1 : 0) + '">' +
      (title ? '<div class="pane__title">' + title + "</div>" : "") +
      '<div class="pane__body" data-pane="' + id + '">' + body + "</div></div>";
  }

  /* -- settings overlay (settingsBody in view.go) --------------------------- */

  function settingRows() {
    var rows = [];
    THEMES.forEach(function (t) { rows.push({ kind: SK_THEME, val: t }); });
    HARNESSES.forEach(function (h) { rows.push({ kind: SK_HARNESS, val: h.name }); });
    NEWS_DAYS.forEach(function (n) { rows.push({ kind: SK_NEWSDAYS, val: String(n) }); });
    MAX_DEPTHS.forEach(function (n) { rows.push({ kind: SK_MAXDEPTH, val: String(n) }); });
    rows.push({ kind: SK_GLYPH, val: "unicode" }, { kind: SK_GLYPH, val: "ascii" });
    rows.push({ kind: SK_EDITOR, val: "" });
    return rows;
  }

  function settingsBody(h) {
    function radio(sel) { return sel ? gr("(*) ") : "( ) "; }
    function line(on, mark, label) {
      return "<div>   " + (on ? cur("> ") : "  ") + mark + (on ? cur(label) : d(label)) + "</div>";
    }
    var hdr = {};
    hdr[SK_THEME] = gp("Theme") + d("   (previews live)");
    hdr[SK_HARNESS] = gp("AI harness") + d("   (grayed = not installed)");
    hdr[SK_NEWSDAYS] = gp("News window") + d("   (top-bar feed lookback)");
    hdr[SK_MAXDEPTH] = gp("Scan depth") + d("   (folders below the root to search; rescans on select)");
    hdr[SK_GLYPH] = gp("Ahead / behind glyphs");
    hdr[SK_EDITOR] = gp("Editor") + d("   (`o` opens the repo — e.g. code, cursor, code -r)");

    var mid = [], cursorLine = 0, prev = -1, rows = settingRows();
    rows.forEach(function (r, i) {
      if (r.kind !== prev) { mid.push("<div>" + hdr[r.kind] + "</div>"); prev = r.kind; }
      var on = S.settingsCursor === i;
      if (on) cursorLine = mid.length;
      if (r.kind === SK_THEME) {
        mid.push(line(on, radio(S.theme === r.val), r.val));
      } else if (r.kind === SK_HARNESS) {
        var inst = HARNESSES.filter(function (x) { return x.name === r.val; })[0].installed;
        var label = r.val + (inst ? "" : "  (not installed)");
        var lbl = on && inst ? cur(label) : d(label);
        mid.push("<div>   " + (on ? cur("> ") : "  ") + radio(inst && S.harness === r.val) + lbl + "</div>");
      } else if (r.kind === SK_NEWSDAYS) {
        var n = parseInt(r.val, 10);
        mid.push(line(on, radio(S.newsDays === n), n === 1 ? "1 day" : n + " days"));
      } else if (r.kind === SK_MAXDEPTH) {
        var dp = parseInt(r.val, 10);
        var dl = dp === 1 ? "1 level" : dp + " levels";
        if (dp === 3) dl += "  (default)"; // config.Default().MaxDepth
        mid.push(line(on, radio(S.maxDepth === dp), dl));
      } else if (r.kind === SK_GLYPH) {
        var gl = r.val === "ascii" ? "ascii    (+ / -)" : "unicode  (arrows)";
        mid.push(line(on, radio(r.val === S.glyphs), gl));
      } else {
        var val = S.editingOpenCmd ? S.openCmdBuf + "_" : S.openCmd;
        var hint = S.editingOpenCmd ? "   enter saves · esc cancels" : on ? "   enter to edit" : "";
        mid.push("<div>   " + (on ? cur("> ") : "  ") + (on ? cur(val) : d(val)) + d(hint) + "</div>");
      }
    });
    var avail = Math.max(1, h - 3);
    var w = win(mid.length, cursorLine, avail);
    return "<div>" + sp("a", "manygit — settings") + "</div><div>&nbsp;</div>" +
      mid.slice(w[0], w[1]).join("") +
      "<div>" + d("   j/k move · enter select · tab keybindings · esc close") + "</div>";
  }

  function keysBody() {
    var uni = S.glyphs === "unicode";
    var up = uni ? "↑" : "+", dn = uni ? "↓" : "-";
    function kr(k, t) { return '<div>  <span class="kcol">' + k + "</span>" + d(t) + "</div>"; }
    var left = [
      "<div>" + gp("Panels & navigation") + "</div>",
      kr("1/2/3", "focus Repos / Scripts / Branches"),
      kr("4", "PRs (beside Branches)"),
      kr("5/6/7", "bottom: Graph / Changes / Output"),
      kr("tab", "cycle panels"),
      kr("z", "zoom the focused pane full-screen"),
      kr("j/k", "move in the focused panel"),
      kr("←/→", "hop between Repos and Branches"),
      kr("enter", "branches / checkout / run / checkout PR"),
      kr("g", "full-screen commit graph"),
      kr("n", "full-screen news feed (all headlines)"),
      kr("t", "toggle each repo's latest tag inline"),
      kr("F", "only changed / unsynced repos"),
      kr("/", "filter the focused list"),
      "<div>&nbsp;</div>",
      "<div>" + gp("GitHub PRs (4)") + d("   (needs gh)") + "</div>",
      kr("m", "toggle mine / review-requested"),
      kr("enter", "checkout the PR's branch in its repo")
    ];
    var right = [
      "<div>" + gp("Actions") + d(" on the > repo") + "</div>",
      kr("s", "sync (fetch + pull --ff-only)"),
      kr("p", "push"),
      kr("f/r", "fetch current / refetch all"),
      kr("b/enter", "checkout selected branch"),
      kr("d/D", "discard changes / +untracked (confirm)"),
      kr("o", "open the repo in your editor"),
      "<div>&nbsp;</div>",
      "<div>" + gp("Status column") + "</div>",
      kr(gr("ok"), "up to date with upstream"),
      kr(yl(up + "N"), "ahead — commits to PUSH"),
      kr(cy(dn + "N"), "behind — commits to PULL"),
      kr(mg(up + "N" + dn + "M"), "diverged"),
      kr(og("*N"), "N files changed (dirty)"),
      kr(d("~ ."), "fetching / loading"),
      kr(d("no-remote"), "local-only repo (no remote)"),
      kr(rd("!"), "no upstream, or error")
    ];
    return "<div>" + sp("a", "manygit — keybindings") + d("   (tab: back to settings · esc close)") + "</div>" +
      '<div>&nbsp;</div><div class="kcols"><div>' + left.join("") + "</div><div>" + right.join("") + "</div></div>";
  }

  /* -- the screen ---------------------------------------------------------- */

  var el = {};
  var LINE_H = 18;
  var H = {}; // measured rows per pane, filled in after the first paint

  function rows(id, fallback) { return H[id] || fallback; }

  function paint() {
    if (S.showGraph) { renderOverlay(graphOverlay(rows("gfull", 20))); return; }
    if (S.showNews) { renderOverlay(newsOverlay(rows("nfull", 20))); return; }
    if (S.showHelp) {
      // helpView is an untitled full-screen panel (overlayBox), not a titledBox
      renderOverlay(pane("", true, S.showKeys ? keysBody() : settingsBody(rows("help", 20)), "help"));
      return;
    }

    var bars = '<div class="term__top">' +
      '<span class="term__brand">manygit</span>' +
      '<span class="term__news">' + topBarMain() + "</span>" +
      '<span class="term__badge">' + prBadge() + "</span></div>";
    var foot = '<div class="term__bot">' +
      '<span class="term__status">' + statusOrFilter() + "</span>" +
      '<span class="term__ind">' + indicators() + "</span></div>";

    if (S.zoomed) { paintZoom(bars, foot); return; }

    el.screen.innerHTML = bars +
      '<div class="term__body">' +
      '<div class="term__col term__col--l">' +
      pane(d("[1] Repos"), S.focus === "repos", renderRepos(rows("repos", 12)), "repos") +
      pane(d("[2] Scripts"), S.focus === "scripts", renderScripts(rows("scripts", 6)), "scripts") +
      "</div>" +
      '<div class="term__col term__col--r">' +
      pane(topTabs() + topHint(), S.focus === "branches",
        S.topView === "prs" ? renderPRs(rows("top", 7)) : renderBranches(rows("top", 7)), "top") +
      pane(bottomTabs() + bottomHint(), S.focus === "bottom", renderBottom(rows("bottom", 11)), "bottom") +
      "</div></div>" + foot;
  }

  // Paint, then correct the row counts from the real layout and repaint once if
  // they were wrong. Pane heights are fixed by the CSS grid and don't depend on
  // content (.pane__body is height:100%, overflow:hidden), so this converges
  // after one correction and every later render measures the same values.
  function render() {
    paint();
    var changed = false;
    Array.prototype.forEach.call(el.screen.querySelectorAll("[data-pane]"), function (e) {
      var id = e.getAttribute("data-pane");
      var r = Math.max(1, Math.floor(e.clientHeight / LINE_H));
      if (r > 0 && H[id] !== r) { H[id] = r; changed = true; }
    });
    if (changed) paint();
  }

  function renderBottom(h) {
    if (S.bottomView === "changes") return renderChanges(h);
    if (S.bottomView === "output") return renderOutput(h);
    return renderGraph(h);
  }

  function renderOverlay(html) {
    el.screen.innerHTML = '<div class="term__body term__body--one">' + html + "</div>";
  }

  function paintZoom(bars, foot) {
    var body, title;
    var h = rows("zoom", 18);
    if (S.focus === "bottom") { title = bottomTabs() + bottomHint(); body = renderBottom(h); }
    else if (S.focus === "scripts") { title = d("[2] Scripts"); body = renderScripts(h); }
    else if (S.focus === "branches") { title = topTabs() + topHint(); body = S.topView === "prs" ? renderPRs(h) : renderBranches(h); }
    else { title = d("[1] Repos"); body = renderRepos(h); }
    el.screen.innerHTML =
      '<div class="term__top"><span class="term__brand">manygit</span>' +
      '<span class="term__news">' + d(discovered().length + " repos") + d("   [zoom — z to restore]") + "</span></div>" +
      '<div class="term__body term__body--zoom">' + pane(title, true, body, "zoom") + "</div>" + foot;
  }

  function graphOverlay(h) {
    var r = curRepo();
    var lines = graph.map(graphLineHTML);
    var start = clamp(S.graphOffset, 0, Math.max(0, lines.length - 1));
    var body = lines.slice(start, start + h).map(function (l) { return "<div>" + l + "</div>"; }).join("");
    return pane(d("Graph: " + (r ? r.n : "(no repo)") + "  (j/k scroll, esc close)"), true, body, "gfull");
  }
  function newsOverlay(h) {
    var lines = S.newsFeed.map(function (n, i) { return "<div>" + d(String(i + 1).padStart(2) + " ") + esc(n) + "</div>"; });
    var start = clamp(S.newsOffset, 0, Math.max(0, lines.length - 1));
    return pane(d("News — " + S.newsFeed.length + " headlines  (j/k scroll, esc close)"), true,
      lines.slice(start, start + h).join(""), "nfull");
  }

  /* ---------------------------------------------------------------- themes */

  function applyTheme(name) {
    document.documentElement.setAttribute("data-theme", name);
  }
  function previewSettings() {
    var r = settingRows()[S.settingsCursor];
    applyTheme(r.kind === SK_THEME ? r.val : S.theme);
  }

  /* ------------------------------------------------------------------ keys */

  function clearPRFilter() {
    if (S.filterPanel === "prs") { S.filter = ""; S.filterPanel = "repos"; S.prCursor = 0; }
  }
  // setMaxDepth mirrors the Go rescanMsg handler: the depth is only committed if
  // the walk actually finds repos. main.go refuses to start on an empty tree, so
  // `?` must not be able to drop you into one either — a fruitless depth keeps
  // both the old depth and the old list.
  function setMaxDepth(depth) {
    if (depth === S.maxDepth) return; // no walk at all
    var found = REPOS.filter(function (r) { return r.depth <= depth; });
    if (!found.length) {
      setStatus(og("no repos at depth " + depth + " — staying at " + S.maxDepth));
      return;
    }
    var before = discovered().length;
    S.maxDepth = depth;
    S.cursor = 0;
    clearBranchFilter();
    loadContext();
    var added = Math.max(0, found.length - before), dropped = Math.max(0, before - found.length);
    setStatus(gr("depth " + depth + ": " + found.length + " repos (+" + added + ", -" + dropped + ")"));
  }

  function clearBranchFilter() {
    if (S.filterPanel === "branches") { S.filter = ""; S.filterPanel = "repos"; S.branchCursor = 0; }
  }

  // keepCursorOn re-points the cursor at the repo at path within the current
  // visible set, or clamps to the top when it's no longer there. Unlike
  // focusRepoByPath it preserves the active filter — it exists for changes that
  // reshuffle the filtered list rather than escape it.
  //
  // It reloads the context panes only when the cursor actually ends up on a
  // different repo. Reloading regardless would be worse than wasteful: it resets
  // graphSel/graphOffset and changeShowDiff, so a reshuffle that moved nothing
  // would still collapse an open diff and scroll the graph back to the top.
  function keepCursorOn(path) {
    S.cursor = 0;
    var vis = visibleRepos();
    for (var i = 0; i < vis.length; i++) {
      if (vis[i].path === path) { S.cursor = i; break; }
    }
    var r = curRepo();
    if (r && r.path === path) return; // same repo still under the cursor — nothing to reload
    loadContext();
  }

  function topScroll(n) {
    if (S.topView === "prs") S.prCursor = clamp(S.prCursor + n, 0, Math.max(0, visiblePRs().length - 1));
    else S.branchCursor = clamp(S.branchCursor + n, 0, Math.max(0, visibleBranches().length - 1));
  }
  function bottomScroll(n) {
    if (S.bottomView === "graph") S.graphSel = clamp(S.graphSel + n, 0, graphCommits().length);
    else if (S.bottomView === "changes") {
      if (S.changeShowDiff) S.changeDiffOff = clamp(S.changeDiffOff + n, 0, Math.max(0, changeDiff.length - 1));
      else S.changeCursor = clamp(S.changeCursor + n, 0, Math.max(0, changeFiles.length - 1));
    } else S.outputOffset = clamp(S.outputOffset + n, 0, Math.max(0, S.outputLines.length - 1));
  }

  function runScript() {
    var vs = visibleScripts();
    if (S.scriptCursor < 0 || S.scriptCursor >= vs.length) return;
    S.outputRun++;
    var run = S.outputRun;
    S.outputTitle = vs[S.scriptCursor].name;
    S.outputLines = [];
    S.outputOffset = 0;
    S.outputRunning = true;
    S.focus = "bottom";
    S.bottomView = "output";
    var lines = ["$ " + S.outputTitle, ""].concat(SCRIPT_OUT[S.outputTitle] || SCRIPT_FALLBACK);
    var i = 0;
    var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    (function step() {
      if (run !== S.outputRun) return; // superseded
      if (i >= lines.length) {
        S.outputRunning = false;
        setStatus(gr("ran " + S.outputTitle));
        render();
        return;
      }
      // reduced motion: no line-by-line reveal, just show the finished output
      var n = reduce ? lines.length : 1;
      for (var k = 0; k < n && i < lines.length; k++, i++) {
        // appendOutput: follow the tail only if we were already at it, so j/k
        // scrollback during a run isn't yanked back down by the next line
        var atBottom = S.outputOffset >= S.outputLines.length - 1;
        S.outputLines.push(lines[i]);
        if (atBottom) S.outputOffset = S.outputLines.length - 1;
      }
      render();
      setTimeout(step, reduce ? 0 : 55);
    })();
  }

  function checkoutSelected() {
    var vb = visibleBranches(), r = curRepo();
    if (!r || S.focus !== "branches" || S.branchCursor >= vb.length) return;
    if (r.dirty > 0) { setStatus(og("checkout skipped: dirty working tree")); return; }
    var name = vb[S.branchCursor].name.replace(/^origin\//, ""); // Branch.LocalName()
    r.b = name;
    loadContext(); // checkoutDoneMsg batches loadContextCmd — graph and Changes must follow
    setStatus(gr("checked out " + name + " in " + r.n));
  }

  function checkoutPR() {
    var prs = visiblePRs();
    if (S.prCursor < 0 || S.prCursor >= prs.length) return;
    var pr = prs[S.prCursor];
    var target = discovered().filter(function (r) { return r.n === pr.repo; })[0];
    if (!target) { setStatus(og("PR repo " + pr.repo + " is not in view")); return; }
    if (target.dirty > 0) { setStatus(og("checkout skipped: dirty working tree in " + target.n)); return; }
    // focusRepoByPath: land on that repo's Branches, ready to review. Note
    // topView flips back to Branches — the fix from TestTUI_PRCheckoutLandsOnBranches.
    target.b = "pr-" + pr.num;
    S.filter = ""; S.filterPanel = "repos"; S.filterAttention = false;
    S.cursor = discovered().indexOf(target);
    S.branchCursor = 0;
    S.focus = "branches";
    S.topView = "branches";
    loadContext(); // branchesFor() puts pr-N at the top, marked current
    setStatus(gr("checked out PR #" + pr.num + " in " + target.n));
  }

  function armDiscard(full) {
    var r = curRepo();
    if (!r) return;
    if (r.dirty === 0) { setStatus(esc("nothing to discard in " + r.n)); return; }
    S.confirmDiscard = true; S.confirmFull = full; S.confirmName = r.n;
    setStatus(rd(full
      ? "discard " + r.n + " + untracked files?  y = confirm, any key = cancel"
      : "discard changes in " + r.n + "?  y = confirm, any key = cancel"));
  }

  function refetchAll() {
    discovered().forEach(function (r) { r.fetching = true; });
    render();
    discovered().forEach(function (r, i) {
      setTimeout(function () { r.fetching = false; render(); }, 260 + i * 70);
    });
  }

  function handleFilterKey(k) {
    if (k === "Escape") { S.filtering = false; S.filter = ""; }
    else if (k === "Enter") { S.filtering = false; }
    else if (k === "Backspace") { S.filter = S.filter.slice(0, -1); }
    // Go appends only under `case tea.KeyRunes`; space arrives as tea.KeySpace
    // and is dropped, so a space never enters the needle.
    else if (k.length === 1 && k !== " ") { S.filter += k; }
    else return;
    if (S.filterPanel === "scripts") S.scriptCursor = 0;
    else if (S.filterPanel === "branches") S.branchCursor = 0;
    else if (S.filterPanel === "prs") S.prCursor = 0;
    else { S.cursor = 0; loadContext(); }
  }

  function handleSettingsKey(k) {
    if (S.editingOpenCmd) {
      if (k === "Escape") S.editingOpenCmd = false;
      else if (k === "Enter") { S.openCmd = S.openCmdBuf.trim(); S.editingOpenCmd = false; }
      else if (k === "Backspace") S.openCmdBuf = S.openCmdBuf.slice(0, -1);
      else if (k.length === 1) S.openCmdBuf += k;
      return;
    }
    var rows = settingRows();
    if (k === "Tab" || k === "?") S.showKeys = !S.showKeys;
    else if (k === "Escape") { applyTheme(S.theme); S.showHelp = false; }
    else if ((k === "j" || k === "ArrowDown") && !S.showKeys) {
      S.settingsCursor = clamp(S.settingsCursor + 1, 0, rows.length - 1); previewSettings();
    } else if ((k === "k" || k === "ArrowUp") && !S.showKeys) {
      S.settingsCursor = clamp(S.settingsCursor - 1, 0, rows.length - 1); previewSettings();
    } else if ((k === "Enter" || k === " ") && !S.showKeys) {
      var r = rows[S.settingsCursor];
      if (r.kind === SK_THEME) {
        S.theme = r.val; applyTheme(r.val);
        try { localStorage.setItem(STORE, r.val); } catch (e) {}
      } else if (r.kind === SK_HARNESS) {
        var h = HARNESSES.filter(function (x) { return x.name === r.val; })[0];
        if (h.installed) S.harness = r.val;
      } else if (r.kind === SK_NEWSDAYS) S.newsDays = parseInt(r.val, 10);
      else if (r.kind === SK_MAXDEPTH) setMaxDepth(parseInt(r.val, 10));
      else if (r.kind === SK_GLYPH) S.glyphs = r.val;
      else { S.editingOpenCmd = true; S.openCmdBuf = S.openCmd; }
    }
  }

  function handleKey(k) {
    if (S.filtering) { handleFilterKey(k); return; }
    if (S.showHelp) { handleSettingsKey(k); return; }
    if (S.confirmDiscard) {
      S.confirmDiscard = false;
      if (k === "y") {
        var r = discovered().filter(function (x) { return x.n === S.confirmName; })[0];
        if (r) { r.dirty = 0; delete WIP_FILES[r.n]; loadChanges(); }
        setStatus(gr("discarded " + (S.confirmFull ? "all changes" : "tracked changes") + " in " + S.confirmName));
      } else setStatus(esc("discard cancelled"));
      return;
    }
    if (S.showGraph) {
      if (k === "g" || k === "Escape") S.showGraph = false;
      else if (k === "j" || k === "ArrowDown") S.graphOffset = Math.min(S.graphOffset + 1, graph.length - 1);
      else if (k === "k" || k === "ArrowUp") S.graphOffset = Math.max(0, S.graphOffset - 1);
      else if (k === "q") setStatus(d("q quits manygit — this is a browser demo"));
      return;
    }
    if (S.showNews) {
      if (k === "n" || k === "Escape") S.showNews = false;
      else if (k === "j" || k === "ArrowDown") S.newsOffset = Math.min(S.newsOffset + 1, S.newsFeed.length - 1);
      else if (k === "k" || k === "ArrowUp") S.newsOffset = Math.max(0, S.newsOffset - 1);
      return;
    }

    switch (k) {
      case "q":
        setStatus(d("q quits manygit — this is a browser demo, so it stays")); break;
      case "?":
        S.showHelp = true; S.showKeys = false;
        S.settingsCursor = Math.max(0, THEMES.indexOf(S.theme)); break;
      case "z": S.zoomed = !S.zoomed; break;
      case "g": S.showGraph = true; S.graphOffset = 0; break;
      case "n": S.showNews = true; S.newsOffset = 0; break;
      case "t": {
        // The tag is part of what `/` matches, so with a repo filter active this
        // resizes the visible list under the cursor — pin it to its repo first.
        var tPath = "";
        var tr = curRepo();
        if (tr) tPath = tr.path;
        S.showTags = !S.showTags;
        if (S.filter && S.filterPanel === "repos") keepCursorOn(tPath);
        break;
      }
      case "1": S.focus = "repos"; break;
      case "2": S.focus = "scripts"; break;
      case "3": S.focus = "branches"; clearPRFilter(); S.topView = "branches"; break;
      case "4": S.focus = "branches"; S.topView = "prs"; break;
      case "5": S.focus = "bottom"; clearPRFilter(); S.bottomView = "graph"; break;
      case "6": S.focus = "bottom"; clearPRFilter(); S.bottomView = "changes"; S.changeShowDiff = false; loadChanges(); break;
      case "7": S.focus = "bottom"; clearPRFilter(); S.bottomView = "output"; break;
      case "Tab": {
        var order = ["repos", "scripts", "branches", "bottom"];
        S.focus = order[(order.indexOf(S.focus) + 1) % order.length]; break;
      }
      case "ArrowRight":
        if (S.focus === "repos") { S.focus = "branches"; S.topView = "branches"; S.branchCursor = 0; }
        break;
      case "ArrowLeft":
        if (S.focus === "branches") S.focus = "repos";
        break;
      case "j": case "ArrowDown":
        if (S.focus === "repos") {
          if (S.cursor < visibleRepos().length - 1) { S.cursor++; clearBranchFilter(); loadContext(); }
        } else if (S.focus === "branches") topScroll(1);
        else if (S.focus === "scripts") {
          if (S.scriptCursor < visibleScripts().length - 1) S.scriptCursor++;
        } else bottomScroll(1);
        break;
      case "k": case "ArrowUp":
        if (S.focus === "repos") {
          if (S.cursor > 0) { S.cursor--; clearBranchFilter(); loadContext(); }
        } else if (S.focus === "branches") topScroll(-1);
        else if (S.focus === "scripts") { if (S.scriptCursor > 0) S.scriptCursor--; }
        else bottomScroll(-1);
        break;
      case "J":
        if (S.focus === "branches" && S.topView === "branches" && S.branchCursor < visibleBranches().length - 1) S.branchCursor++;
        break;
      case "K":
        if (S.focus === "branches" && S.topView === "branches" && S.branchCursor > 0) S.branchCursor--;
        break;
      case "Enter":
        if (S.focus === "bottom" && S.bottomView === "graph") {
          S.bottomView = "changes"; S.changeShowDiff = false; loadChanges(); break;
        }
        if (S.focus === "bottom" && S.bottomView === "changes" && !S.changeShowDiff) {
          if (changeFiles.length && S.changeCursor < changeFiles.length) {
            changeDiff = DIFF; S.changeDiffOff = 0; S.changeShowDiff = true;
          }
          break;
        }
        if (S.focus === "repos") { S.focus = "branches"; S.topView = "branches"; clearPRFilter(); S.branchCursor = 0; break; }
        if (S.focus === "scripts") { runScript(); return; }
        if (S.focus === "branches" && S.topView === "prs") { checkoutPR(); break; }
        checkoutSelected();
        break;
      case "b": checkoutSelected(); break;
      case "m":
        if (S.focus === "branches" && S.topView === "prs") { S.prShowReview = !S.prShowReview; S.prCursor = 0; }
        break;
      case "Escape":
        if (S.focus === "bottom" && S.bottomView === "changes") {
          if (S.changeShowDiff) S.changeShowDiff = false;
          else S.bottomView = "graph";
        }
        break;
      case "o": {
        var ro = curRepo();
        if (ro) setStatus(d("o runs `" + S.openCmd + " " + ro.path + "` — nothing to open from a browser"));
        break;
      }
      case "F": S.filterAttention = !S.filterAttention; S.cursor = 0; loadContext(); break;
      case "/":
        S.filtering = true; S.filter = "";
        if (S.focus === "scripts") { S.filterPanel = "scripts"; S.scriptCursor = 0; }
        else if (S.focus === "branches" && S.topView === "prs") { S.filterPanel = "prs"; S.prCursor = 0; }
        else if (S.focus === "branches") { S.filterPanel = "branches"; S.branchCursor = 0; }
        else { S.filterPanel = "repos"; S.cursor = 0; }
        break;
      case "f": {
        var rf = curRepo();
        if (rf && !rf.fetching) {
          rf.fetching = true; render();
          setTimeout(function () { rf.fetching = false; render(); }, 500);
          return;
        }
        break;
      }
      case "r": refetchAll(); return;
      case "s": {
        var rs = curRepo();
        if (!rs) break;
        if (!rs.remote) setStatus(og("sync " + rs.n + " skipped: no remote"));
        else if (rs.dirty > 0) setStatus(og("sync " + rs.n + " skipped: dirty working tree"));
        else { rs.behind = 0; setStatus(gr("synced " + rs.n)); }
        break;
      }
      case "p": {
        var rp = curRepo();
        if (!rp) break;
        if (!rp.remote) setStatus(og("push " + rp.n + " skipped: no remote"));
        else { rp.ahead = 0; setStatus(gr("pushed " + rp.n)); }
        break;
      }
      case "d": armDiscard(false); break;
      case "D": armDiscard(true); break;
      default: return;
    }
  }

  /* ------------------------------------------------------------------ boot */

  // Keys the browser would otherwise act on (scroll / quick-find / focus move).
  var SWALLOW = {
    " ": 1, "/": 1, Tab: 1, Enter: 1, ArrowUp: 1, ArrowDown: 1, ArrowLeft: 1, ArrowRight: 1,
    Backspace: 1, Escape: 1
  };

  function onKey(e) {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    // Escape hatch. `tab` cycles panes in manygit, so a keyboard user who tabbed
    // in would otherwise be trapped here forever. shift+tab is unbound in the
    // real TUI, so spending it on "give the focus back" costs nothing.
    if (e.key === "Tab" && e.shiftKey) return;
    var k = e.key;
    if (SWALLOW[k] || k.length === 1) e.preventDefault();
    handleKey(k);
    render();
  }

  // The ground is the SITE's, not manygit's: the tool has no background setting —
  // it inherits your terminal's (theme.go). So this is its own axis from the
  // demo's `?` theme picker, and the two compose. The <head> script has already
  // applied the stored/OS choice before first paint; this only wires the toggle.
  function wireMode() {
    var btn = document.getElementById("mode");
    if (!btn) return;
    var meta = document.querySelector('meta[name="theme-color"]');
    var root = document.documentElement;
    function paint() {
      var light = root.getAttribute("data-mode") === "light";
      btn.textContent = light ? "dark" : "light"; // the label is what you'd get
      btn.setAttribute("aria-label", "Switch to a " + (light ? "dark" : "light") + " terminal");
      if (meta) meta.setAttribute("content", light ? "#eeede8" : "#0b0b0c");
    }
    paint();
    btn.addEventListener("click", function () {
      var next = root.getAttribute("data-mode") === "light" ? "dark" : "light";
      root.setAttribute("data-mode", next);
      try { localStorage.setItem("manygit.mode", next); } catch (e) {}
      paint();
    });
  }

  function boot() {
    wireMode(); // site chrome — must work even if the demo doesn't

    el.term = document.getElementById("term");
    el.screen = document.getElementById("screen");
    el.say = document.getElementById("say");
    if (!el.term) return;

    try {
      var saved = localStorage.getItem(STORE);
      if (saved && THEMES.indexOf(saved) >= 0) S.theme = saved;
    } catch (e) {}
    applyTheme(S.theme);

    var probe = getComputedStyle(el.term).lineHeight;
    var n = parseFloat(probe);
    if (!isNaN(n) && n > 4) LINE_H = n;

    loadContext();
    render();

    el.term.addEventListener("keydown", onKey);
    el.term.addEventListener("focus", function () { boot(); render(); });
    el.term.addEventListener("blur", render);
    window.addEventListener("resize", render);

    // The on-screen keypad runs the same handler, so touch works too. Only a
    // real pointer click pulls focus into the terminal (detail > 0) — a keyboard
    // user activating the button with enter keeps their place in the tab order.
    Array.prototype.forEach.call(document.querySelectorAll("[data-key]"), function (b) {
      b.addEventListener("click", function (e) {
        if (e.detail > 0) el.term.focus();
        boot(); // a keyboard-activated button never fires the term's focus event
        handleKey(b.getAttribute("data-key"));
        render();
      });
    });

    // rotate the news headline, like newsTickCmd
    if (!window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      setInterval(function () {
        if (S.showHelp || S.showGraph || S.showNews || S.newsFeed.length < 2) return;
        S.newsIndex = (S.newsIndex + 1) % S.newsFeed.length;
        render();
      }, 6000);
    }

    // copy buttons
    document.querySelectorAll("[data-copy]").forEach(function (b) {
      b.addEventListener("click", function () {
        var text = b.getAttribute("data-copy");
        var done = function () {
          b.dataset.copied = "1";
          b.textContent = "copied";
          setTimeout(function () { b.dataset.copied = "0"; b.textContent = "copy"; }, 1600);
        };
        if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, function () {});
        else done();
      });
    });
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
