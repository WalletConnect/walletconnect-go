import {
  CHAINS, chainName, api, onEvents, initLog, toast, el, icon, short, fill, relative, copyable,
  peer, badge, accountsOf, chainsOf, chainSelect, buttonAction,
} from "/assets/common.js";

const METHODS = ["personal_sign", "eth_signTypedData_v4", "eth_sendTransaction"];
const METHOD_LABEL = { personal_sign: "Sign message", eth_signTypedData_v4: "Sign typed data", eth_sendTransaction: "Send transaction" };
const EVENTS = ["chainChanged", "accountsChanged"];

// Signing requests per session topic, newest first, as kept by the server.
let results = {};

const act = (path, body) => api(path, body).then(refresh);

// The chain and address picked per session, kept across re-renders.
const picks = {};
let countdownTimer;

function toggles(id, items, checked) {
  document.getElementById(id).replaceChildren(...items.map(([value, label]) =>
    el("label", { class: "chip-toggle" }, el("input", { type: "checkbox", value, checked: checked.includes(value) }), el("span", {}, label))));
}
const checkedValues = (id) => [...document.querySelectorAll(`#${id} input:checked`)].map((i) => i.value);

toggles("chains", CHAINS, ["eip155:1", "eip155:10"]);
toggles("methods", METHODS.map((m) => [m, m]), ["personal_sign", "eth_signTypedData_v4"]);
document.getElementById("connect-row").append(
  buttonAction([icon("qr"), "Connect wallet"], connect, "primary lg"),
  el("span", { class: "muted small" }, "Proposals expire after 5 minutes."));

