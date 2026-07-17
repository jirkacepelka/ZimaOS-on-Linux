"use strict";

// Zima Connect local UI controller.
//
// It polls /api/status, reflects the ZeroTier connection phase, and — the key
// product behaviour — once the connection is live and the ZimaOS server has
// been located, it automatically sends the browser to the ZimaOS login page.

const el = (id) => document.getElementById(id);

const statusBox = el("status");
const statusText = el("status-text");
const connectBtn = el("connect-btn");
const disconnectBtn = el("disconnect-btn");
const openZima = el("open-zima");
const errorBox = el("error");
const backendName = el("backend-name");

// When true, we auto-redirect to ZimaOS as soon as it is reachable. We only do
// this once per page load so the user can come "back" without a redirect loop.
let autoRedirectArmed = true;

const STATE_LABELS = {
  stopped: "Not connected",
  starting: "Starting…",
  joining: "Joining network…",
  discovering: "Finding your ZimaOS…",
  connected: "Connected",
  error: "Problem",
};

function showError(msg) {
  if (!msg) {
    errorBox.hidden = true;
    return;
  }
  errorBox.hidden = false;
  errorBox.textContent = msg;
}

function render(s) {
  const state = s.state || "stopped";

  statusBox.className = "status status-" + state;
  statusText.textContent =
    (STATE_LABELS[state] || state) + (s.message ? " — " + s.message : "");

  backendName.textContent = s.backend ? "engine: " + s.backend : "";

  // Prefill the form from saved config the first time only.
  if (!render.prefilled) {
    if (s.configured_network_id) el("network-id").value = s.configured_network_id;
    if (s.configured_zima_host) el("zima-host").value = s.configured_zima_host;
    el("autostart").checked = !!s.autostart;
    render.prefilled = true;
  }

  const connecting =
    state === "joining" || state === "discovering" || state === "starting";
  connectBtn.disabled = connecting;
  connectBtn.textContent = connecting ? "Connecting…" : "Connect";

  const active = state !== "stopped";
  disconnectBtn.hidden = !active;

  if (state === "connected" && s.zima_url) {
    openZima.hidden = false;
    openZima.href = s.zima_url;
    if (autoRedirectArmed) {
      autoRedirectArmed = false;
      // Small delay so the "Connected" state is visible before we leave.
      setTimeout(() => {
        window.location.href = s.zima_url;
      }, 700);
    }
  } else {
    openZima.hidden = true;
  }
}

async function poll() {
  try {
    const res = await fetch("/api/status");
    const s = await res.json();
    render(s);
  } catch (e) {
    render({ state: "error", message: "cannot reach local service" });
  }
}

async function postJSON(url, body) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body || {}),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

el("connect-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  showError(null);
  autoRedirectArmed = true; // a fresh Connect should redirect on success
  try {
    await postJSON("/api/connect", {
      network_id: el("network-id").value,
      zima_host: el("zima-host").value.trim(),
      autostart: el("autostart").checked,
    });
    poll();
  } catch (e) {
    showError(e.message);
  }
});

disconnectBtn.addEventListener("click", async () => {
  showError(null);
  try {
    await postJSON("/api/disconnect", {});
    poll();
  } catch (e) {
    showError(e.message);
  }
});

el("autostart").addEventListener("change", async (ev) => {
  try {
    await postJSON("/api/autostart", { enabled: ev.target.checked });
  } catch (e) {
    showError(e.message);
    ev.target.checked = !ev.target.checked; // revert on failure
  }
});

poll();
setInterval(poll, 2000);
