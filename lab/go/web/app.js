const $ = (id) => document.getElementById(id);
let timer;
let stream,
  taskRefresh,
  taskBusy = false,
  taskAgain = false;
let selected = sessionStorage.getItem("plant-selected") || "";
let currentRun = "",
  renderedDetail = "",
  renderedBoard = "";
let pending = JSON.parse(sessionStorage.getItem("plant-pending") || "null");
const number = (n, digits = 0) =>
  n == null
    ? "—"
    : n.toLocaleString(undefined, { maximumFractionDigits: digits });
const el = (tag, text, cls) => {
  const e = document.createElement(tag);
  if (text != null) e.textContent = text;
  if (cls) e.className = cls;
  return e;
};
async function api(path, body) {
  const response = await fetch(
    path,
    body
      ? {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        }
      : {},
  );
  if (!response.ok) {
    if (response.status === 401 && path !== "/api/login") showLogin();
    const error = new Error(await response.text());
    error.status = response.status;
    throw error;
  }
  return response.json();
}
function showLogin() {
  clearTimeout(timer);
  clearTimeout(taskRefresh);
  stream?.close();
  stream = null;
  $("console").hidden = true;
  $("login").hidden = false;
  $("connection").textContent = "Session required";
}
function nodeCard(n) {
  const card = el("div", null, "node " + n.state);
  const top = el("div", null, "node-top");
  top.append(el("span", n.role), el("span", n.state, "badge " + n.state));
  const rate = el("div", number(n.rate_per_second), "node-rate");
  rate.append(el("small", " msg/s"));
  card.append(top, el("div", n.id, "node-name"), rate);
  const d = n.delivery;
  let note =
    n.observation_age_seconds < 0
      ? "No source observation"
      : "Observed " + number(n.observation_age_seconds, 1) + "s ago";
  if (d && n.state !== "stale") {
    if (n.role === "relay")
      note += " · queue " + number(Math.max(...d.queues.map((q) => q.depth)));
    if (n.role === "subscriber") note += " · pending " + d.pending;
  }
  card.append(el("div", note, "node-note"));
  return card;
}
async function refresh() {
  try {
    const s = await api("/api/state");
    $("login").hidden = true;
    $("console").hidden = false;
    currentRun = s.run_id;
    connectTasks();
    refreshTasks();
    $("connection").textContent =
      "● Connected · " + new Date(s.at).toLocaleTimeString();
    document.querySelector(".live-label").textContent = "● LIVE";
    document.querySelector(".live-label").className = "live-label";
    $("fresh").textContent =
      s.nodes.filter(
        (n) => n.observation_age_seconds >= 0 && n.observation_age_seconds <= 3,
      ).length + " / 8";
    const subscribers = s.nodes.filter((n) => n.role === "subscriber");
    $("throughput").textContent = subscribers.every(
      (n) => n.rate_per_second != null,
    )
      ? number(subscribers.reduce((sum, n) => sum + n.rate_per_second, 0))
      : "Incomplete";
    $("run").textContent = "RUN " + s.run_id + " · one physical lab host";
    $("chains").replaceChildren();
    for (const chain of ["a", "b"]) {
      const section = el("div", null, "chain");
      const title = el("div", null, "chain-title");
      title.append(
        el("strong", "CHAIN " + chain.toUpperCase()),
        el(
          "span",
          s.active_sessions[chain] ? "Active session" : "Quiet session",
        ),
      );
      const nodes = el("div", null, "nodes");
      const ordered = s.nodes
        .filter((n) => n.chain === chain)
        .sort(
          (a, b) =>
            ({ publisher: 0, relay: 1, subscriber: 2 })[a.role] -
            { publisher: 0, relay: 1, subscriber: 2 }[b.role],
        );
      nodes.append(...ordered.map(nodeCard));
      section.append(title, nodes);
      $("chains").append(section);
    }
    try {
      const response = await api("/api/alerts");
      const alerts = response.data.alerts;
      $("alerts-count").textContent = alerts.filter(
        (a) => a.state === "firing",
      ).length;
      $("alerts").replaceChildren();
      if (!alerts.length)
        $("alerts").append(el("div", "● No active monitoring alerts", "ok"));
      for (const a of alerts) {
        const row = el("div", null, "alert");
        row.append(
          el("strong", a.labels.alertname + " · " + a.state),
          el(
            "span",
            [a.labels.process, a.labels.subscriber].filter(Boolean).join(" → "),
          ),
        );
        $("alerts").append(row);
      }
    } catch {
      $("alerts-count").textContent = "Unknown";
      $("alerts").replaceChildren(
        el("div", "Monitoring unavailable. This is not an all-clear.", "error"),
      );
    }
  } catch (error) {
    if (!$("console").hidden) {
      $("connection").textContent = "Disconnected · live view unavailable";
      document.querySelector(".live-label").textContent = "UNAVAILABLE";
      document.querySelector(".live-label").className = "live-label error";
      $("fresh").textContent = "Unknown";
      $("throughput").textContent = "—";
      $("alerts-count").textContent = "Unknown";
      $("alerts").replaceChildren(
        el(
          "div",
          "Alert state unavailable. This is not an all-clear.",
          "error",
        ),
      );
      $("chains").replaceChildren(
        el("p", "Last observations are unavailable. Reconnecting…", "error"),
      );
    }
  }
  if (!$("console").hidden) timer = setTimeout(refresh, 2000);
}
$("login-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  try {
    await api("/api/login", { token: $("token").value });
    $("token").value = "";
    $("login-error").textContent = "";
    refresh();
  } catch (error) {
    $("login-error").textContent = error.message;
  }
});
$("logout").addEventListener("click", async () => {
  await api("/api/logout", {});
  showLogin();
});
$("chat").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("investigate").disabled = true;
  try {
    pending ||= {
      id: crypto.randomUUID().replaceAll("-", ""),
      question: $("question").value,
      chain: $("scope").value,
    };
    sessionStorage.setItem("plant-pending", JSON.stringify(pending));
    const j = await api("/api/investigations", pending);
    pending = null;
    sessionStorage.removeItem("plant-pending");
    selectTask(j.id);
    $("submit-status").textContent =
      "Investigation accepted. You can start another now.";
  } catch (error) {
    if (error.status && error.status < 500) {
      pending = null;
      sessionStorage.removeItem("plant-pending");
    }
    $("submit-status").textContent =
      error.message +
      (pending
        ? " Retry uses the same request ID, not a duplicate investigation."
        : "");
  } finally {
    $("investigate").disabled = false;
    $("question").readOnly = !!pending;
    $("scope").disabled = !!pending;
    $("investigate").textContent = pending
      ? "Retry submission"
      : "Start investigation ↗";
  }
});