async function connect() {
  const out = await api("/api/connect", { chains: checkedValues("chains"), methods: checkedValues("methods"), events: EVENTS });
  const countdown = el("div", { class: "countdown" });
  const tick = () => {
    const left = Math.max(0, Math.round(out.expiry - Date.now() / 1000));
    countdown.textContent = `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
    if (left === 0) { clearInterval(countdownTimer); countdown.textContent = "Expired"; }
  };
  clearInterval(countdownTimer);
  countdownTimer = setInterval(tick, 1000);
  tick();
  document.getElementById("pairing").replaceChildren(el("div", { class: "pairing" },
    el("div", { class: "qr" }, el("img", { src: out.qr, alt: "Pairing QR code" })),
    el("div", {},
      el("div", { class: "eyebrow" }, "Waiting for a wallet"),
      el("h3", {}, "Scan with your wallet"),
      el("div", { class: "row", style: "margin-top:6px" }, countdown, el("span", { class: "muted small" }, "until the proposal expires")),
      el("div", { class: "uri-box" },
        el("input", { type: "text", readonly: true, value: out.uri, "aria-label": "Pairing URI" }),
        buttonAction([icon("copy"), "Copy"], async () => { await navigator.clipboard.writeText(out.uri); toast("Pairing link copied", "ok"); })),
      el("div", { class: "row", style: "margin-top:10px" },
        buttonAction("Cancel", async () => { await api("/api/disconnect", { topic: out.pairingTopic }); clearPairing(); }, "ghost")))));
}

function clearPairing() {
  clearInterval(countdownTimer);
  document.getElementById("pairing").replaceChildren();
}

const resultTitle = (r) =>
  `${METHOD_LABEL[r.method] || r.method} on ${chainName(r.chainId)}${r.account ? ` as ${short(r.account, 5)}` : ""}`;

function renderResult(r) {
  if (r.pending) {
    return el("div", { class: "result pending" }, el("span", { class: "status" }),
      el("div", { class: "body" }, el("div", { class: "line1" }, resultTitle(r)),
        el("div", { class: "line2" }, "Waiting for the wallet to answer…")));
  }
  if (r.error) {
    return el("div", { class: "result bad" }, el("span", { class: "status" }, icon("x")),
      el("div", { class: "body" }, el("div", { class: "line1" }, resultTitle(r)),
        el("div", { class: "line2" }, r.code ? `Error ${r.code}: ${r.error}` : r.error)));
  }
  const res = r.result || {};
  const verified = res.verified;
  const response = typeof res.response === "string" ? res.response : JSON.stringify(res.response);
  return el("div", { class: `result ${verified === false ? "bad" : "ok"}` },
    el("span", { class: "status" }, icon(verified === false ? "x" : "check")),
    el("div", { class: "body" },
      el("div", { class: "line1" }, resultTitle(r)),
      el("div", { class: "line2" },
        verified === true ? el("span", { style: "color:var(--ok)" }, "Signature verified against the session account · ")
          : verified === false ? el("span", {}, `Signed by ${short(res.signer)}, not the session account · `) : "",
        copyable(response, short(response, 10)))));
}

// addressesOn lists the session's addresses on chain, in account order.
const addressesOn = (s, chain) => accountsOf(s).filter((a) => a.startsWith(chain + ":")).map((a) => a.split(":")[2]);

// accountGroups groups CAIP-10 accounts by address: [[address, [chains]]].
function accountGroups(s) {
  const groups = new Map();
  for (const a of accountsOf(s)) {
    const [ns, ref, address] = a.split(":");
    const key = address.toLowerCase();
    if (!groups.has(key)) groups.set(key, [address, []]);
    groups.get(key)[1].push(`${ns}:${ref}`);
  }
  return [...groups.values()];
}

function renderSession(s) {
  const chains = chainsOf(s);
  const pick = (picks[s.topic] ||= {});
  if (!chains.includes(pick.chain)) pick.chain = chains[0];

  const chainPicker = chainSelect(chains);
  chainPicker.value = pick.chain;
  const addressPicker = el("select", { "aria-label": "Address" });
  const addressRow = el("span", { class: "row" }, el("span", { class: "muted small" }, "as"), addressPicker);
  const fillAddresses = () => {
    const addresses = addressesOn(s, pick.chain);
    if (!addresses.some((a) => a.toLowerCase() === (pick.account || "").toLowerCase())) pick.account = addresses[0];
    addressPicker.replaceChildren(...addresses.map((a) => el("option", { value: a }, short(a, 5))));
    addressPicker.value = pick.account;
    addressRow.hidden = addresses.length < 2;
  };
  chainPicker.addEventListener("change", () => { pick.chain = chainPicker.value; fillAddresses(); });
  addressPicker.addEventListener("change", () => { pick.account = addressPicker.value; });
  fillAddresses();

  const methods = Object.values(s.namespaces).flatMap((ns) => ns.methods || []);
  const send = (method) => buttonAction([icon("pen"), METHOD_LABEL[method]], () =>
    act("/api/request", { topic: s.topic, chainId: pick.chain, account: pick.account, method }));
  const list = results[s.topic] || [];

  return el("div", { class: "item" },
    el("div", { class: "item-head" }, peer(s.peer.metadata), el("span", { class: "spacer" }),
      s.acknowledged ? badge("Active", "ok") : badge("Settling", "warn")),
    el("div", { class: "stack-sm", style: "margin-top:14px" }, accountGroups(s).map(([address, chainList]) =>
      el("div", { class: "row" }, copyable(address, short(address, 6)),
        chainList.map((c) => el("span", { class: "chip" }, el("span", { class: "net" }, chainName(c))))))),
    el("div", { class: "divider" }),
    el("div", { class: "toolbar" },
      el("span", { class: "muted small" }, "Request on"), chainPicker, addressRow),
    el("div", { class: "toolbar", style: "margin-top:8px" }, METHODS.filter((m) => methods.includes(m)).map(send)),
    el("div", { class: "toolbar", style: "margin-top:8px" },
      buttonAction([icon("ping"), "Ping"], () => act("/api/ping", { topic: s.topic }), "ghost"),
      buttonAction([icon("refresh"), "Extend"], () => act("/api/extend", { topic: s.topic }), "ghost"),
      el("span", { class: "spacer" }),
      buttonAction([icon("power"), "Disconnect"], () => act("/api/disconnect", { topic: s.topic }), "danger")),
    el("div", { class: "meta" },
      el("span", {}, "Topic ", copyable(s.topic)),
      el("span", {}, icon("clock"), `Expires ${relative(s.expiry)}`)),
    list.length ? el("div", { class: "results" }, list.slice(0, 8).map(renderResult)) : null);
}

async function refresh() {
  const state = await api("/api/state");
  results = state.requests || {};
  document.getElementById("client").replaceChildren(el("span", { class: "muted small" }, "Client "), copyable(state.clientId, short(state.clientId.replace("did:key:", ""), 4)));
  fill("sessions", state.sessions || [], renderSession, "No sessions yet", "Connect a wallet above to start a session.");
}

onEvents((type, data) => {
  switch (type) {
    case "connect_result":
      if (data.error) toast(`Connection failed: ${data.error}`, "bad");
      else if (data.result.cancelled) clearPairing();
      else { toast(`Connected to ${data.result.wallet}`, "ok"); clearPairing(); }
      break;
    case "request_result":
      if (data.error) toast(`${data.method} failed: ${data.error}`, "bad");
      break;
    case "ping_result":
      toast(data.error ? `Ping failed: ${data.error}` : `Wallet answered the ping in ${data.result.roundTrip}`, data.error ? "bad" : "ok");
      break;
    case "extend_ack":
      toast(data.error ? `Extend not acknowledged: ${data.error}` : "Session extended by 7 days", data.error ? "bad" : "ok");
      break;
    case "session_delete":
      toast("The wallet disconnected", "info");
      break;
    case "session_event":
      toast(`Wallet event: ${data.Name} on ${chainName(data.ChainID)}`, "info");
      break;
    case "session_update":
      toast("The wallet updated the session accounts", "info");
      break;
  }
  if (type !== "relay_state") refresh();
});

initLog();
refresh();
setInterval(refresh, 60000);
