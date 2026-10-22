// Generates testdata/vectors.json from the published JS SDK so the Go
// implementation can be checked byte for byte.
//   cd interop/js && npm install && node gen-vectors.mjs
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { x25519 } from "@noble/curves/ed25519";
import * as relayAuth from "@walletconnect/relay-auth";
import {
  deriveSymKey,
  hashKey,
  hashMessage,
  encrypt,
  decrypt,
  formatUri,
  parseUri,
  isConformingNamespaces,
  isValidNamespaces,
  isValidRequiredNamespaces,
} from "@walletconnect/utils";

const hex = (b) => Buffer.from(b).toString("hex");
const fromHex = (h) => Uint8Array.from(Buffer.from(h, "hex"));
const fill = (n, v) => new Uint8Array(n).fill(v);

// ---- crypto ----
const privA = hex(fill(32, 0x11));
const privB = hex(fill(32, 0x22));
const pubA = hex(x25519.getPublicKey(fromHex(privA)));
const pubB = hex(x25519.getPublicKey(fromHex(privB)));
const symKey = deriveSymKey(privA, pubB);
const pairingSymKey = hex(fill(32, 0x33));
const iv = hex(fill(12, 0x44));
const plaintext = JSON.stringify({ id: 1760000000000123, jsonrpc: "2.0", method: "wc_sessionPing", params: {} });
const sealed = encrypt({ symKey, message: plaintext, iv });

const cryptoVectors = {
  privA,
  privB,
  pubA,
  pubB,
  symKey,
  symKeyReverse: deriveSymKey(privB, pubA),
  sessionTopic: hashKey(symKey),
  pairingSymKey,
  pairingTopic: hashKey(pairingSymKey),
  iv,
  plaintext,
  sealed,
  decrypted: decrypt({ symKey, encoded: sealed }),
  hashMessageInput: "hello walletconnect",
  hashMessage: hashMessage("hello walletconnect"),
};

// ---- relay auth ----
const seed = fill(32, 0x55);
const keyPair = relayAuth.generateKeyPair(seed);
const sub = hex(fill(32, 0x66));
const aud = "wss://relay.walletconnect.org";
const iat = 1760000000;
const ttl = 86400;
const jwt = await relayAuth.signJWT(sub, aud, ttl, keyPair, iat);
const clientId = relayAuth.encodeIss(keyPair.publicKey);
const topicForSub = cryptoVectors.sessionTopic;
const authVectors = {
  seed: hex(seed),
  publicKey: hex(keyPair.publicKey),
  clientId,
  sub,
  aud,
  iat,
  ttl,
  jwt,
  subscriptionTopic: topicForSub,
  subscriptionId: hashMessage(topicForSub + clientId),
};

// ---- uri ----
const uriParams = {
  protocol: "wc",
  version: 2,
  topic: cryptoVectors.pairingTopic,
  symKey: pairingSymKey,
  relay: { protocol: "irn" },
  expiryTimestamp: 1760000300,
};
const formatted = formatUri(uriParams);
const formattedWithMethods = formatUri({ ...uriParams, methods: ["wc_sessionPropose", "wc_sessionAuthenticate"] });
const parseInputs = [
  formatted,
  formattedWithMethods,
  formatted.replace("wc:", "wc://"),
  Buffer.from(formatted).toString("base64"),
  `wc:${cryptoVectors.pairingTopic}@2?relay-protocol=irn&symKey=${pairingSymKey}`,
];
const uriVectors = {
  input: uriParams,
  formatted,
  formattedWithMethods,
  parsed: parseInputs.map((input) => {
    const p = parseUri(input);
    return {
      input,
      topic: p.topic,
      version: p.version,
      symKey: p.symKey,
      relay: p.relay,
      expiryTimestamp: p.expiryTimestamp ?? 0,
      methods: p.methods ?? [],
    };
  }),
};

// ---- namespaces ----
const evm = { methods: ["eth_sendTransaction", "personal_sign"], events: ["chainChanged", "accountsChanged"] };
const acc = (c) => `${c}:0xab16a96D359eC26a11e2C2b3d8f8B8942d5Bfcdb`;
const conformingCases = [
  { name: "empty required", required: {}, approved: { eip155: { accounts: [acc("eip155:1")], ...evm } } },
  { name: "exact match", required: { eip155: { chains: ["eip155:1"], ...evm } }, approved: { eip155: { accounts: [acc("eip155:1")], ...evm } } },
  { name: "approved superset", required: { eip155: { chains: ["eip155:1"], methods: ["personal_sign"], events: [] } }, approved: { eip155: { accounts: [acc("eip155:1"), acc("eip155:10")], ...evm } } },
  { name: "missing namespace", required: { solana: { chains: ["solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"], methods: ["solana_signMessage"], events: [] } }, approved: { eip155: { accounts: [acc("eip155:1")], ...evm } } },
  { name: "missing chain", required: { eip155: { chains: ["eip155:1", "eip155:137"], ...evm } }, approved: { eip155: { accounts: [acc("eip155:1")], ...evm } } },
  { name: "missing method", required: { eip155: { chains: ["eip155:1"], methods: ["eth_signTypedData_v4"], events: [] } }, approved: { eip155: { accounts: [acc("eip155:1")], ...evm } } },
  { name: "missing event", required: { eip155: { chains: ["eip155:1"], methods: [], events: ["message"] } }, approved: { eip155: { accounts: [acc("eip155:1")], ...evm } } },
  { name: "inline required key", required: { "eip155:1": evm }, approved: { eip155: { accounts: [acc("eip155:1")], ...evm } } },
  { name: "inline approved key matching accounts", required: { "eip155:1": evm }, approved: { "eip155:1": { accounts: [acc("eip155:1")], ...evm } } },
  { name: "inline approved key wrong accounts", required: {}, approved: { "eip155:1": { accounts: [acc("eip155:5")], ...evm } } },
].map((c) => {
  const err = isConformingNamespaces(c.required, c.approved, "approve()");
  return { ...c, errorCode: err ? err.code : 0 };
});

const sessionCases = [
  { name: "valid", namespaces: { eip155: { accounts: [acc("eip155:1")], ...evm } } },
  { name: "bad account", namespaces: { eip155: { accounts: ["eip155:1"], ...evm } } },
  { name: "empty account address", namespaces: { eip155: { accounts: ["eip155:1:"], ...evm } } },
  { name: "empty methods ok", namespaces: { eip155: { accounts: [acc("eip155:1")], methods: [], events: [] } } },
  { name: "empty object", namespaces: {} },
].map((c) => {
  const err = isValidNamespaces(c.namespaces, "approve()");
  return { ...c, errorCode: err ? err.code : 0 };
});

const proposalCases = [
  { name: "valid chains array", namespaces: { eip155: { chains: ["eip155:1"], ...evm } } },
  { name: "valid inline key", namespaces: { "eip155:1": evm } },
  { name: "no chains", namespaces: { eip155: evm } },
  { name: "bad chain", namespaces: { eip155: { chains: ["eip155"], ...evm } } },
  { name: "empty object", namespaces: {} },
].map((c) => {
  const err = isValidRequiredNamespaces(c.namespaces, "connect()", "optionalNamespaces");
  return { ...c, errorCode: err ? err.code : 0 };
});

const out = {
  crypto: cryptoVectors,
  auth: authVectors,
  uri: uriVectors,
  namespaces: { conforming: conformingCases, session: sessionCases, proposal: proposalCases },
};
const target = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "testdata", "vectors.json");
writeFileSync(target, JSON.stringify(out, null, 2) + "\n");
console.log(`wrote ${target}`);