function renderDiagnosis(d, question) {
  const answer = el("div", null, "answer");
  answer.append(
    el("span", d.conclusion.replaceAll("_", " "), "conclusion " + d.conclusion),
    el("h3", question),
    el("p", d.summary),
    el("p", "Next check: " + d.next),
  );
  const checks = el("ul", null, "checks");
  checks.append(...d.checks.map((c) => el("li", c)));
  answer.append(checks);
  const details = el("details");
  details.append(
    el(
      "summary",
      "Inspect source evidence · " + d.evidence.length + " processes",
    ),
  );
  details.append(
    el(
      "p",
      "Snapshot " + d.evidence_id + " · " + new Date(d.at).toLocaleTimeString(),
      "small",
    ),
  );
  for (const n of d.evidence) {
    const row = el("div", null, "evidence-row");
    row.append(
      el("strong", n.id + " · " + n.state),
      el("span", "Observed " + n.observed_at + " · sample " + n.sample_seq),
      el(
        "span",
        "Rate " +
          number(n.rate_per_second, 1) +
          " msg/s · source age " +
          number(n.observation_age_seconds, 2) +
          "s",
      ),
    );
    if (n.delivery) {
      row.append(
        el(
          "span",
          "Received " +
            number(n.delivery.ingress) +
            " · consumed " +
            number(n.delivery.consumed) +
            " · missing sequences " +
            number(n.delivery.missing),
        ),
        el(
          "span",
          "Consumption pending " +
            n.delivery.pending +
            " · oldest " +
            number(n.delivery.pending_age_seconds, 2) +
            "s",
        ),
      );
      for (const q of n.delivery.queues || [])
        row.append(
          el(
            "span",
            q.subscriber +
              ": queue " +
              q.depth +
              " · oldest " +
              number(q.oldest_seconds, 2) +
              "s · drops " +
              number(q.drops),
          ),
        );
    }
    if (n.resources) {
      row.append(
        el(
          "span",
          "Resources · " +
            (n.resource_age_seconds > 15 ? "STALE · " : "") +
            number(n.resources.rss_bytes / 1048576, 1) +
            " MiB RSS · " +
            n.resources.file_descriptors +
            " FDs · " +
            number(n.resources.cpu_seconds, 2) +
            " cumulative CPU seconds · observed " +
            number(n.resource_age_seconds, 1) +
            "s ago",
        ),
      );
    }
    details.append(row);
  }
  answer.append(
    details,
    el(
      "p",
      "Rule-based result. No LLM, specialist worker, action or approval was executed.",
      "small",
    ),
  );
  return answer;
}

function selectTask(id) {
  selected = id;
  sessionStorage.setItem("plant-selected", id);
  renderedDetail = "";
  refreshTasks();
}

function connectTasks() {
  if (stream) return;
  const after = sessionStorage.getItem("plant-event") || "0";
  stream = new EventSource("/api/events?after=" + encodeURIComponent(after));
  stream.onopen = () => {
    $("task-connection").textContent = "● Durable progress connected";
  };
  stream.onerror = () => {
    $("task-connection").textContent = "Reconnecting · investigations continue";
  };
  stream.addEventListener("progress", (e) => {
    sessionStorage.setItem("plant-event", e.lastEventId);
    clearTimeout(taskRefresh);
    taskRefresh = setTimeout(refreshTasks, 100);
  });
}

