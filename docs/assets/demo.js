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

  var SK_THEME = 0, SK_HARNESS = 1, SK_NEWSDAYS = 2, SK_MAXDEPTH = 3, SK_GLYPH = 4, SK_MOUSE = 5, SK_EDITOR = 6;

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

  // `my PRs` — all authored by the signed-in user, which is what the
  // `author:@me` PR search returns.
  //
  // base/head are gh.PullRequest.BaseRef/HeadRef. The binary gets them from a
  // `gh api graphql` search: `gh search prs --json` has no baseRefName or
  // headRefName field at any version, which is why that query moved to GraphQL.
  //
  // #204 is the fixture for the already-checked-out marker: its head is the
  // branch web-dashboard is sitting on in REPOS, so the row renders green from
  // the first frame.
  var PR_MINE = [
    { num: 412, author: "rabeeh-ta", title: "Retry budget for upstream calls", repo: "api-gateway", base: "main", head: "feat/retry-budget", draft: false },
    { num: 204, author: "rabeeh-ta", title: "Usage chart: switch to the aggregated endpoint", repo: "web-dashboard", base: "main", head: "feat/usage-chart", draft: false },
    { num: 77, author: "rabeeh-ta", title: "Streaming responses in the JS SDK", repo: "sdk-js", base: "release/2.4", head: "feat/streaming-responses", draft: true },
    { num: 39, author: "rabeeh-ta", title: "Drop node 18 from the test matrix", repo: "ci-actions", base: "main", head: "chore/drop-node-18", draft: false }
  ];

  // `review requests`. Ordered so pressing enter down the list walks all three
  // real outcomes of checkoutPR(): a clean repo checks out, a dirty one is
  // skipped with a reason, and one whose repo isn't in the tree says so.
  var PR_REVIEW = [
    { num: 55, author: "Sinu00", title: "Design tokens: a dark-mode pass", repo: "design-system", base: "main", head: "feat/dark-mode-tokens", draft: false },
    { num: 91, author: "zameel7", title: "Bump the cluster to Kubernetes 1.30", repo: "k8s-manifests", base: "staging", head: "chore/k8s-1.30", draft: false },
    { num: 128, author: "nihxdr", title: "Split the OTLP exporter out of core", repo: "telemetry", base: "main", head: "refactor/otlp-exporter", draft: false },
    { num: 143, author: "rafeehcp", title: "Drop the vendored logger", repo: "api-gateway", base: "main", head: "chore/drop-vendored-logger", draft: true },
    { num: 12, author: "Sinu00", title: "Cache the route table between reloads", repo: "docs-site", base: "main", head: "feat/route-table-cache", draft: false }
  ];

  // "Title: detail" — the shape news.go's prompt now asks the harness for, so
  // the overlay can render a heading with its explanation beneath. An entry with
  // no colon is all heading (the last one), which is what an older cached feed
  // looks like and has to keep working.
  var NEWS = [
    "api-gateway landed a token-bucket rate limiter: per-route budgets, a Retry-After header, and the retry budget is next",
    "web-dashboard is mid-refactor on the usage chart: 7 files still dirty, the aggregated endpoint swap is half done",
    "sdk-js has diverged from origin/release/2.4: 1 commit ahead and 3 behind, streaming responses still unmerged",
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

  // What a scripted run actually does to the repos — the reason the Repos pane
  // has to keep up while a script is still running.
  //
  // `git: true` means the change touches .git (a pull moves the branch and
  // rewrites FETCH_HEAD), so git.Fingerprint sees it and the repo-probe tick
  // re-stats that one repo mid-run: its row updates as the line scrolls past,
  // while the other repos cost nothing. `git: false` never touches .git — npm
  // rewriting a lockfile is a working-tree change — so the probe is blind to it
  // by design (TestFingerprint_IgnoresWorkingTreeEdits) and it only surfaces in
  // the full re-stat when the script ends. Both halves of that are real; the
  // demo shows both rather than pretending one mechanism covers everything.
  var SCRIPT_EFFECTS = {
    "scripts/sync-all.sh": [
      { after: "     1 file changed, 16 insertions(+), 8 deletions(-)", repo: "mobile-client", git: true,
        apply: function (r) { r.behind = 0; } },
      { after: "     1 file changed, 3 insertions(+), 3 deletions(-)", repo: "k8s-manifests", git: true,
        apply: function (r) { r.behind = 0; } }
    ],
    "bootstrap.sh": [
      { after: null, repo: "web-dashboard", git: false,
        apply: function (r) { r.dirty += 1; } } // npm ci rewrote package-lock.json
    ]
  };

  /* ----------------------------------------------------------------- state */

  var S = {
    cursor: 0,
    focus: "repos", // repos | scripts | branches | bottom
    topView: "branches", // branches | prs
    bottomView: "graph", // graph | changes | output

    filter: "",
    filtering: false,
    // `:` harness mode. The demo's FOURTH intentional divergence: there is no AI
    // and no git in a browser, so the reply is a canned script. The page says so,
    // exactly as it says the git is fake. Everything else here is real: the input,
    // the ghost completion, the confirm, and the refusal of a dangerous command.
    aiPrompting: false, aiPrompt: "", aiPending: null, aiGhost: false, aiGhostGen: 0,
    // In-memory only, like the Go: a reload starts with an empty history.
    aiHistory: [], aiHistIdx: 0, aiDraft: "",
    // `!` shell mode. The demo's FIFTH intentional divergence: a browser has no
    // shell, so a handful of commands answer from a script and everything else
    // says so plainly. The page says the shell output is scripted, exactly as it
    // says the git is fake. Everything around it is real and ported: the repo
    // binding, the input, the history, the echoed command, the streaming into
    // the Output pane, and esc to cancel.
    shellPrompting: false, shellCmd: "", shellLoc: "", shellRunning: false,
    shellHistory: [], shellHistIdx: 0, shellDraft: "",
    filterPanel: "repos", // repos | scripts | branches | prs
    filterAttention: false,

    showHelp: false, showKeys: false, keysOffset: 0, showGraph: false, showNews: false,
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
    confirmPlan: false,
    confirmFull: false,
    confirmName: "",

    // gh availability, resolved by ghProbeCmd after Init. Until it returns, the
    // PRs pane says "checking GitHub..." and the badge/indicator are absent.
    ghProbed: false,
    ghInstalled: false,
    ghAvailable: false,
    ghUser: "",
    prLoaded: false,
    prChosen: false, // the user picked a list with `m`; autoPickPRList defers to it

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
    mouse: "on", // config.Default().Mouse — click + wheel, like the binary
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
  var bo = function (s) { return sp("b", s); }; // styleStrong: weight, not hue
  var ti = function (s) { return sp("ti", s); }; // styleTitle: accent + bold
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

  // The Go reads r.status, which is the ZERO VALUE until statusMsg lands: an
  // unloaded repo has 0 dirty, 0 ahead, 0 behind, no remote and no branch. Every
  // consumer gets that for free — needsAttention, the s/p/d guards, checkout.
  // The demo's fixtures instead carry their post-load numbers from the very
  // first frame, so anything reading them directly leaks the answer into the
  // boot window: `F` would list seven repos while the binary lists none, and `s`
  // would report "synced" against a row still drawn as `.`. These accessors ARE
  // that zero value — the same rule CLAUDE.md states for runInit.
  function stDirty(r) { return r.loaded ? r.dirty : 0; }
  function stAhead(r) { return r.loaded ? r.ahead : 0; }
  function stBehind(r) { return r.loaded ? r.behind : 0; }
  function stRemote(r) { return r.loaded && r.remote; }

  function needsAttention(r) { return stDirty(r) > 0 || stAhead(r) > 0 || stBehind(r) > 0; }

  // currentBranch is the branch label a row shows: "detached" when the head is,
  // otherwise the branch name. `/` matches this string, so it has to be the same
  // one renderRow draws.
  function currentBranch(r) {
    if (!r.loaded) return ""; // r.status is the zero value until statusMsg lands
    return r.detached ? "detached" : r.b;
  }

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
  // Go: m.prMine/m.prReview are nil until prsMsg lands, so every reader is
  // implicitly gated on prLoaded — the lists, the header counts, and the top-bar
  // badge alike. These accessors are that nil-ness. Without them runInit shows a
  // full PR list in the 260ms before the fetch returns, and prEmptyState's
  // "Loading PRs..." branch can never fire.
  function prMine() { return S.prLoaded ? PR_MINE : []; }
  function prReview() { return S.prLoaded ? PR_REVIEW : []; }
  function activePRs() { return S.prShowReview ? prReview() : prMine(); }
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
  // ctxSettle / contextCmd port update.go. In the Go each repo change costs 2-3
  // git subprocesses (branches + `git log --graph --all`), so holding j down 30
  // rows fired 30 loads and buried the render loop. A change that lands after a
  // quiet gap is deliberate and loads now — a single press stays instant; one
  // that lands mid-sweep only schedules, and each supersedes the last.
  //
  // The gen counter is kept rather than clearTimeout: bubbletea can't cancel a
  // pending tea.Tick, so the Go has to drop superseded ticks by generation, and
  // the port mirrors that rather than taking the browser's shortcut.
  var ctxSettle = 120; // ms — matches ctxSettle in update.go
  var ctxGen = 0, ctxPending = false, lastCtxAt = 0;

  function contextCmd() {
    var now = Date.now();
    var quiet = now - lastCtxAt >= ctxSettle;
    lastCtxAt = now;
    ctxGen++; // supersede any timer already in flight, whichever branch we take
    if (quiet) { ctxPending = false; loadContext(); return; }
    ctxPending = true;
    var gen = ctxGen;
    setTimeout(function () {
      if (gen !== ctxGen || !ctxPending) return; // a later move scheduled its own
      ctxPending = false;
      loadContext();
      render();
    }, ctxSettle);
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
  function dirtyBadge(r) { return r.loaded && r.dirty > 0 ? og("*" + r.dirty) : ""; }

  function renderRow(i, r) {
    var on = i === S.cursor && S.focus === "repos";
    var mark = i === S.cursor;
    // fitNameSuffixes only appends a suffix when it's non-empty — an unloaded repo
    // has no branch yet, and " ()" is not a thing the real tool ever draws.
    var br = currentBranch(r);
    var name = esc(r.n) +
      (br ? d(" (" + br + ")") : "") +
      (S.showTags && r.loaded && r.tag ? d(" (" + r.tag + ")") : "");
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
      // Both filters can be on at once, and then the needle is what emptied the
      // list — `F` + `/zzzz` must not claim "Everything is in sync" while seven
      // repos are dirty. Say why the list is empty, innermost cause first.
      // (The Go renders an empty box here; these two strings are the port's own.
      // See the note in CLAUDE.md.)
      var why = S.filter && S.filterPanel === "repos"
        ? 'No repos match "' + S.filter + '"'
        : "Everything is in sync";
      return centerBlock(d(why));
    }
    var rl = repoLines(vis, S.cursor);
    var w = win(rl.lines.length, rl.cursorLine, h);
    return rl.lines.slice(w[0], w[1]).map(function (e) {
      var r = vis[e.repo];
      return e.header ? '<div class="gp">' + esc(r.g) + "</div>" : renderRow(e.repo, r);
    }).join("");
  }

  // repoLines, ported from view.go: the pane before windowing — a header wherever
  // the group changes, then the repo's row — plus the cursor's line. Shared by
  // renderRepos and clickRow, so a click lands on the row drawn under it.
  function repoLines(vis, cursor) {
    var lines = [], cursorLine = 0, last = null;
    vis.forEach(function (r, i) {
      if (r.g !== last) { lines.push({ repo: i, header: true }); last = r.g; }
      if (i === cursor) cursorLine = lines.length;
      lines.push({ repo: i, header: false });
    });
    return { lines: lines, cursorLine: cursorLine };
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
  function runInit() {
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
    if (reduce) { initSettled(repos); return; } // no reveal; land on the loaded state

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
      // then both PR lists — prsMsg, which is also where the pane picks its list
      setTimeout(function () { S.prLoaded = true; autoPickPRList(); render(); }, 260);
    }, 780);
    setTimeout(function () { S.newsLoading = true; render(); }, 900);     // harness summarising
    setTimeout(function () {                                              // newsFeedMsg
      S.newsLoading = false; S.newsFeed = NEWS.slice(); render();
    }, 2100);
  }

  // initSettled is the state Init leaves behind, with no reveal — reduced motion.
  function initSettled(repos) {
    repos.forEach(function (r) { r.loaded = true; r.fetching = false; });
    S.ghProbed = true; S.ghInstalled = true; S.ghAvailable = true; S.ghUser = "rabeeh-ta";
    S.prLoaded = true;
    autoPickPRList();
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

  // A PR occupies two lines — the title line, then the repo/branch detail line
  // indented under it past the cursor gutter — plus a blank line separating it
  // from the next. Without the gap, nine PRs are eighteen unbroken lines and the
  // eye can't find where one ends and the next starts.
  var PR_ROW_LINES = 2;
  var PR_ROW_GAP = 1;
  var PR_DETAIL_INDENT = "&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;";

  // prRowsThatFit: n rows occupy n*PR_ROW_LINES plus the gaps BETWEEN them,
  // (n-1)*PR_ROW_GAP — so a trailing blank never costs a row the pane could
  // otherwise have shown. Always at least one.
  function prRowsThatFit(h) {
    return Math.max(1, Math.floor((h + PR_ROW_GAP) / (PR_ROW_LINES + PR_ROW_GAP)));
  }

  // prArrow points from the incoming branch to the one receiving it, so the row
  // reads "base gets head". It follows the same status_glyphs setting as ↑/↓:
  // ambiguous-width terminals can render ← two cells wide and drift the line,
  // so `ascii` gets the plain two-character form.
  function prArrow() { return S.glyphs === "unicode" ? "←" : "<-"; }

  // prRepo is repoBySlug: the discovered repo a PR belongs to, or null. The Go
  // matches on the origin slug cached at status-load time; the fixtures carry the
  // short name, so the demo matches on that.
  function prRepo(p) {
    var hit = discovered().filter(function (r) { return r.n === p.repo; });
    return hit.length ? hit[0] : null;
  }

  // prCheckedOut: this repo is sitting on the PR's head branch, i.e. this PR is
  // what it currently has checked out. Guarded on a non-empty head so a PR with
  // unknown refs can't match a repo whose branch hasn't been read yet.
  function prCheckedOut(p) {
    if (!p.head) return false;
    var r = prRepo(p);
    return !!r && r.loaded && currentBranch(r) === p.head;
  }

  // prBranchLine is the second line of a PR row: which repo the PR is in, which
  // branch that repo is checked out to locally, and base ← head. The local
  // branch is omitted when the PR's repo isn't in the tree — which doubles as
  // the tell that enter and o will say it is not in this tree.
  function prBranchLine(p) {
    // The repo is the field you scan this line for — which of a dozen repos this
    // PR is in — so it leads on weight, not dimmed like the detail around it.
    var out = bo(p.repo);
    var r = prRepo(p);
    if (r && r.loaded && currentBranch(r)) {
      var paren = "(" + currentBranch(r) + ")";
      out += " " + (prCheckedOut(p) ? gr(paren) : d(paren));
    }
    if (p.base || p.head) {
      out += d(": ") + cy(p.base || "") + d(" " + prArrow() + " ") + yl(p.head || "");
    }
    return out;
  }

  function renderPRs(h) {
    if (!S.ghAvailable) {
      return centerBlock(d(prUnavailableHint()).replace(/\n/g, "<br>"));
    }
    var my = "my PRs (" + prMine().length + ")";
    var rev = "review requests (" + prReview().length + ")";
    var header = S.prShowReview
      ? " " + d("m: " + my) + d("    ") + gp(rev)
      : " " + gp(my) + d("    ") + d("m: " + rev);

    var prs = visiblePRs();
    if (!prs.length) {
      var e = prEmptyState();
      return "<div>" + header + "</div>" + centerBlock(e[0], e[1]);
    }
    // The window counts ROWS and not lines — an off-by-one leaves a title whose
    // branch line got clamped away.
    var w = win(prs.length, S.prCursor, prRowsThatFit(h - 2));
    var out = "<div>" + header + "</div><div>&nbsp;</div>";
    for (var i = w[0]; i < w[1]; i++) {
      var p = prs[i];
      var on = S.focus === "branches" && i === S.prCursor;
      if (i > w[0]) out += "<div>&nbsp;</div>"; // gap BETWEEN rows, not after the last
      out += "<div>" + (on ? " " + cur("> ") : "   ") +
        yl("#" + p.num) + "  " + ghUser(p.author) + "  " + esc(p.title) +
        (p.draft ? d(" [draft]") : "") + "</div>";
      out += "<div>" + PR_DETAIL_INDENT + prBranchLine(p) + "</div>";
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
      // `<repo> $ cmd` (a `!` run) accents like the bare `$ script.sh` a script
      // run echoes — same rule, widened for the repo prefix.
      var cls = /^(\S+\s)?\$\s/.test(l) ? "a"
        : /^==>/.test(l) ? "gp"
        : /^done\./.test(l) ? "gr"
        : /skipped|drifted|yes$/.test(l) ? "og"
        : "";
      // owrap, not the pane's default `pre`: a long harness note has to flow onto
      // a second line instead of running off the edge. Port of the wrapping the Go
      // does in renderOutputView.
      out += '<div class="owrap">' + (cls ? sp(cls, l) : esc(l)) + "</div>";
    }
    return out;
  }

  /* -- tab bars (tabBar in view.go) ---------------------------------------- */

  function tabBar(tabs, active) {
    return tabs.map(function (t, i) {
      return '<span class="tab" data-tab="' + i + '" data-on="' + (i === active ? 1 : 0) + '">' +
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
    if (S.bottomView === "output" && S.shellRunning) h = "ctrl+c: cancel";
    else if (S.bottomView === "graph") h = "enter: its files";
    else if (S.bottomView === "changes" && S.changeShowDiff) h = "esc: back";
    else if (S.bottomView === "changes" && changeFiles.length) h = "enter: diff   esc: back";
    if (!h) return "";
    return '<span class="tabhint">' + esc(h) + "</span>";
  }

  /* -- bars ---------------------------------------------------------------- */

  function prBadge() {
    if (!S.ghAvailable) return "";
    var rev = prReview().length, mine = prMine().length;
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
    if (lastEscape !== null) return yl("Press Esc again to quit");
    if (S.shellPrompting) return shellPromptLine();
    if (S.aiPrompting) return aiPromptLine();
    if (S.filtering) return yl("/" + S.filter + "_");
    if (S.statusLine) return S.statusLine;
    var enter = "enter branches";
    if (S.focus === "scripts") enter = "enter run";
    else if (S.focus === "branches") enter = S.topView === "prs" ? "enter checkout PR" : "enter checkout";
    return d(enter + " | z zoom | g graph | n news | t tags | F changed | s sync | p push | d/D discard | o open | r refetch | ! shell | : ai | ? help | q/esc esc quit");
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
    return '<div class="pane" data-pid="' + id + '" data-focused="' + (focused ? 1 : 0) + '">' +
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
    rows.push({ kind: SK_MOUSE, val: "on" }, { kind: SK_MOUSE, val: "off" });
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
    hdr[SK_HARNESS] = gp("AI harness") + d("   (grey = not installed)");
    hdr[SK_NEWSDAYS] = gp("News window") + d("   (top-bar lookback)");
    hdr[SK_MAXDEPTH] = gp("Scan depth") + d("   (rescans on select)");
    hdr[SK_GLYPH] = gp("Ahead / behind glyphs");
    hdr[SK_MOUSE] = gp("Mouse") + d("   (off = terminal selects)");
    hdr[SK_EDITOR] = gp("Editor") + d("   (`o` opens the repo)");

    // Two columns, split at SK_MAXDEPTH — the Go splits the same way so the whole
    // list fits the documented 80x20 minimum without scrolling. settingRows()
    // order is unchanged, so j/k still walks top-to-bottom, left column then right.
    var left = [], right = [], cursorLine = 0, prev = -1, rows = settingRows();
    rows.forEach(function (r, i) {
      var mid = r.kind >= SK_MAXDEPTH ? right : left;
      if (r.kind !== prev) {
        if (mid.length) mid.push("<div>&nbsp;</div>"); // air above every group but the column's first
        mid.push("<div>" + hdr[r.kind] + "</div>");
        prev = r.kind;
      }
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
      } else if (r.kind === SK_MOUSE) {
        mid.push(line(on, radio(r.val === S.mouse), r.val === "off" ? "off" : "on   (click + wheel)"));
      } else {
        var val = S.editingOpenCmd ? S.openCmdBuf + "_" : S.openCmd;
        var hint = S.editingOpenCmd ? "   enter saves · esc cancels" : on ? "   enter to edit" : "";
        mid.push("<div>   " + (on ? cur("> ") : "  ") + (on ? cur(val) : d(val)) + d(hint) + "</div>");
      }
    });
    var avail = Math.max(1, h - 4); // title, blank, blank, footer
    var n = Math.max(left.length, right.length);
    while (left.length < n) left.push("<div>&nbsp;</div>");
    while (right.length < n) right.push("<div>&nbsp;</div>");
    var w = win(n, cursorLine, avail);
    return overlayHead() +
      '<div class="scols"><div>' + left.slice(w[0], w[1]).join("") + "</div>" +
      "<div>" + right.slice(w[0], w[1]).join("") + "</div></div>" +
      "<div>&nbsp;</div><div>" + d("j/k move · enter select") + "</div>";
  }

  // overlayTabs / overlayHead — ports of view.go. Same tab chrome as topTabs and
  // bottomTabs, with two deliberate differences: the faces carry no number (they
  // have no numeric shortcut), and the inactive face is NOT dimmed — with , retired
  // it is the only signpost to Settings, and --dim is the lowest-contrast colour.
  function overlayTabs(onKeys) {
    return ["Keys", "Settings"].map(function (name, i) {
      var on = (i === 0) === !!onKeys;
      return '<span class="tab tab--face" data-on="' + (on ? 1 : 0) + '">' +
        esc(" " + name + " ") + "</span>";
    }).join('<span class="tabdiv">│</span>');
  }

  // The two lines both faces share. This REPLACES the old per-face title rather
  // than adding a row: the body already fills the box exactly at the smallest
  // supported size, so an extra line would clip the footer (view.go says the same).
  function overlayHead() {
    return "<div>" + overlayTabs(S.showKeys) + d("   tab · [ ] switch · esc close") +
      "</div><div>" + (lastEscape !== null ? yl("Press Esc again to quit") : "&nbsp;") + "</div>";
  }

  // The tab bar sits inside #term, which is rebuilt wholesale on every keystroke,
  // so a screen reader never hears it change. Mirror overlay state into the live
  // region that lives OUTSIDE #term — the same channel setStatus uses. This is
  // announce-only: it sets no visible status line, because the overlay already
  // shows the change on screen.
  function announce(s) { if (el.say) el.say.textContent = s; }

  // keysBody takes a row budget and windows to it, like the Go's keysBody: the
  // reference is 28 rows and a short terminal shows fewer, so j/k scrolls it.
  function keysBody(h) {
    var uni = S.glyphs === "unicode";
    var up = uni ? "↑" : "+", dn = uni ? "↓" : "-";
    function kr(k, t) { return '<div>  <span class="kcol">' + k + "</span>" + d(t) + "</div>"; }
    var left = [
      "<div>" + gp("Panels & navigation") + "</div>",
      kr("1/2/3", "focus Repos / Scripts / Branches"),
      kr("4", "PRs (beside Branches)"),
      kr("5/6/7", "bottom: Graph / Changes / Output"),
      kr("tab", "cycle panels"),
      kr("shift+tab", "cycle panels backwards"),
      kr("[ ]", "cycle the focused pane's tabs"),
      kr("z", "zoom the focused pane full-screen"),
      kr("j/k", "move in the focused panel"),
      kr("←/→", "hop between Repos and Branches"),
      kr("enter", "branches / checkout / run / checkout PR"),
      kr("g", "full-screen commit graph"),
      kr("n", "full-screen news feed (all headlines)"),
      kr("t", "toggle each repo's latest tag inline"),
      kr("F", "only changed / unsynced repos"),
      kr("/", "filter the focused list"),
      kr("!", "shell ($) in the > repo — stays open, esc leaves"),
      kr("esc", "back out one layer of state"),
      "<div>&nbsp;</div>",
      "<div>" + gp("GitHub PRs (4)") + d("   (needs gh)") + "</div>",
      kr("m", "toggle mine / review-requested"),
      kr("enter", "checkout the PR's branch in its repo"),
      "<div>&nbsp;</div>",
      "<div>" + gp("This screen") + "</div>",
      kr("?", "open / close this overlay"),
      kr("tab / [ ]", "keys <-> settings"),
      kr("j/k", "scroll this page"),
      kr("esc", "close this overlay"),
      kr("q", "quit manygit"),
      kr("esc esc", "quit within 500 ms; any other key resets")
    ];
    // The Go keeps "Graph -> Changes" at the foot of the LEFT column (view.go's
    // keysBody). It sits on the right here purely to balance the two columns: a
    // real terminal is as tall as you like, but this one is a fixed 498px (27
    // lines) below 768px, and a 28-line left column clipped its last row off.
    // Same rows, same order, same strings — only which column they land in.
    var right = [
      "<div>" + gp("Actions") + d(" on the > repo") + "</div>",
      kr("s", "sync (fetch + pull --ff-only)"),
      kr("p", "push"),
      kr("f/r", "fetch current / refetch all"),
      kr("b/enter", "checkout selected branch"),
      kr("d/D", "discard changes / +untracked (confirm)"),
      kr("o", "open the repo in your editor"),
      "<div>&nbsp;</div>",
      "<div>" + gp("Graph (5) -> Changes (6)") + "</div>",
      kr("5 j/k", "select a commit (WIP on top)"),
      kr("5 enter", "show its changed files"),
      kr("6 j/k", "pick a file"),
      kr("6 enter", "view its diff"),
      kr("esc", "back: diff / files / graph"),
      "<div>&nbsp;</div>",
      "<div>" + gp("Status column") + "</div>",
      kr(gr("ok"), "up to date with upstream"),
      kr(yl(up + "N"), "ahead — commits to PUSH"),
      kr(cy(dn + "N"), "behind — commits to PULL"),
      kr(mg(up + "N" + dn + "M"), "diverged"),
      kr(og("*N"), "N files changed (dirty)"),
      kr(d("~ ."), "fetching / loading"),
      kr(d("no-remote"), "local-only repo (no remote configured)"),
      kr(rd("!"), "branch has no upstream, or error"),
      // Last, as in view.go: the status legend keeps the first page.
      "<div>&nbsp;</div>",
      "<div>" + gp("Mouse") + d("   (? settings: on / off)") + "</div>",
      kr("click", "focus a pane, pick a row or tab"),
      kr("wheel", "j/k in the pane under the pointer"),
      kr("shift", "hold it to drag-select text")
    ];
    var n = Math.max(left.length, right.length);
    while (left.length < n) left.push("<div>&nbsp;</div>");
    while (right.length < n) right.push("<div>&nbsp;</div>");
    var avail = Math.max(1, h - 2); // the two head lines; settingsBody subtracts 4
    S.keysOffset = clamp(S.keysOffset, 0, Math.max(0, n - avail));
    var w = win(n, S.keysOffset + avail - 1, avail);
    return overlayHead() +
      '<div class="kcols"><div>' + left.slice(w[0], w[1]).join("") +
      "</div><div>" + right.slice(w[0], w[1]).join("") + "</div></div>";
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
      // helpView is an untitled full-screen panel (overlayBox), not a titledBox.
      // overlayBox centres the body block both ways — the block moves as a unit,
      // so the columns inside stay aligned with each other.
      //
      // Both faces are rendered and STACKED in one grid cell, with the inactive one
      // visibility:hidden so it still contributes its size. That makes the stack as
      // big as the larger face, which is this port's padBlock: the keys face is far
      // wider and taller than settings, and without it the centred block — tab bar
      // included — jumped every time you pressed tab.
      var h = rows("help", 20);
      renderOverlay(pane("", true,
        '<div class="overlay"><div class="overlay__stack">' +
        '<div class="overlay__face" data-on="' + (S.showKeys ? 1 : 0) + '">' + keysBody(h) + "</div>" +
        '<div class="overlay__face" data-on="' + (S.showKeys ? 0 : 1) + '">' + settingsBody(h) + "</div>" +
        "</div></div>",
        "help"));
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
    el.term.setAttribute("data-mouse", S.mouse); // site.css: pointer cursor on clickable bits
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
    // Go: graphView renders "(loading graph…)" until graphLines fills, rather
    // than an empty box. loadContext() only lands at 300ms, so this is reachable.
    if (!lines.length) {
      return pane(d("Graph: " + (r ? r.n : "(no repo)") + "  (j/k scroll, esc close)"), true,
        "<div>" + d("(loading graph…)") + "</div>", "gfull");
    }
    var start = clamp(S.graphOffset, 0, Math.max(0, lines.length - 1));
    var body = lines.slice(start, start + h).map(function (l) { return "<div>" + l + "</div>"; }).join("");
    return pane(d("Graph: " + (r ? r.n : "(no repo)") + "  (j/k scroll, esc close)"), true, body, "gfull");
  }
  // splitHeadline cuts a headline into its changelog heading and the explanation
  // after it, on the FIRST colon. No colon means all heading and no detail.
  // A leading colon is not a title, so that line is kept whole.
  function splitHeadline(h) {
    var i = h.indexOf(":");
    if (i <= 0) return [h.trim(), ""];
    return [h.slice(0, i).trim(), h.slice(i + 1).trim()];
  }

  // wrapWords breaks on spaces only, never mid-word — the Go can't use lipgloss
  // Width() here for the same reason (it hard-wraps mid-word). A single word
  // longer than w gets its own over-long line rather than being cut.
  function wrapWords(s, w) {
    var fields = String(s).split(/\s+/).filter(Boolean);
    if (!fields.length) return [];
    if (w <= 0) return [fields.join(" ")];
    var out = [], cur = fields[0];
    for (var i = 1; i < fields.length; i++) {
      if (cur.length + 1 + fields[i].length <= w) { cur += " " + fields[i]; continue; }
      out.push(cur); cur = fields[i];
    }
    out.push(cur);
    return out;
  }

  var NEWS_MEASURE = 76; // readable line length; the overlay is full-screen
  function newsColW() { return NEWS_MEASURE; }

  // newsLines renders the feed as a changelog: a numbered heading per entry, its
  // explanation wrapped and indented beneath, and a blank line between entries.
  // Ten headlines stacked with no gap read as one grey block. Returns flat lines
  // so j/k can scroll by RENDERED line, since an entry is several.
  function newsLines() {
    var colW = newsColW(), indent = "&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;", out = [];
    S.newsFeed.forEach(function (h, i) {
      if (i > 0) out.push("&nbsp;"); // the gap goes BETWEEN entries
      var parts = splitHeadline(h);
      var num = d(String(i + 1).padStart(2) + " ").replace(/ /g, "&nbsp;");
      wrapWords(parts[0], colW - 3).forEach(function (ln, j) {
        out.push((j === 0 ? num : "&nbsp;&nbsp;&nbsp;") + ti(ln));
      });
      wrapWords(parts[1], colW - 5).forEach(function (ln) {
        out.push(indent + d(ln));
      });
    });
    return out;
  }

  function newsOverlay(h) {
    var title = d("News — " + S.newsFeed.length + " headlines  (j/k scroll, esc close)");
    // Go: newsView has two empty-feed states, summarizing vs nothing to say. The
    // feed only lands at 2100ms of runInit, so `n` before then hits the first.
    if (!S.newsFeed.length) {
      var empty = S.newsLoading
        ? "(summarizing recent commits…)"
        : "(no main-branch activity in the last " + S.newsDays + " days, or no AI harness set)";
      return pane(title, true, "<div>" + d(empty) + "</div>", "nfull");
    }
    var lines = newsLines();
    var start = clamp(S.newsOffset, 0, Math.max(0, lines.length - 1));
    var body = lines.slice(start, start + h).map(function (l) { return "<div>" + l + "</div>"; }).join("");
    // lipgloss.Place centres the block as one unit; a max-width column with auto
    // margins is the browser's version of that.
    return pane(title, true, '<div class="newscol">' + body + "</div>", "nfull");
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

  // TOP_VIEWS / BOTTOM_VIEWS are the tab bars, in key order. `[` / `]` wrap on
  // their length, the way the Go wraps on topViewCount / bottomViewCount.
  var TOP_VIEWS = ["branches", "prs"];              // keys 3, 4
  var BOTTOM_VIEWS = ["graph", "changes", "output"]; // keys 5, 6, 7

  // setTopView focuses the top slot and shows v. The number keys, `[`/`]`, right
  // and enter-on-Repos all route through here so a tab can never be entered by
  // one route with side effects another route skips.
  function setTopView(v) {
    S.focus = "branches";
    if (v !== "prs") clearPRFilter(); // leaving the PRs sub-view drops its `/` filter
    S.topView = v;
    if (v === "prs") autoPickPRList();
  }

  // autoPickPRList points the PRs pane at whichever list has something in it:
  // your own PRs normally, review requests when yours are empty and reviews are
  // waiting. Opening onto an empty list while the other one has nine PRs in it
  // wastes the keypress. With nothing in either it stays on your own, so the
  // pane shows the more useful of the two empty messages.
  //
  // Runs both when the pane opens and when the lists land — they load async, so
  // `4` is normally pressed while both are still empty (runInit reproduces that
  // gap). Once the user has chosen with `m` it stops, so an explicit choice
  // outlives the `r` refresh.
  function autoPickPRList() {
    if (S.prChosen) return;
    var want = prMine().length === 0 && prReview().length > 0;
    if (want !== S.prShowReview) { S.prShowReview = want; S.prCursor = 0; }
  }

  // setBottomView: same, plus the bottom slot's own side effects — a PR needle is
  // meaningless down here, and Changes has to (re)load its files.
  function setBottomView(v) {
    S.focus = "bottom";
    clearPRFilter();
    S.bottomView = v;
    if (v === "changes") { S.changeShowDiff = false; loadChanges(); }
  }

  // cycleTab moves the focused pane's tab bar by delta, wrapping. Repos and
  // Scripts have no tab bar, so it is deliberately a no-op there rather than
  // jumping somewhere the user didn't ask for — `tab` stays pane-cycling
  // everywhere, which is why this lives on its own keys.
  function cycleTab(delta) {
    if (S.focus === "branches") {
      var t = TOP_VIEWS.indexOf(S.topView), n = TOP_VIEWS.length;
      setTopView(TOP_VIEWS[((t + delta) % n + n) % n]);
    } else if (S.focus === "bottom") {
      var b = BOTTOM_VIEWS.indexOf(S.bottomView), m = BOTTOM_VIEWS.length;
      setBottomView(BOTTOM_VIEWS[((b + delta) % m + m) % m]);
    }
  }

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

  // keepCursorOn re-points the cursor at path in the visible set, or clamps to
  // the top when it's gone, preserving the filter. Reloads the panes only when
  // the cursor lands on a different repo (else a moved-nothing reshuffle would
  // collapse an open diff).
  //
  // Port of Model.reclampCursor (update.go): the Go re-clamps in statusMsg, but
  // the demo mutates the fixture inline, so each mutating key calls this with the
  // path it was on. Without it, syncing/discarding the highlighted repo under `F`
  // or a branch `/needle` drops its row from under the cursor.
  function reclampCursor(was) {
    var vis = visibleRepos();
    if (S.cursor >= vis.length) S.cursor = vis.length - 1;
    if (S.cursor < 0) S.cursor = 0;
    var cur = curRepo();
    if (cur && cur.path !== was) loadContext();
  }

  // noLocalClone is the one message for "this PR's repo isn't among the repos
  // manygit scanned". enter and o fail for exactly that reason, so they say
  // exactly this, differing only in the verb. It names the tree rather than the
  // pane, because that is what has to change to fix it.
  function noLocalClone(slug, verb) {
    return slug + " isn't in this tree — no local clone to " + verb;
  }

  // openTarget resolves what `o` should open. In the PRs pane that is the
  // highlighted PR's local clone, not whatever the Repos cursor happens to be
  // on: you act on what you are looking at. Returns {path} when there is
  // something to open, {missing} when the PR has no local clone.
  function openTarget() {
    if (S.focus === "branches" && S.topView === "prs") {
      var prs = visiblePRs();
      if (S.prCursor < 0 || S.prCursor >= prs.length) return {};
      var pr = prs[S.prCursor], r = prRepo(pr);
      return r ? { path: r.path } : { missing: pr.repo };
    }
    var cr = curRepo();
    return cr ? { path: cr.path } : {};
  }

  // loadContextIfCurrent (update.go): panes 3/5/6 show the repo under the REPO
  // cursor, so they only go stale when the thing that changed is that repo.
  // Reloading for any other repo would spend 2-3 git subprocesses in the binary
  // redrawing content that is already correct.
  function loadContextIfCurrent(path) {
    if (!path) return;
    var r = curRepo();
    if (r && r.path === path) loadContext();
  }

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

  // takeOutputPane hands the Output pane to a new producer, superseding whatever
  // was writing there — a streaming script, a pending AI reply, or both.
  //
  // The Go has to bump TWO counters here: scriptOutMsg is stamped with outputRun
  // and the AI replies with aiRun, so bumping only one leaves the other's
  // messages still passing their staleness check and writing into a pane they no
  // longer own. This port only ever had the one counter, so it always had the
  // exclusive handover — it is a named function so the two stay greppable
  // together, per the "ported functions keep their Go names" rule.
  function takeOutputPane(title) {
    S.outputRun++;
    S.outputTitle = title;
    S.outputLines = [];
    S.outputOffset = 0;
    S.outputRunning = true;
    // Port of the Go's killRunning() call here: a new producer means the old one
    // is no longer running, so the "esc: cancel" hint must not linger onto it.
    S.shellRunning = false;
  }

  function runScript() {
    var vs = visibleScripts();
    if (S.scriptCursor < 0 || S.scriptCursor >= vs.length) return;
    takeOutputPane(vs[S.scriptCursor].name);
    var run = S.outputRun;
    S.focus = "bottom";
    S.bottomView = "output";
    var lines = ["$ " + S.outputTitle, ""].concat(SCRIPT_OUT[S.outputTitle] || SCRIPT_FALLBACK);
    var effects = (SCRIPT_EFFECTS[S.outputTitle] || []).slice();
    var i = 0;
    var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    (function step() {
      if (run !== S.outputRun) return; // superseded
      if (i >= lines.length) {
        S.outputRunning = false;
        // Everything the probe couldn't see lands now. restatAll() is the Go's
        // restatAllCmd: a local re-read of every repo, no fetch — so ending a
        // script can't spray git fetches at every remote.
        restatAll(effects);
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
        applyGitEffects(effects, lines[i]);
      }
      render();
      setTimeout(step, reduce ? 0 : 55);
    })();
  }

  // applyGitEffects is the repo-probe tick: the .git-level changes announced by
  // the line that just printed are applied now, so their rows update while the
  // script is still running instead of waiting for it to finish. Effects are
  // spliced out as they fire, so restatAll() at the end only has the rest left.
  //
  // reclampCursor is not optional here. In the Go every one of these refreshes
  // arrives as a statusMsg, and that handler re-clamps on each one because a new
  // status changes which repos are VISIBLE while the cursor is an index into
  // that list. Under `F`, sync-all.sh clearing `behind` on two repos drops them
  // both from the filtered list — without the re-clamp the cursor is left past
  // the end, curRepo() goes null, and the pane draws no cursor at all.
  function applyGitEffects(effects, line) {
    for (var i = effects.length - 1; i >= 0; i--) {
      var e = effects[i];
      if (!e.git || e.after !== line) continue;
      var r = discovered().filter(function (x) { return x.n === e.repo; })[0];
      if (r) {
        var was = curRepo() ? curRepo().path : "";
        e.apply(r);
        reclampCursor(was);
        loadContextIfCurrent(r.path);
      }
      effects.splice(i, 1);
    }
  }

  // restatAll re-reads every repo, applying whatever the probe couldn't see. The
  // binary spends ~170ms of concurrent local git here across ~28 repos — the
  // same work Init() does at launch, once, at the moment a script ends. Every
  // one of those reads is a statusMsg, so it re-clamps for the same reason
  // applyGitEffects does.
  function restatAll(effects) {
    var was = curRepo() ? curRepo().path : "";
    effects.forEach(function (e) {
      var r = discovered().filter(function (x) { return x.n === e.repo; })[0];
      if (r) e.apply(r);
    });
    effects.length = 0;
    reclampCursor(was);
    loadContext(); // the Go batches loadContextCmd here too
  }

  function checkoutSelected() {
    var vb = visibleBranches(), r = curRepo();
    if (!r || S.focus !== "branches" || S.branchCursor >= vb.length) return;
    if (stDirty(r) > 0) { setStatus(og("checkout skipped: dirty working tree")); return; }
    var name = vb[S.branchCursor].name.replace(/^origin\//, ""); // Branch.LocalName()
    r.b = name;
    loadContext(); // checkoutDoneMsg batches loadContextCmd — graph and Changes must follow
    // The branch is half of what `/` matches on (repoHaystack), so a checkout can
    // drop this very repo out of an active filter — `/main` then checking out
    // feat/x is exactly that. The Go re-clamps for free, because checkoutDoneMsg
    // batches statusCmd and the resulting statusMsg runs reclampCursor.
    reclampCursor(r.path);
    setStatus(gr("checked out " + name + " in " + r.n));
  }

  function checkoutPR() {
    var prs = visiblePRs();
    if (S.prCursor < 0 || S.prCursor >= prs.length) return;
    var pr = prs[S.prCursor];
    var target = prRepo(pr);
    if (!target) { setStatus(og(noLocalClone(pr.repo, "check out"))); return; }
    if (stDirty(target) > 0) { setStatus(og("checkout skipped: dirty working tree in " + target.n)); return; }
    // `gh pr checkout` lands on the PR's HEAD branch, so the row's (branch) now
    // matches pr.head and prCheckedOut() turns it green.
    var was = curRepo() ? curRepo().path : "";
    target.b = pr.head;
    // Deliberately nothing moves — no focus change, no cursor jump, no filter
    // reset. A PR spans several repos and you check out two or three in a row,
    // so landing on one repo's Branches pane after each enter would break the
    // walk. The row updates in place instead (Go: statusCmd + reclampCursor;
    // here the fixture mutation IS the status, so reclamp directly).
    reclampCursor(was);
    loadContextIfCurrent(target.path);
    setStatus(gr("checked out PR #" + pr.num + " in " + target.n));
  }

  function armDiscard(full) {
    var r = curRepo();
    if (!r) return;
    // Go: `if r.loaded && r.status.DirtyCount == 0`. The loaded conjunct matters —
    // on an unloaded repo we don't know yet, so the confirm arms rather than
    // refusing. Dropping it inverts the behaviour during boot.
    if (r.loaded && r.dirty === 0) { setStatus(esc("nothing to discard in " + r.n)); return; }
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

  /* -- `:` harness mode (canned) ------------------------------------------
     Port of internal/tui/update.go handleAIPromptKey + the aiPlanMsg/aiDoneMsg
     handlers, and internal/aigit's Complete/Validate/Execute. The ONE thing that
     is faked is the harness reply, because a browser has no AI and no git — the
     fourth intentional divergence, and the page says so on screen. The confirm,
     the refusal of a force-push, and stop-at-first-failure are all real logic. */

  // aiNames is the completion vocabulary: repo names, group folders, branches.
  function aiNames() {
    var out = [], seen = {};
    function add(n) { if (n && !seen[n]) { seen[n] = 1; out.push(n); } }
    REPOS.forEach(function (r) { add(r.n); });
    REPOS.forEach(function (r) { add(r.g); });
    REPOS.forEach(function (r) { add(r.b); });
    // Scripts complete WITH their @, like aigit.Context.Names does.
    SCRIPTS.forEach(function (sc) { add("@" + sc.name); });
    return out;
  }

  // Complete — port of aigit.Complete. Fills in as far as every candidate agrees
  // (the longest common prefix), the way a shell does, then waits for the
  // character that decides between them rather than picking one.
  function aiComplete(s) {
    var i = s.search(/[^ \t]*$/), word = s.slice(i);
    if (!word) return "";
    var lower = word.toLowerCase(), lcp = null;
    aiNames().forEach(function (n) {
      if (n.toLowerCase().indexOf(lower) !== 0) return;
      if (lcp === null) { lcp = n; return; }
      var j = 0;
      while (j < lcp.length && j < n.length && lcp[j] === n[j]) j++;
      lcp = lcp.slice(0, j);
    });
    if (lcp === null || lcp.length <= word.length) return "";
    return lcp.slice(word.length);
  }

  // ghostSettle mirrors the Go's: the suggestion stays hidden until typing stops,
  // so the line does not churn as it grows, shrinks and vanishes per keystroke.
  var ghostSettle = 120;

  function scheduleGhost() {
    S.aiGhost = false;
    var gen = ++S.aiGhostGen;
    setTimeout(function () {
      if (gen !== S.aiGhostGen || !S.aiPrompting) return; // superseded
      S.aiGhost = true;
      render();
    }, ghostSettle);
  }

  function aiPromptLine() {
    var lead = gp(S.harness + ":") + " " + esc(S.aiPrompt);
    if (!S.aiGhost) return lead + "_";
    var ghost = aiComplete(S.aiPrompt);
    // Cursor mark dropped while a suggestion is up, so the word reads whole
    // ("alpha") instead of split ("al_pha") — same as the Go.
    return ghost ? lead + d(esc(ghost)) : lead + "_";
  }

  // dropWord / historyStep — ports of the Go helpers of the same name.
  function dropWord(s) {
    s = s.replace(/[ \t]+$/, "");
    var i = Math.max(s.lastIndexOf(" "), s.lastIndexOf("\t"));
    return i >= 0 ? s.slice(0, i + 1) : "";
  }

  function historyStep(d) {
    if (!S.aiHistory.length) return;
    if (S.aiHistIdx === S.aiHistory.length) S.aiDraft = S.aiPrompt;
    var i = Math.min(Math.max(S.aiHistIdx + d, 0), S.aiHistory.length);
    S.aiHistIdx = i;
    S.aiPrompt = i === S.aiHistory.length ? S.aiDraft : S.aiHistory[i];
    scheduleGhost();
  }

  function handleAIPromptKey(k) {
    if (k === "Escape") { S.aiPrompting = false; S.aiPrompt = ""; S.aiGhost = false; return; }
    // tab completes whether or not the ghost is up: it is an explicit request.
    if (k === "Tab") { S.aiPrompt += aiComplete(S.aiPrompt); scheduleGhost(); return; }
    if (k === "Backspace") {
      // opt/cmd+backspace deletes a word, like the Go's alt+backspace.
      S.aiPrompt = altKey ? dropWord(S.aiPrompt) : S.aiPrompt.slice(0, -1);
      scheduleGhost(); return;
    }
    if (k === "ArrowUp") { historyStep(-1); return; }
    if (k === "ArrowDown") { historyStep(1); return; }
    if (k === "Enter") {
      var req = S.aiPrompt.trim();
      S.aiPrompting = false; S.aiPrompt = "";
      if (req) {
        if (!S.aiHistory.length || S.aiHistory[S.aiHistory.length - 1] !== req) S.aiHistory.push(req);
        S.aiHistIdx = S.aiHistory.length;
        askCannedHarness(req);
      }
      return;
    }
    // Unlike the filter, a space is kept — this is a sentence. (The Go handles
    // tea.KeySpace for the same reason; see handleAIPromptKey there.)
    if (k.length === 1) { S.aiPrompt += k; scheduleGhost(); }
  }

  // cannedPlan is the scripted stand-in for the harness. It recognises the shapes
  // the real thing handles and falls back to a decline, so the demo never claims
  // to understand something it doesn't.
  function cannedPlan(req) {
    var q = req.toLowerCase();
    var cur = REPOS[Math.min(S.cursor, REPOS.length - 1)] || REPOS[0];
    if (/mkdir|touch|ls |cd |rm |npm|install/.test(q)) {
      return { steps: [], note: "manygit only runs git, so I can't do that here." };
    }
    if (/force/.test(q)) { // deliberately refused downstream, to show the guard
      return { steps: [{ repo: cur.n, args: ["push", "--force"] }], note: "" };
    }
    var group = null;
    REPOS.forEach(function (r) { if (q.indexOf(r.g.toLowerCase()) >= 0) group = r.g; });
    var targets = group ? REPOS.filter(function (r) { return r.g === group; }) : [cur];
    // An @reference means "read this file and do the part I asked for". The demo
    // has no filesystem, so it recognises the reference and scopes to the group
    // named in the request — enough to show the shape without pretending to read.
    var ref = (req.match(/@([^\s]+)/) || [])[1];
    if (ref) {
      var picked = group ? targets : REPOS.filter(function (r) { return r.g === "apps"; });
      return {
        steps: picked.map(function (r) { return { repo: r.n, args: ["pull", "--ff-only"] }; }),
        note: "from " + ref + " — only the git steps; its npm parts were left out."
      };
    }
    if (/tag/.test(q)) {
      return { steps: targets.map(function (r) {
        return { repo: r.n, args: ["tag", "-a", "v1.2.0", "-m", "release v1.2.0"] };
      }), note: "" };
    }
    if (/rebase/.test(q)) {
      return { steps: targets.map(function (r) {
        return { repo: r.n, args: ["rebase", "origin/main"] };
      }), note: "" };
    }
    if (/merge/.test(q)) {
      return { steps: targets.map(function (r) {
        return { repo: r.n, args: ["merge", "origin/main"] };
      }), note: "" };
    }
    if (/sync|pull|fetch|update/.test(q)) {
      var steps = [];
      targets.forEach(function (r) {
        steps.push({ repo: r.n, args: ["fetch", "--quiet"] });
        steps.push({ repo: r.n, args: ["pull", "--ff-only"] });
      });
      return { steps: steps, note: "" };
    }
    return { steps: [], note: "this demo only scripts a few examples — try \"sync everything in apps\"." }; // a statement, never a question
  }

  // aiValidate — port of aigit.Validate, trimmed to the checks the demo can hit.
  function aiValidate(plan) {
    var bad = [];
    plan.steps.forEach(function (st) {
      var a = st.args;
      if (a[0] === "push" && a.some(function (x) { return /^(-f|--force|--force-with-lease)$/.test(x); })) {
        bad.push({ step: st, reason: "force-pushes, which rewrites history on the remote" });
      } else if (a[0] === "push" && a.some(function (x) { return /^(-d|--delete)$/.test(x) || x.charAt(0) === ":"; })) {
        bad.push({ step: st, reason: "deletes a remote ref" });
      }
    });
    return bad;
  }

  function askCannedHarness(req) {
    // No repo probe here: asking the harness a question runs nothing against the
    // repos, so there is nothing for the Repos pane to follow.
    takeOutputPane(S.harness + ": " + req);
    S.outputLines = [d("asking " + S.harness + "... (scripted in this demo)")];
    setBottomView("output");
    var run = S.outputRun;
    setTimeout(function () {
      if (run !== S.outputRun) return;
      var plan = cannedPlan(req);
      S.outputRunning = false;
      S.outputLines = [];
      if (plan.note) S.outputLines.push(d(plan.note));
      var bad = aiValidate(plan);
      if (bad.length) {
        S.outputLines.push(rd("refused — not run"));
        bad.forEach(function (b) {
          S.outputLines.push("  " + b.step.repo + "  git " + b.step.args.join(" "));
          S.outputLines.push("  " + d("↳ " + b.reason));
        });
        setStatus(d("plan refused")); render(); return;
      }
      if (!plan.steps.length) {
        if (!plan.note) S.outputLines.push(d("  nothing to do"));
        // No thread: an empty plan ends the exchange rather than pausing it.
        S.outputLines.push("");
        S.outputLines.push(d("  press : to ask again"));
        render(); return;
      }
      plan.steps.forEach(function (st) {
        S.outputLines.push("  " + gp(st.repo) + "  git " + esc(st.args.join(" ")));
      });
      S.outputLines.push("");
      S.outputLines.push(yl("run " + plan.steps.length + " command" + (plan.steps.length === 1 ? "" : "s") + "? [y/N]"));
      S.confirmPlan = true;
      S.aiPending = plan;
      announce("Plan ready. Press y to run, any other key to cancel.");
      render();
    }, 700);
  }

  function handlePlanConfirm(k) {
    var plan = S.aiPending;
    S.confirmPlan = false; S.aiPending = null;
    if (k !== "y") {
      S.outputLines.push(d("cancelled — nothing ran"));
      setStatus(d("plan cancelled"));
      return;
    }
    takeOutputPane("running " + plan.steps.length + " command" + (plan.steps.length === 1 ? "" : "s"));
    setBottomView("output");
    // Step through them on a timer so it reads like work happening, and stop at
    // the first failure exactly as aigit.Execute does.
    var run = S.outputRun, i = 0;
    (function step() {
      if (run !== S.outputRun) return;
      if (i >= plan.steps.length) {
        S.outputRunning = false;
        // aiDoneMsg re-reads every repo — "whatever ran changed the repos, so
        // re-read them rather than leaving the list showing pre-command branches
        // and counts". This is the same refetchAll the `r` key runs.
        refetchAll();
        setStatus(gr(plan.steps.length + " command" + (plan.steps.length === 1 ? "" : "s") + " ok"));
        render(); return;
      }
      var st = plan.steps[i++];
      S.outputLines.push("  " + gp(st.repo) + "  git " + esc(st.args.join(" ")) + "  " + gr("ok"));
      applyStepToRepo(st); // the repo probe: these are git commands, so pane 1 follows them live
      render();
      setTimeout(step, 260);
    })();
  }

  // applyStepToRepo is this port's repo probe. In the binary an AI plan runs real
  // git, so .git changes, git.Fingerprint notices, and that repo's row is
  // re-stat'd while the plan is still running. Here the commands are simulated,
  // so the fixture is moved by hand to whatever the command would really have
  // done — only for the verbs cannedPlan actually emits. reclampCursor for the
  // same reason applyGitEffects needs it: changing a repo's counts can drop it
  // out of the `F` filter, and the cursor is an index into that list.
  function applyStepToRepo(st) {
    var r = discovered().filter(function (x) { return x.n === st.repo; })[0];
    if (!r) return;
    var was = curRepo() ? curRepo().path : "";
    var verb = st.args[0];
    if (verb === "pull" || verb === "merge" || verb === "rebase") r.behind = 0;
    else if (verb === "tag") {
      var name = st.args.filter(function (a) { return /^v\d/.test(a); })[0];
      if (name) r.tag = name;
    } else return; // fetch --quiet moves nothing the row shows
    reclampCursor(was);
    loadContextIfCurrent(r.path);
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
    else { S.cursor = 0; contextCmd(); } // typing re-picks the repo per keystroke
  }

  // The demo's `q` divergence: the Go quits from here, the browser can't. One
  // string, used in every state the Go binds q — top level, the graph and news
  // overlays, and the ? overlay — so the key never just swallows a press.
  var QUIT_HINT = "q quits manygit — this is a browser demo, so it stays";

  function handleSettingsKey(k) {
    if (S.editingOpenCmd) {
      if (k === "Escape") S.editingOpenCmd = false;
      else if (k === "Enter") { S.openCmd = S.openCmdBuf.trim(); S.editingOpenCmd = false; }
      else if (k === "Backspace") S.openCmdBuf = S.openCmdBuf.slice(0, -1);
      else if (k.length === 1) S.openCmdBuf += k;
      return;
    }
    var rows = settingRows();
    // ? is the door in AND out — it closes from either face, exactly like esc. Both
    // owe the theme-preview cleanup: j/k on the settings face applies themes live,
    // and in a browser a leaked preview sticks to documentElement site-wide.
    // tab / shift+tab / [ / ] all flip the face (e.key is "Tab" for shift+tab too,
    // and with two faces forward and backward land in the same place). Flipping
    // INTO settings parks the cursor on the committed theme — that seeding was the
    // , key's job and has nowhere else to live.
    if (k === "q") setStatus(d(QUIT_HINT)); // Go: quits from the overlay
    else if (k === "Tab" || k === "[" || k === "]") {
      S.showKeys = !S.showKeys;
      if (!S.showKeys) S.settingsCursor = Math.max(0, THEMES.indexOf(S.theme));
      announce(S.showKeys ? "Keys" : "Settings");
    }
    else if (k === "?" || k === "Escape") {
      applyTheme(S.theme); S.showHelp = false;
      announce("Help closed");
    }
    // j/k drives whichever face is showing: the settings cursor, or the keys face's
    // scroll (it is taller than the terminal, so its last rows need reaching).
    else if (k === "j" || k === "ArrowDown") {
      if (S.showKeys) S.keysOffset++; // keysBody clamps against its own row count
      else { S.settingsCursor = clamp(S.settingsCursor + 1, 0, rows.length - 1); previewSettings(); }
    } else if (k === "k" || k === "ArrowUp") {
      if (S.showKeys) S.keysOffset = Math.max(0, S.keysOffset - 1);
      else { S.settingsCursor = clamp(S.settingsCursor - 1, 0, rows.length - 1); previewSettings(); }
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
      else if (r.kind === SK_MOUSE) S.mouse = r.val; // live, like tea.DisableMouse
      else { S.editingOpenCmd = true; S.openCmdBuf = S.openCmd; }
    }
  }

  /* -- `!` shell mode (canned) --------------------------------------------
     Port of internal/tui/update.go handleShellPromptKey + runShellLine. The ONE
     thing that is faked is the command's output, because a browser has no bash
     — the fifth intentional divergence, and the page says so on screen. The repo
     binding, the input, the history, the echo, the streaming and esc are real. */

  // The fixed part of the `!` prompt — the app naming itself, the way a shell
  // prompt names the host before the path. Verbatim from view.go.
  var SHELL_PROMPT_LEAD = "$manygit:";

  function cannedShell(cmd, repo) {
    var c = cmd.trim();
    if (/^git\s+status/.test(c)) return ["## main...origin/main", " M internal/tui/update.go", "?? notes.txt"];
    if (/^git\s+log/.test(c)) return ["a1b2c3d  wire the shell pane", "d4e5f6a  fix the tab bar"];
    if (/^git\s+branch/.test(c)) return ["* main", "  feat/shell"];
    if (/^ls(\s|$)/.test(c)) return ["README.md", "go.mod", "internal", "main.go"];
    if (/^pwd$/.test(c)) return ["~/code/" + repo];
    if (/^echo\s+/.test(c)) return [c.replace(/^echo\s+/, "")];
    return null;
  }

  // shellLocation / trimLeftTo / shellLocBudget — ports of the Go helpers of the
  // same names. "(root)" is a Repos-pane label, not a path segment.
  function shellLocation(r) {
    return !r.g || r.g === "(root)" ? r.n : r.g + "/" + r.n;
  }
  function trimLeftTo(s, max) {
    if (max < 1) return "";
    if (s.length <= max) return s;
    if (max === 1) return "…";
    return "…" + s.slice(s.length - (max - 1));
  }
  // The Go derives this from the terminal's column count, halving what is left
  // after the right-hand indicators. This terminal is a CSS box that reflows, so
  // there is no column count to halve — it uses the Go's upper cap, which is what
  // a normal-width terminal lands on anyway. Every fixture path is well under it,
  // so the demo shows the same untrimmed prompt a real 100-col terminal shows.
  // trimLeftTo above is still the real logic, ported, and is what would fire.
  function shellLocBudget() {
    return 40;
  }
  function shellPromptLine() {
    var loc = trimLeftTo(S.shellLoc, shellLocBudget());
    return gp(SHELL_PROMPT_LEAD + loc) + " " + esc(S.shellCmd) + "_";
  }

  // Port of the Go's shellHistoryStep: clamped, no wraparound, and the draft is
  // stashed on the way in so up-then-down is a round trip.
  function shellHistoryStep(n) {
    if (!S.shellHistory.length) return;
    if (S.shellHistIdx === S.shellHistory.length) S.shellDraft = S.shellCmd;
    var i = Math.min(Math.max(S.shellHistIdx + n, 0), S.shellHistory.length);
    S.shellHistIdx = i;
    S.shellCmd = i === S.shellHistory.length ? S.shellDraft : S.shellHistory[i];
  }

  function handleShellPromptKey(k) {
    // esc leaves the shell and nothing else: a command already running keeps
    // running. Focus lands on Repos (pane 1), like the Go.
    if (k === "Escape") { S.shellPrompting = false; S.shellCmd = ""; S.focus = "repos"; return; }
    if (k === "Enter") { runShellLine(); return; }
    if (k === "Backspace") { S.shellCmd = S.shellCmd.slice(0, -1); return; }
    if (k === "ArrowUp") { shellHistoryStep(-1); return; }
    if (k === "ArrowDown") { shellHistoryStep(1); return; }
    if (k.length === 1) S.shellCmd += k;
  }

  function runShellLine() {
    var line = S.shellCmd.trim(), repo = S.shellLoc;
    // The prompt STAYS OPEN: a shell you sit in, not a one-shot. Only esc leaves.
    S.shellCmd = "";
    if (!line) return;
    if (!S.shellHistory.length || S.shellHistory[S.shellHistory.length - 1] !== line) {
      S.shellHistory.push(line);
    }
    S.shellHistIdx = S.shellHistory.length;

    var echo = repo + " $ " + line;
    takeOutputPane(echo);
    S.shellRunning = true;
    S.focus = "bottom";
    S.bottomView = "output";
    var run = S.outputRun;
    var body = cannedShell(line, repo);
    // RAW strings, never HTML: renderOutput colours by regex on the raw line and
    // escapes everything else, so markup pushed here would render literally.
    var lines = [echo].concat(
      body ? body : ["this demo has no shell — try git status, ls, pwd or echo"]
    );
    var i = 0;
    var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    (function step() {
      if (run !== S.outputRun) return; // superseded
      if (i >= lines.length) {
        S.outputRunning = false;
        S.shellRunning = false;
        setStatus(body ? gr(repo + ": exited 0") : d(repo + ": nothing ran — the shell is faked here"));
        render();
        return;
      }
      var n = reduce ? lines.length : 1;
      for (var j = 0; j < n && i < lines.length; j++, i++) {
        var atBottom = S.outputOffset >= S.outputLines.length - 1;
        S.outputLines.push(lines[i]);
        if (atBottom) S.outputOffset = S.outputLines.length - 1;
      }
      render();
      setTimeout(step, reduce ? 0 : 55);
    })();
  }

  var lastEscape = null;
  function handleKey(k) {
    var now = performance.now();
    if (k === "Escape") {
      if (lastEscape !== null && now - lastEscape <= 500) {
        lastEscape = null;
        setStatus(d("esc esc quits manygit — this is a browser demo, so it stays"));
        el.term.blur();
        return;
      }
      lastEscape = now;
      setTimeout(function () {
        if (lastEscape === now) { lastEscape = null; render(); }
      }, 500);
    } else {
      lastEscape = null;
    }
    if (S.shellPrompting) { handleShellPromptKey(k); return; }
    if (S.aiPrompting) { handleAIPromptKey(k); return; }
    if (S.confirmPlan) { handlePlanConfirm(k); return; }
    if (S.filtering) { handleFilterKey(k); return; }
    if (S.showHelp) { handleSettingsKey(k); return; }
    if (S.confirmDiscard) {
      S.confirmDiscard = false;
      if (k === "y") {
        var r = discovered().filter(function (x) { return x.n === S.confirmName; })[0];
        if (r) { r.dirty = 0; delete WIP_FILES[r.n]; loadChanges(); reclampCursor(r.path); }
        setStatus(gr("discarded " + (S.confirmFull ? "all changes" : "tracked changes") + " in " + S.confirmName));
      } else setStatus(esc("discard cancelled"));
      return;
    }
    if (S.showGraph) {
      if (k === "g" || k === "Escape") S.showGraph = false;
      else if (k === "j" || k === "ArrowDown") S.graphOffset = Math.min(S.graphOffset + 1, graph.length - 1);
      else if (k === "k" || k === "ArrowUp") S.graphOffset = Math.max(0, S.graphOffset - 1);
      else if (k === "q") setStatus(d(QUIT_HINT));
      return;
    }
    if (S.showNews) {
      if (k === "n" || k === "Escape") S.showNews = false;
      // By RENDERED line, not by headline: an entry is a heading plus a wrapped
      // explanation plus a gap, so clamping to newsFeed.length stops j short.
      else if (k === "j" || k === "ArrowDown") S.newsOffset = Math.min(S.newsOffset + 1, newsLines().length - 1);
      else if (k === "k" || k === "ArrowUp") S.newsOffset = Math.max(0, S.newsOffset - 1);
      else if (k === "q") setStatus(d(QUIT_HINT));
      return;
    }

    switch (k) {
      case "q":
        setStatus(d(QUIT_HINT)); break;
      case "?":
        // ? is the universal "show me the keys" reflex, so it lands on the
        // keybindings. Settings is the overlay's other face, reached from inside
        // with tab / shift+tab / [ / ] — it has no key of its own.
        S.showHelp = true; S.showKeys = true;
        announce("Help open — Keys. tab or bracket keys switch to Settings.");
        break;
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
      case "3": setTopView("branches"); break;
      case "4": setTopView("prs"); break;
      case "5": setBottomView("graph"); break;
      case "6": setBottomView("changes"); break;
      case "7": setBottomView("output"); break;
      case "]": cycleTab(1); break;
      case "[": cycleTab(-1); break;
      case "Tab": {
        var order = ["repos", "scripts", "branches", "bottom"];
        var at = order.indexOf(S.focus);
        // Go: `case "tab"` forward, `case "shift+tab"` back. The + order.length
        // keeps the modulo positive when wrapping past the first panel — the same
        // reason the Go writes (m.focus - 1 + panelCount) % panelCount.
        S.focus = shiftTab
          ? order[(at - 1 + order.length) % order.length]
          : order[(at + 1) % order.length];
        break;
      }
      case "ArrowRight":
        if (S.focus === "repos") { setTopView("branches"); S.branchCursor = 0; }
        break;
      case "ArrowLeft":
        if (S.focus === "branches") S.focus = "repos";
        break;
      case "j": case "ArrowDown":
        if (S.focus === "repos") {
          if (S.cursor < visibleRepos().length - 1) { S.cursor++; clearBranchFilter(); contextCmd(); }
        } else if (S.focus === "branches") topScroll(1);
        else if (S.focus === "scripts") {
          if (S.scriptCursor < visibleScripts().length - 1) S.scriptCursor++;
        } else bottomScroll(1);
        break;
      case "k": case "ArrowUp":
        if (S.focus === "repos") {
          if (S.cursor > 0) { S.cursor--; clearBranchFilter(); contextCmd(); }
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
        if (S.focus === "repos") { setTopView("branches"); S.branchCursor = 0; break; }
        if (S.focus === "scripts") { runScript(); return; }
        if (S.focus === "branches" && S.topView === "prs") { checkoutPR(); break; }
        checkoutSelected();
        break;
      case "b": checkoutSelected(); break;
      case "m":
        // prChosen: an explicit pick, so autoPickPRList stops overriding it.
        if (S.focus === "branches" && S.topView === "prs") { S.prShowReview = !S.prShowReview; S.prChosen = true; S.prCursor = 0; }
        break;
      case "Escape": {
        // esc backs out of exactly ONE layer per press, innermost first, so it
        // never yanks you further than you meant. Order is the visual nesting:
        // the diff sits inside Changes, Changes inside the zoomed pane, and the
        // filters are the outermost thing shaping what you're looking at.
        var escRepo = curRepo();
        var escPath = escRepo ? escRepo.path : "";
        if (S.shellRunning && !S.shellPrompting && S.focus === "bottom" && S.bottomView === "output") {
          // Scoped to the Output pane, like the Go: esc's job everywhere else is
          // to back out a layer, and killing a background command from inside a
          // diff would be a nasty surprise.
          S.outputRun++; // supersede the streaming timer
          S.outputRunning = false;
          S.shellRunning = false;
          S.outputLines.push("— cancelled —"); // raw: renderOutput escapes markup
          setStatus(og(S.shellLoc + ": cancelled"));
        } else if (S.focus === "bottom" && S.bottomView === "changes" && S.changeShowDiff) {
          S.changeShowDiff = false;
        } else if (S.focus === "bottom" && S.bottomView === "changes") {
          S.bottomView = "graph";
        } else if (S.zoomed) {
          S.zoomed = false;
        } else if (S.filter) {
          // land back on the repo you filtered your way to, not the top of the
          // widened list
          S.filter = "";
          S.filterPanel = "repos";
          S.branchCursor = 0;
          S.prCursor = 0;
          S.scriptCursor = 0;
          keepCursorOn(escPath);
        } else if (S.filterAttention) {
          S.filterAttention = false;
          keepCursorOn(escPath);
        }
        break;
      }
      case "o": {
        // Divergence #2: the Go spawns an editor, a browser can't — so this says
        // what it WOULD run. The target is resolved exactly as the Go resolves
        // it, so the repo it names is the right one.
        var t = openTarget();
        if (t.missing) setStatus(og(noLocalClone(t.missing, "open")));
        else if (t.path) setStatus(d("o runs `" + S.openCmd + " " + t.path + "` — nothing to open from a browser"));
        break;
      }
      case "F": S.filterAttention = !S.filterAttention; S.cursor = 0; loadContext(); break;
      case "!": {
        // `!` is the key (vim's shell-escape convention); what it opens renders as
        // `<repo> $`, matching the `<repo> $ <cmd>` line the pane echoes back.
        // Bound to the cursor repo up front and shown in the prompt, so there is
        // never a question which folder the command lands in.
        var sr = curRepo();
        if (!sr) { setStatus(d("no repo selected")); break; }
        S.shellPrompting = true; S.shellCmd = ""; S.shellLoc = shellLocation(sr);
        S.shellHistIdx = S.shellHistory.length;
        announce("Shell open in " + shellLocation(sr) + ". Type a command, enter runs it, esc leaves.");
        break;
      }
      case ":":
        // Global, not pane-scoped: the request names its own scope.
        S.aiPrompting = true; S.aiPrompt = ""; S.aiGhost = false;
        announce("AI prompt open. Type a git request, tab completes, enter sends.");
        break;
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
        // Guard order is the Go's (update.go `case "s"`): not-loaded first, then
        // no-remote, then dirty. Until status lands we don't know if there's a
        // remote, so skipping beats syncing blind.
        if (!rs.loaded) setStatus(og("sync " + rs.n + " skipped: status not loaded yet"));
        else if (!rs.remote) setStatus(og("sync " + rs.n + " skipped: no remote"));
        else if (rs.dirty > 0) setStatus(og("sync " + rs.n + " skipped: dirty working tree"));
        else { rs.behind = 0; setStatus(gr("synced " + rs.n)); reclampCursor(rs.path); }
        break;
      }
      case "p": {
        var rp = curRepo();
        if (!rp) break;
        if (!rp.loaded) setStatus(og("push " + rp.n + " skipped: status not loaded yet"));
        else if (!rp.remote) setStatus(og("push " + rp.n + " skipped: no remote"));
        else { rp.ahead = 0; setStatus(gr("pushed " + rp.n)); reclampCursor(rp.path); }
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
    " ": 1, "/": 1, ":": 1, Tab: 1, Enter: 1, ArrowUp: 1, ArrowDown: 1, ArrowLeft: 1, ArrowRight: 1,
    Backspace: 1, Escape: 1
  };

  // shiftTab is read by handleKey's Tab case. It is a module-level flag rather
  // than a parameter so handleKey keeps the single-string signature the keypad
  // buttons and the tests both call it with.
  var shiftTab = false;
  var altKey = false;

  /* -- mouse (mouse.go) ------------------------------------------------------
     A port of handleMouse / clickTab / clickRow / clickPR / clickBottom. The Go
     turns a screen cell into a pane with hitTest; here the DOM already knows which
     pane was hit, so only the ROW is arithmetic — the pixel offset into the pane
     body over LINE_H, the same rows-per-pane figure render() measures. From there
     it is the Go's logic: re-derive the renderer's window() and pick the line.

     Navigation only, as in the binary: a click never checks out, syncs, pushes,
     discards or runs. */

  var PID_FOCUS = { repos: "repos", scripts: "scripts", top: "branches", bottom: "bottom" };

  function mouseBlocked() {
    return S.filtering || S.shellPrompting || S.aiPrompting ||
      S.confirmPlan || S.confirmDiscard || S.editingOpenCmd;
  }

  // handleMouse's front half: which pane, and which line of it. Returns null for
  // anything that isn't a pane (the bars, the gap between the columns).
  function paneHit(target, clientY) {
    var pane = target.closest && target.closest("[data-pid]");
    if (!pane) return null;
    var pid = pane.getAttribute("data-pid");
    var focus = pid === "zoom" ? S.focus : PID_FOCUS[pid];
    if (!focus) return null;
    var body = pane.querySelector("[data-pane]");
    var tab = target.closest(".tab[data-tab]");
    var row = -1; // -1 = the title border, where the tab bar sits
    if (!tab && body) {
      row = Math.floor((clientY - body.getBoundingClientRect().top) / LINE_H);
      if (row < 0) row = -1;
    }
    return { focus: focus, row: row, tab: tab ? parseInt(tab.getAttribute("data-tab"), 10) : -1, inner: rows(pid, 1) };
  }

  function clickTab(h) {
    if (h.tab < 0) return;
    if (h.focus === "branches") setTopView(h.tab === 1 ? "prs" : "branches");
    else if (h.focus === "bottom") setBottomView(["graph", "changes", "output"][h.tab]);
  }

  function clickRow(h) {
    if (h.focus === "repos") {
      var vis = visibleRepos();
      var rl = repoLines(vis, S.cursor);
      var w = win(rl.lines.length, rl.cursorLine, Math.max(1, h.inner));
      var i = w[0] + h.row;
      if (i >= w[1] || rl.lines[i].header || rl.lines[i].repo === S.cursor) return;
      S.cursor = rl.lines[i].repo;
      clearBranchFilter(); // same as j/k: the branch filter belonged to the old repo
      contextCmd();
    } else if (h.focus === "scripts") {
      var ws = win(visibleScripts().length, S.scriptCursor, h.inner);
      if (ws[0] + h.row < ws[1]) S.scriptCursor = ws[0] + h.row; // select only — enter runs
    } else if (h.focus === "branches") {
      if (S.topView === "prs") { clickPR(h); return; }
      var wb = win(visibleBranches().length, S.branchCursor, h.inner);
      if (wb[0] + h.row < wb[1]) S.branchCursor = wb[0] + h.row; // select only — enter checks out
    } else if (h.focus === "bottom") {
      clickBottom(h);
    }
  }

  // clickPR mirrors renderPRs: a header, a spacer, then rows of PR_ROW_LINES
  // lines with PR_ROW_GAP blanks between them.
  function clickPR(h) {
    var prs = visiblePRs();
    if (!S.ghAvailable || !prs.length || h.row < 2) return;
    var w = win(prs.length, S.prCursor, prRowsThatFit(Math.max(1, h.inner - 2)));
    var r = h.row - 2;
    if (r % (PR_ROW_LINES + PR_ROW_GAP) >= PR_ROW_LINES) return; // the gap between two PRs
    var i = w[0] + Math.floor(r / (PR_ROW_LINES + PR_ROW_GAP));
    if (i < w[1]) S.prCursor = i;
  }

  // Graph and Changes are picked from; the diff and Output are only read, so a
  // click there just focuses (the wheel scrolls them).
  function clickBottom(h) {
    if (S.bottomView === "graph") {
      var commits = graphCommits();
      var selIdx = 0;
      if (S.graphSel >= 1 && commits[S.graphSel - 1]) selIdx = graph.indexOf(commits[S.graphSel - 1]) + 1;
      var w = win(1 + graph.length, selIdx, h.inner);
      var i = w[0] + h.row;
      if (i >= w[1]) return;
      if (i === 0) { S.graphSel = 0; return; }
      var g = graph[i - 1];
      if (g.hash) S.graphSel = commits.indexOf(g) + 1; // connector art isn't a commit
    } else if (S.bottomView === "changes" && !S.changeShowDiff && changeFiles.length) {
      var wc = win(changeFiles.length, S.changeCursor, h.inner);
      if (wc[0] + h.row < wc[1]) S.changeCursor = wc[0] + h.row; // select only — enter opens the diff
    }
  }

  function handleClick(e) {
    if (S.mouse !== "on" || mouseBlocked()) return;
    if (e.target.closest && e.target.closest("a")) return; // the @author links stay links
    // Full-screen overlays: nothing in them to point at.
    if (S.showGraph || S.showNews || S.showHelp) return;
    var h = paneHit(e.target, e.clientY);
    if (!h) return;
    runInit();
    S.focus = h.focus; // what `tab` does: focus only, no view change
    if (h.row < 0) clickTab(h);
    else clickRow(h);
    render();
  }

  // The wheel is j/k for the pane under the pointer, and scrolls the overlays the
  // way j/k does — except the settings face, where j/k previews themes live.
  // Trackpads send many small deltas, so they add up to whole lines first.
  var wheelAcc = 0;
  function handleWheel(e) {
    if (document.activeElement !== el.term) return; // see the note in boot()
    if (S.mouse !== "on" || mouseBlocked()) return;
    e.preventDefault();
    wheelAcc += e.deltaMode === 1 ? e.deltaY * LINE_H : e.deltaY;
    var steps = Math.trunc(wheelAcc / LINE_H);
    if (!steps) return;
    wheelAcc -= steps * LINE_H;
    if (S.showHelp && !S.showKeys) return;
    if (!(S.showGraph || S.showNews || S.showHelp)) {
      var h = paneHit(e.target, e.clientY);
      if (!h) return;
      S.focus = h.focus;
    }
    var k = steps > 0 ? "ArrowDown" : "ArrowUp";
    for (var n = Math.abs(steps); n > 0; n--) handleKey(k);
    render();
  }

  // idleEscape reports whether esc has nothing to do in the TUI right now. The
  // demo keeps a single idle esc as its accessible keyboard exit. A second
  // rapid esc after backing out of a layer mirrors the tool's quit shortcut.
  function idleEscape() {
    if (S.filtering || S.showHelp || S.showGraph || S.showNews || S.confirmDiscard ||
        S.shellPrompting || S.aiPrompting || S.confirmPlan) return false;
    if (S.outputRunning && S.focus === "bottom" && S.bottomView === "output") return false;
    if (S.focus === "bottom" && S.bottomView === "changes") return false;
    // esc now peels zoom and the filters too — it may only release the keyboard
    // once there is genuinely nothing left in the TUI for it to undo.
    if (S.zoomed || S.filter || S.filterAttention) return false;
    return true;
  }

  function onKey(e) {
    if (e.ctrlKey || e.metaKey || e.altKey) { lastEscape = null; return; }

    // THE ESCAPE HATCH. This widget swallows tab AND shift+tab (both cycle panes
    // in manygit), so without a way out a keyboard user is trapped — WCAG 2.1.2.
    // esc is the exit, but only when it would otherwise do nothing: inside the
    // Changes pane, or any overlay/filter/confirm, it keeps its real meaning and
    // the user can press it again once those are closed.
    if (e.key === "Escape" && (lastEscape === null || performance.now() - lastEscape > 500) && idleEscape()) {
      lastEscape = null;
      e.preventDefault();
      el.term.blur();
      return;
    }

    shiftTab = e.key === "Tab" && e.shiftKey;
    // altKey/metaKey ride alongside like shiftTab does: handleKey keeps its
    // single-string signature so the on-screen keypad can call it too.
    altKey = e.altKey || e.metaKey;
    var k = e.key;
    if (SWALLOW[k] || k.length === 1) e.preventDefault();
    handleKey(k);
    shiftTab = false;
    render();
  }

  // The ground is the SITE's, not manygit's: the tool has no background setting —
  // it inherits your terminal's (theme.go). So this is its own axis from the
  // demo's `?` theme picker, and the two compose. The <head> script has already
  // applied the stored/OS choice before first paint; this only wires the toggle.
  // One controller for every ground toggle on the page: the masthead chip on
  // desktop and the mobile gate's own corner toggle (the masthead is hidden on a
  // phone, and the toggle went with it). They share data-mode + localStorage, so
  // flipping either — or resizing across the breakpoint — leaves both in step.
  // The label of each is its own destination ("terminal" vs "background").
  function wireMode() {
    var btns = document.querySelectorAll("[data-mode-toggle]");
    if (!btns.length) return;
    var meta = document.querySelector('meta[name="theme-color"]');
    var root = document.documentElement;
    function paint() {
      var light = root.getAttribute("data-mode") === "light";
      Array.prototype.forEach.call(btns, function (b) {
        b.textContent = light ? "dark" : "light"; // the label is what you'd get
        b.setAttribute(
          "aria-label",
          "Switch to a " + (light ? "dark" : "light") + " " + (b.getAttribute("data-mode-toggle") || "terminal")
        );
      });
      if (meta) meta.setAttribute("content", light ? "#eeede8" : "#0b0b0c");
    }
    paint();
    Array.prototype.forEach.call(btns, function (b) {
      b.addEventListener("click", function () {
        var next = root.getAttribute("data-mode") === "light" ? "dark" : "light";
        root.setAttribute("data-mode", next);
        try { localStorage.setItem("manygit.mode", next); } catch (e) {}
        paint();
      });
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

    render(); // pre-Init: names + dots only. runInit() fills the rest on first focus.

    el.term.addEventListener("keydown", onKey);
    el.term.addEventListener("focus", function () { runInit(); render(); });
    el.term.addEventListener("blur", render);
    window.addEventListener("resize", render);

    // The mouse. Clicks work from the first one (it also focuses the terminal,
    // like clicking into a terminal window). The wheel only once the terminal
    // has focus: a real terminal owns every wheel event over it, but here the
    // widget sits in a page, and grabbing the wheel of someone scrolling PAST the
    // demo would trap them in it — the same reason esc releases the keyboard.
    el.term.addEventListener("click", handleClick);
    el.term.addEventListener("wheel", handleWheel, { passive: false });

    // The on-screen keypad runs the same handler, so touch works too. Only a
    // real pointer click pulls focus into the terminal (detail > 0) — a keyboard
    // user activating the button with enter keeps their place in the tab order.
    Array.prototype.forEach.call(document.querySelectorAll("[data-key]"), function (b) {
      b.addEventListener("click", function (e) {
        if (e.detail > 0) el.term.focus();
        runInit(); // a keyboard-activated button never fires the term's focus event
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
