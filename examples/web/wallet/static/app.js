import {
  CHAINS, chainName, api, onEvents, initLog, toast, el, icon, short, fill, relative, copyable,
  peer, badge, accountsOf, chainsOf, accountChip, chainSelect, buttonAction, decodeMessage,
} from "/assets/common.js";

const SIGNED = ["personal_sign", "eth_sign", "eth_signTypedData", "eth_signTypedData_v4"];
const TITLES = {
  personal_sign: "Sign message", eth_sign: "Sign message", eth_signTypedData: "Sign typed data",
  eth_signTypedData_v4: "Sign typed data", eth_sendTransaction: "Send transaction",
};

let sessionsByTopic = {};
let lastState = {};

// act calls the API and then reloads the page state, since answering a
// request or a proposal produces no SDK event of its own.
const act = (path, body) => api(path, body).then(refresh);
const uriInput = document.getElementById("uri");

async function pair() {
  const uri = uriInput.value.trim();
  if (!uri) return toast("Paste a wc: link first", "bad");
  await act("/api/pair", { uri });
  uriInput.value = "";
  toast("Paired. Waiting for the proposal…", "info");
}
document.getElementById("pair").addEventListener("click", () => pair().catch(() => {}));
uriInput.addEventListener("keydown", (e) => { if (e.key === "Enter") pair().catch(() => {}); });
document.getElementById("paste").addEventListener("click", async () => {
  try { uriInput.value = (await navigator.clipboard.readText()).trim(); } catch { toast("The browser blocked clipboard access", "bad"); }
});

// ---- proposals ----

function requested(p) {
  const all = { ...p.optionalNamespaces, ...p.requiredNamespaces };
  const chains = [...new Set(Object.entries(all).flatMap(([k, ns]) => (k.includes(":") ? [k] : ns.chains || [])))];
  const methods = [...new Set(Object.values(all).flatMap((ns) => ns.methods || []))];
  return { chains, methods };
}

function renderProposal(p) {
  const { chains, methods } = requested(p);
  return el("div", { class: "item" },
    el("div", { class: "item-head" }, peer(p.proposer.metadata), el("span", { class: "spacer" }),
      el("span", { class: "badge warn", title: "The Go SDK does not implement the Verify API" }, icon("shield"), "Unverified")),
    el("div", { class: "stack-sm", style: "margin-top:16px" },
      el("div", { class: "eyebrow" }, "Wants to connect on"),
      el("div", { class: "row" }, chains.length ? chains.map((c) => el("span", { class: "chip" }, el("span", { class: "net" }, chainName(c))))
        : el("span", { class: "muted" }, "No networks requested; Ethereum will be granted")),
      methods.length ? el("div", { class: "muted small" }, `Methods: ${methods.join(", ")}`) : null),
    el("div", { class: "divider" }),
    el("div", { class: "toolbar" },
      buttonAction([icon("check"), "Approve"], () => act("/api/approve", { proposalId: p.id }), "primary"),
      buttonAction("Reject", () => act("/api/reject", { proposalId: p.id }), "danger"),
      el("button", { class: "ghost", onclick: dismiss }, "Later"),
      el("span", { class: "spacer" }),
      el("span", { class: "muted small" }, icon("clock"), ` Expires ${relative(p.expiryTimestamp)}`)));
}

// ---- requests ----

function payloadView(r) {
  const params = Array.isArray(r.params) ? r.params : [];
  if (r.method === "personal_sign" || r.method === "eth_sign") {
    const msg = r.method === "personal_sign" ? params[0] : params[1];
    return el("div", { class: "payload" }, el("div", { class: "label" }, "Message"), el("div", { class: "message" }, decodeMessage(msg)));
  }
  if (r.method.startsWith("eth_signTypedData")) {
    let td = params[1];
    try { if (typeof td === "string") td = JSON.parse(td); } catch { /* show raw */ }
    const domain = td && td.domain ? `${td.domain.name || "Unknown"} · ${td.primaryType || ""}` : "";
    return el("div", { class: "payload" }, el("div", { class: "label" }, domain || "Typed data"),
      el("pre", {}, JSON.stringify(td && td.message ? td.message : td, null, 2)));
  }
  return el("div", { class: "payload" }, el("div", { class: "label" }, "Params"), el("pre", {}, JSON.stringify(r.params, null, 2)));
}

function renderRequest(r) {
  const session = sessionsByTopic[r.topic];
  const signable = SIGNED.includes(r.method);
  return el("div", { class: "item" },
    el("div", { class: "item-head" },
      session ? peer(session.peer.metadata) : el("div", {}, "Unknown session"),
      el("span", { class: "spacer" }), el("span", { class: "badge blue" }, r.method)),
    el("h3", { style: "margin-top:16px;font-size:22px" }, TITLES[r.method] || r.method),
    el("div", { class: "row", style: "margin-top:6px" }, el("span", { class: "chip" }, el("span", { class: "net" }, chainName(r.chainId))),
      el("span", { class: "muted small" }, icon("clock"), ` Expires ${relative(r.expiryTimestamp)}`)),
    payloadView(r),
    signable ? null : el("div", { class: "muted small", style: "margin-top:10px" },
      "This wallet cannot perform this method. Approving answers with an unsupported-method error."),
    el("div", { class: "divider" }),
    el("div", { class: "toolbar" },
      buttonAction([icon("pen"), signable ? "Sign" : "Answer"], () => act("/api/respond", { topic: r.topic, id: r.id, approve: true }), "primary"),
      buttonAction("Reject", () => act("/api/respond", { topic: r.topic, id: r.id, approve: false }), "danger"),
      el("button", { class: "ghost", onclick: dismiss }, "Later")));
}