async function refreshTasks() {
  if ($("console").hidden) return;
  if (taskBusy) {
    taskAgain = true;
    return;
  }
  taskBusy = true;
  try {
    const jobs = await api("/api/investigations");
    if ($("console").hidden) return;
    $("task-error").textContent = "";
    const signature = JSON.stringify(jobs) + selected + currentRun;
    if (signature !== renderedBoard) {
      renderedBoard = signature;
      const focus = document.activeElement?.id;
      $("tasks").replaceChildren();
      const running = jobs.filter((j) => j.state === "running").length;
      const queued = jobs.filter((j) => j.state === "queued").length;
      $("task-counts").textContent =
        `${running} / 2 active · ${queued} / 20 queued · latest 100 investigations · no model calls`;
      if (!jobs.length)
        $("tasks").append(
          el("p", "No investigations yet. Start one from the desk above."),
        );
      for (const j of jobs) {
        const row = el(
          "div",
          null,
          "task-row" + (j.id === selected ? " selected" : ""),
        );
        const info = el("div", null, "task-info");
        info.append(
          el(
            "span",
            `CHAIN ${j.chain.toUpperCase()} · ${j.state}`,
            "task-state " + j.state,
          ),
          el("strong", j.question),
          el("span", j.note, "small"),
          el(
            "span",
            `${j.id.slice(0, 8)} · updated ${new Date(j.updated_at).toLocaleTimeString()}${j.run_id !== currentRun ? " · previous plant run" : ""}`,
            "small muted",
          ),
        );
        const actions = el("div", null, "task-actions");
        const view = el("button", "View", "secondary");
        view.id = "view-" + j.id;
        view.setAttribute(
          "aria-label",
          "View " + j.question + " · chain " + j.chain,
        );
        view.onclick = () => {
          selectTask(j.id);
          $("result").scrollIntoView({ block: "nearest", behavior: "smooth" });
        };
        actions.append(view);
        if (["queued", "running"].includes(j.state)) {
          const cancel = el("button", "Cancel", "secondary cancel");
          cancel.id = "cancel-" + j.id;
          cancel.setAttribute(
            "aria-label",
            "Cancel " + j.question + " · chain " + j.chain,
          );
          cancel.onclick = async () => {
            cancel.disabled = true;
            try {
              await api(`/api/investigations/${j.id}/cancel`, {});
              await refreshTasks();
            } catch (error) {
              $("task-error").textContent =
                "Cancellation not confirmed. " + error.message;
              cancel.disabled = false;
            }
          };
          actions.append(cancel);
        }
        row.append(info, actions);
        $("tasks").append(row);
      }
      if (focus) document.getElementById(focus)?.focus({ preventScroll: true });
    }
    if (selected) {
      const id = selected;
      const j = await api("/api/investigations/" + id);
      if (selected !== id || $("console").hidden) return;
      const key = j.id + j.updated_at + currentRun;
      if (key !== renderedDetail) {
        renderedDetail = key;
        const detail = el("div", null, "task-detail");
        detail.append(
          el(
            "span",
            `CHAIN ${j.chain.toUpperCase()} · ${j.state}`,
            "task-state " + j.state,
          ),
          el("h3", j.question),
          el("p", j.note),
          el(
            "p",
            `Snapshots ${j.samples.length}/3 · deadline ${new Date(j.deadline).toLocaleTimeString()} · model calls 0 · reclaim ${j.reclaims}/1`,
            "small",
          ),
        );
        if (j.run_id !== currentRun)
          detail.append(
            el(
              "p",
              "Historical plant run: " +
                j.run_id +
                ". Not evidence about the current run.",
              "error",
            ),
          );
        if (j.result)
          detail.append(renderDiagnosis(j.result, "Window conclusion"));
        for (const [i, sample] of j.samples.entries()) {
          const snapshot = el("details", null, "observation");
          snapshot.append(
            el(
              "summary",
              `Observation ${i + 1} · ${new Date(sample.at).toLocaleTimeString()} · ${sample.conclusion.replaceAll("_", " ")}`,
            ),
            renderDiagnosis(sample, "Source observation"),
          );
          detail.append(snapshot);
        }
        $("result").replaceChildren(detail);
      }
    }
  } catch (error) {
    $("task-error").textContent =
      "Board unavailable; displayed records may be stale. " + error.message;
  } finally {
    taskBusy = false;
    if (taskAgain) {
      taskAgain = false;
      refreshTasks();
    }
  }
}
if (pending) {
  $("question").value = pending.question;
  $("scope").value = pending.chain;
  $("question").readOnly = true;
  $("scope").disabled = true;
  $("investigate").textContent = "Retry submission";
}
refresh();