// ---- modal ----
// Waiting proposals, then requests, open one at a time. "Later" or Esc
// postpones the current one; the header's waiting button brings it back.

const modal = document.getElementById("modal");
const dismissed = new Set();
let shownKey = null;
modal.addEventListener("cancel", (e) => { e.preventDefault(); dismiss(); });

const waitingButton = document.getElementById("waiting");
waitingButton.addEventListener("click", () => {
  dismissed.clear();
  updateModal();
});

function dismiss() {
  if (shownKey) dismissed.add(shownKey);
  shownKey = null;
  updateModal();
}

function updateModal() {
  const all = [
    ...(lastState.proposals || []).map((p) => ({ key: `p:${p.id}`, title: "Connection request", view: () => renderProposal(p) })),
    ...(lastState.requests || []).map((r) => ({ key: `r:${r.id}`, title: TITLES[r.method] ? "Signature request" : "Request", view: () => renderRequest(r) })),
  ];
  const queue = all.filter((q) => !dismissed.has(q.key));
  const postponed = all.length - queue.length;
  waitingButton.hidden = postponed === 0;
  waitingButton.textContent = `${postponed} waiting`;
  const next = queue[0];
  if (!next) {
    shownKey = null;
    if (modal.open) modal.close();
    return;
  }
  const counter = document.getElementById("modal-count");
  if (next.key === shownKey) {
    if (counter) counter.textContent = queue.length > 1 ? `1 of ${queue.length}` : "";
    return;
  }
  shownKey = next.key;
  modal.replaceChildren(el("div", { class: "modal-card" },
    el("div", { class: "modal-head" }, el("h2", {}, next.title),
      el("span", { class: "count", id: "modal-count" }, queue.length > 1 ? `1 of ${queue.length}` : ""),
      el("span", { class: "spacer" }),
      el("button", { class: "ghost icon", title: "Later", "aria-label": "Close", onclick: dismiss }, icon("x"))),
    next.view()));
  if (!modal.open) modal.showModal();
  modal.querySelector("button.primary")?.focus();
}

// ---- sessions ----

function renderSession(s) {
  const chains = chainsOf(s);
  const network = chainSelect(chains);
  const missing = CHAINS.map(([c]) => c).filter((c) => !chains.includes(c));
  const add = chainSelect(missing);
  const emit = (name, label) => buttonAction([icon("bolt"), label], () => act("/api/emit", { topic: s.topic, chainId: network.value, name }));

  return el("div", { class: "item" },
    el("div", { class: "item-head" }, peer(s.peer.metadata), el("span", { class: "spacer" }),
      s.acknowledged ? badge("Active", "ok") : badge("Waiting for the dApp", "warn")),
    el("div", { class: "row", style: "margin-top:14px" }, accountsOf(s).map(accountChip)),
    el("div", { class: "divider" }),
    el("div", { class: "toolbar" },
      el("span", { class: "muted small" }, "Network"), network,
      emit("chainChanged", "Switch to network"), emit("accountsChanged", "Announce account")),
    missing.length ? el("div", { class: "toolbar", style: "margin-top:8px" },
      el("span", { class: "muted small" }, "Add"), add,
      buttonAction([icon("plus"), "Add network"], () => act("/api/update", { topic: s.topic, chainId: add.value }))) : null,
    el("div", { class: "toolbar", style: "margin-top:8px" },
      buttonAction([icon("ping"), "Ping"], () => act("/api/ping", { topic: s.topic }), "ghost"),
      buttonAction([icon("refresh"), "Extend"], () => act("/api/extend", { topic: s.topic }), "ghost"),
      el("span", { class: "spacer" }),
      buttonAction([icon("power"), "Disconnect"], () => act("/api/disconnect", { topic: s.topic }), "danger")),
    el("div", { class: "meta" },
      el("span", {}, "Topic ", copyable(s.topic)),
      el("span", {}, icon("clock"), `Expires ${relative(s.expiry)}`)));
}

async function refresh() {
  const state = await api("/api/state");
  lastState = state;
  document.getElementById("account").replaceChildren(copyable(state.address, short(state.address, 5)));
  sessionsByTopic = Object.fromEntries((state.sessions || []).map((s) => [s.topic, s]));
  fill("sessions", state.sessions || [], renderSession, "No sessions yet", "Paste a wc: link above to connect to a dApp.");
  updateModal();
}

onEvents((type, data) => {
  switch (type) {
    case "session_proposal":
      toast(`${data.Proposal.proposer.metadata.name || "A dApp"} wants to connect`, "info");
      break;
    case "session_request":
      toast(`New request: ${data.Method}`, "info");
      break;
    case "approve_ack":
      toast(data.error ? `Session not acknowledged: ${data.error}` : "Session established", data.error ? "bad" : "ok");
      break;
    case "update_ack":
      toast(data.error ? `Update not acknowledged: ${data.error}` : "The dApp accepted the new networks", data.error ? "bad" : "ok");
      break;
    case "extend_ack":
      toast(data.error ? `Extend not acknowledged: ${data.error}` : "Session extended by 7 days", data.error ? "bad" : "ok");
      break;
    case "ping_result":
      toast(data.error ? `Ping failed: ${data.error}` : "The dApp answered the ping", data.error ? "bad" : "ok");
      break;
    case "session_delete":
      toast("The dApp disconnected", "info");
      break;
  }
  if (type !== "relay_state") refresh();
});

initLog();
refresh();
setInterval(refresh, 60000);
