package engine

// Transaction hash extraction for the TNV data a wallet's result carries.
// Ported from extractTxHashesFromResult in
// packages/sign-client/src/controllers/engine.ts and the helpers in
// packages/utils/src/signatures.ts, polkadot.ts and canton.ts. Where a chain
// returns only a signed transaction, the hash is computed the way the chain
// does, so the relay can match it with what lands on chain.

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"github.com/mr-tron/base58"
	"golang.org/x/crypto/blake2b"
)

// tnvMethods are the session request methods whose results carry a
// transaction hash, with the result key that holds it; "" means the result
// is the hash itself or needs the method-specific extraction below.
var tnvMethods = map[string]string{
	"eth_sendTransaction":    "",
	"eth_sendRawTransaction": "",
	"wallet_sendCalls":       "",

	"solana_signTransaction":        "signature",
	"solana_signAllTransactions":    "transactions",
	"solana_signAndSendTransaction": "signature",

	"sui_signAndExecuteTransaction": "digest",
	"sui_signTransaction":           "",

	"hedera_signAndExecuteTransaction": "transactionId",
	"hedera_executeTransaction":        "transactionId",

	"near_signTransaction":  "",
	"near_signTransactions": "",

	"tron_signTransaction": "txID",

	"xrpl_signTransaction":    "",
	"xrpl_signTransactionFor": "",

	"algo_signTxn": "",

	"sendTransfer": "txid", // bitcoin

	"stacks_stxTransfer": "txId",

	"polkadot_signTransaction": "",

	"cosmos_signDirect": "",

	"canton_prepareSignExecute": "",

	"stellar_signXDR":          "",
	"stellar_signAndSubmitXDR": "tx_hash",
}

// txHashes returns the transaction hashes the result of a session request
// carries, or nil when the method is not one that produces transactions or
// the result cannot be read. It never fails: TNV data is best effort.
func txHashes(method string, params, result json.RawMessage) []string {
	key, ok := tnvMethods[method]
	if !ok || len(result) == 0 || bytes.Equal(result, []byte("null")) {
		return nil
	}
	hashes, err := extractTxHashes(method, key, params, result)
	if err != nil || len(hashes) == 0 {
		return nil
	}
	return hashes
}

func extractTxHashes(method, key string, params, result json.RawMessage) ([]string, error) {
	switch method {
	case "sui_signTransaction":
		var r struct {
			TransactionBytes string `json:"transactionBytes"`
		}
		if err := json.Unmarshal(result, &r); err != nil {
			return nil, err
		}
		h, err := suiDigest(r.TransactionBytes)
		return []string{h}, err
	case "near_signTransaction":
		h, err := nearTransactionID(result)
		return []string{h}, err
	case "near_signTransactions":
		var txs []json.RawMessage
		if err := json.Unmarshal(result, &txs); err != nil {
			return nil, err
		}
		out := make([]string, 0, len(txs))
		for _, tx := range txs {
			h, err := nearTransactionID(tx)
			if err != nil {
				return nil, err
			}
			out = append(out, h)
		}
		return out, nil
	case "xrpl_signTransaction", "xrpl_signTransactionFor":
		var r struct {
			TxJSON struct {
				Hash string `json:"hash"`
			} `json:"tx_json"`
		}
		if err := json.Unmarshal(result, &r); err != nil {
			return nil, err
		}
		return nonEmpty(r.TxJSON.Hash), nil
	case "polkadot_signTransaction":
		var p struct {
			TransactionPayload polkadotPayload `json:"transactionPayload"`
		}
		var r struct {
			Signature string `json:"signature"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(result, &r); err != nil {
			return nil, err
		}
		h, err := polkadotExtrinsicHash(p.TransactionPayload, r.Signature)
		return []string{h}, err
	case "algo_signTxn":
		var txs []*string
		if err := json.Unmarshal(result, &txs); err != nil {
			var one string
			if err := json.Unmarshal(result, &one); err != nil {
				return nil, err
			}
			txs = []*string{&one}
		}
		var out []string
		for _, tx := range txs {
			if tx == nil {
				continue // a transaction in the group this wallet did not sign
			}
			h, err := algorandTransactionID(*tx)
			if err != nil {
				return nil, err
			}
			out = append(out, h)
		}
		return out, nil
	case "cosmos_signDirect":
		h, err := cosmosSignDirectHash(result)
		return []string{h}, err
	case "stellar_signXDR":
		var p struct {
			Chain string `json:"chain"`
		}
		var r struct {
			SignedXDR string `json:"signedXDR"`
		}
		_ = json.Unmarshal(params, &p)
		if err := json.Unmarshal(result, &r); err != nil {
			return nil, err
		}
		h, err := stellarTransactionHash(r.SignedXDR, p.Chain)
		return []string{h}, err
	case "wallet_sendCalls":
		return walletSendCallsHashes(result)
	case "canton_prepareSignExecute":
		return cantonTransactionHashes(params, result)
	}

	// The result is the hash: "0x...".
	var s string
	if err := json.Unmarshal(result, &s); err == nil {
		return nonEmpty(s), nil
	}
	if key == "" {
		return nil, nil
	}
	// The result is an object holding the hash, or a list of them, under key.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(result, &obj); err != nil {
		return nil, err
	}
	raw, ok := obj[key]
	if !ok {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &s); err == nil {
		return nonEmpty(s), nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, err
	}
	if method == "solana_signAllTransactions" {
		out := make([]string, 0, len(list))
		for _, tx := range list {
			id, err := solanaTransactionID(tx)
			if err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, nil
	}
	return list, nil
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// decodeBase64 accepts standard or URL-safe base64, padded or not.
func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

func decodeHex(s string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}

// ---- solana ----

// solanaTransactionID is the first signature of a serialized transaction,
// which is the transaction's id on chain.
func solanaTransactionID(b64 string) (string, error) {
	b, err := decodeBase64(b64)
	if err != nil {
		return "", err
	}
	if len(b) == 0 || b[0] == 0 {
		return "", errors.New("solana: no signatures found")
	}
	if len(b) < 1+int(b[0])*64 {
		return "", errors.New("solana: transaction too short for its signature count")
	}
	if len(b) < 100 {
		return "", errors.New("solana: transaction too short")
	}
	return base58.Encode(b[1:65]), nil
}

// ---- sui ----

// suiDigest is blake2b-256 over the type tag and the transaction bytes.
func suiDigest(b64 string) (string, error) {
	b, err := decodeBase64(b64)
	if err != nil {
		return "", err
	}
	sum := blake2b.Sum256(append([]byte("TransactionData::"), b...))
	return base58.Encode(sum[:]), nil
}

// ---- near ----

// nearTransactionID is sha256 of the signed transaction bytes, which arrive
// as a JSON array, as an object with numeric keys, or as a Buffer-like
// object wrapping either under "data".
func nearTransactionID(raw json.RawMessage) (string, error) {
	b, err := bytesFromJSON(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return base58.Encode(sum[:]), nil
}

func bytesFromJSON(raw json.RawMessage) ([]byte, error) {
	var ints []int
	if err := json.Unmarshal(raw, &ints); err == nil {
		return intsToBytes(ints)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("near: unexpected bytes encoding")
	}
	if data, ok := obj["data"]; ok {
		return bytesFromJSON(data)
	}
	// Object.values of an object with numeric keys, in ascending key order.
	keys := make([]int, 0, len(obj))
	for k := range obj {
		n, err := strconv.Atoi(k)
		if err != nil {
			return nil, fmt.Errorf("near: unexpected key %q in bytes object", k)
		}
		keys = append(keys, n)
	}
	sort.Ints(keys)
	ints = make([]int, 0, len(keys))
	for _, k := range keys {
		var v int
		if err := json.Unmarshal(obj[strconv.Itoa(k)], &v); err != nil {
			return nil, err
		}
		ints = append(ints, v)
	}
	return intsToBytes(ints)
}

func intsToBytes(ints []int) ([]byte, error) {
	out := make([]byte, len(ints))
	for i, v := range ints {
		if v < 0 || v > 255 {
			return nil, fmt.Errorf("near: byte value %d out of range", v)
		}
		out[i] = byte(v)
	}
	return out, nil
}

// ---- algorand ----

// algorandTransactionID is sha512/256 over "TX" and the canonical msgpack of
// the unsigned transaction, base32 without padding. The signed transaction
// is a msgpack map whose "txn" entry is that encoding, so the bytes are
// taken from it in place rather than decoded and re-encoded.
func algorandTransactionID(b64 string) (string, error) {
	signed, err := decodeBase64(b64)
	if err != nil {
		return "", err
	}
	txn, err := msgpackMapValue(signed, "txn")
	if err != nil {
		return "", fmt.Errorf("algorand: %w", err)
	}
	sum := sha512.Sum512_256(append([]byte("TX"), txn...))
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:]), nil
}

// msgpackMapValue returns the encoded value under a string key of a msgpack
// map, or an error when b is not a map or lacks the key.
func msgpackMapValue(b []byte, want string) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty")
	}
	var n int
	var pos int
	switch c := b[0]; {
	case c&0xf0 == 0x80:
		n, pos = int(c&0x0f), 1
	case c == 0xde:
		if len(b) < 3 {
			return nil, errors.New("truncated map")
		}
		n, pos = int(binary.BigEndian.Uint16(b[1:3])), 3
	case c == 0xdf:
		if len(b) < 5 {
			return nil, errors.New("truncated map")
		}
		n, pos = int(binary.BigEndian.Uint32(b[1:5])), 5
	default:
		return nil, errors.New("not a map")
	}
	for range n {
		key, klen, err := msgpackString(b[pos:])
		if err != nil {
			return nil, err
		}
		pos += klen
		vlen, err := msgpackSkip(b[pos:])
		if err != nil {
			return nil, err
		}
		if key == want {
			return b[pos : pos+vlen], nil
		}
		pos += vlen
	}
	return nil, fmt.Errorf("missing %q", want)
}

func msgpackString(b []byte) (string, int, error) {
	if len(b) == 0 {
		return "", 0, errors.New("truncated string")
	}
	var head, n int
	switch c := b[0]; {
	case c&0xe0 == 0xa0:
		head, n = 1, int(c&0x1f)
	case c == 0xd9 && len(b) >= 2:
		head, n = 2, int(b[1])
	case c == 0xda && len(b) >= 3:
		head, n = 3, int(binary.BigEndian.Uint16(b[1:3]))
	case c == 0xdb && len(b) >= 5:
		head, n = 5, int(binary.BigEndian.Uint32(b[1:5]))
	default:
		return "", 0, errors.New("map key is not a string")
	}
	if len(b) < head+n {
		return "", 0, errors.New("truncated string")
	}
	return string(b[head : head+n]), head + n, nil
}

// msgpackSkip returns the encoded length of the value at the start of b.
func msgpackSkip(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, errors.New("truncated value")
	}
	c := b[0]
	// Fixed-size and length-prefixed scalars.
	var size int // total size when known from the format byte
	var head, n, items int
	switch {
	case c <= 0x7f, c >= 0xe0, c == 0xc0, c == 0xc2, c == 0xc3:
		size = 1
	case c&0xe0 == 0xa0:
		size = 1 + int(c&0x1f)
	case c&0xf0 == 0x90:
		head, items = 1, int(c&0x0f)
	case c&0xf0 == 0x80:
		head, items = 1, 2*int(c&0x0f)
	case c == 0xc4, c == 0xd9:
		head, n = 2, -1
	case c == 0xc5, c == 0xda:
		head, n = 3, -2
	case c == 0xc6, c == 0xdb:
		head, n = 5, -4
	case c == 0xc7:
		head, n = 3, -1
	case c == 0xc8:
		head, n = 4, -2
	case c == 0xc9:
		head, n = 6, -4
	case c == 0xca, c == 0xd2, c == 0xce:
		size = 5
	case c == 0xcb, c == 0xd3, c == 0xcf:
		size = 9
	case c == 0xcc, c == 0xd0:
		size = 2
	case c == 0xcd, c == 0xd1:
		size = 3
	case c == 0xd4:
		size = 3
	case c == 0xd5:
		size = 4
	case c == 0xd6:
		size = 6
	case c == 0xd7:
		size = 10
	case c == 0xd8:
		size = 18
	case c == 0xdc:
		head, items = 3, -2
	case c == 0xdd:
		head, items = 5, -4
	case c == 0xde:
		head, items = 3, -2
	case c == 0xdf:
		head, items = 5, -4
	default:
		return 0, fmt.Errorf("unsupported msgpack format byte %#x", c)
	}
	if size > 0 {
		if len(b) < size {
			return 0, errors.New("truncated value")
		}
		return size, nil
	}
	if len(b) < head {
		return 0, errors.New("truncated value")
	}
	// A negative n or items is the width of the big-endian length field
	// that follows the format byte.
	readLen := func(width int) int {
		switch width {
		case 1:
			return int(b[1])
		case 2:
			return int(binary.BigEndian.Uint16(b[1:3]))
		default:
			return int(binary.BigEndian.Uint32(b[1:5]))
		}
	}
	if n < 0 {
		size = head + readLen(-n)
		if len(b) < size {
			return 0, errors.New("truncated value")
		}
		return size, nil
	}
	if items < 0 {
		items = readLen(-items)
		if c == 0xde || c == 0xdf {
			items *= 2
		}
	}
	pos := head
	for range items {
		l, err := msgpackSkip(b[pos:])
		if err != nil {
			return 0, err
		}
		pos += l
	}
	return pos, nil
}

// ---- cosmos ----

// cosmosSignDirectHash is sha256 over the protobuf TxRaw built from the
// signed body, auth info and signature, upper-case hex as explorers show it.
func cosmosSignDirectHash(result json.RawMessage) (string, error) {
	var r struct {
		Signed struct {
			BodyBytes     string `json:"bodyBytes"`
			AuthInfoBytes string `json:"authInfoBytes"`
		} `json:"signed"`
		Signature struct {
			Signature string `json:"signature"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return "", err
	}
	var raw []byte
	for i, field := range []string{r.Signed.BodyBytes, r.Signed.AuthInfoBytes, r.Signature.Signature} {
		b, err := decodeBase64(field)
		if err != nil {
			return "", err
		}
		raw = append(raw, byte(0x0a+8*i)) // field numbers 1, 2, 3, wire type 2
		raw = binary.AppendUvarint(raw, uint64(len(b)))
		raw = append(raw, b...)
	}
	sum := sha256.Sum256(raw)
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
}

// ---- stellar ----

var stellarPassphrases = map[string]string{
	"pubnet":  "Public Global Stellar Network ; September 2015",
	"testnet": "Test SDF Network ; September 2015",
}

const (
	stellarEnvelopeTxV0     = 0
	stellarEnvelopeTx       = 2
	stellarEnvelopeFeeBump  = 5
	stellarDecoratedSigLen  = 72 // hint (4) + length (4) + ed25519 signature (64)
	stellarEd25519SigLen    = 64
	stellarMaxEnvelopeSigs  = 20
	stellarEnvelopeMinBytes = 8
)

// stellarTransactionHash is sha256 over the network id, the envelope type
// and the transaction body of a signed envelope, which is what the network
// signs and explorers index. chain is a CAIP-2 id such as stellar:pubnet.
func stellarTransactionHash(signedXDR, chain string) (string, error) {
	b, err := decodeBase64(signedXDR)
	if err != nil {
		return "", err
	}
	if len(b) < stellarEnvelopeMinBytes {
		return "", errors.New("stellar: envelope too short")
	}
	var envelopeType uint32
	var bodyStart int
	switch binary.BigEndian.Uint32(b[0:4]) {
	case stellarEnvelopeTxV0:
		// V0 is hashed as ENVELOPE_TYPE_TX over the bytes including the
		// leading zeros, which double as the legacy account key type.
		envelopeType, bodyStart = stellarEnvelopeTx, 0
	case stellarEnvelopeTx:
		envelopeType, bodyStart = stellarEnvelopeTx, 4
	case stellarEnvelopeFeeBump:
		envelopeType, bodyStart = stellarEnvelopeFeeBump, 4
	default:
		return "", fmt.Errorf("stellar: unsupported envelope type %d", binary.BigEndian.Uint32(b[0:4]))
	}
	sigs, err := stellarSignatureArrayOffset(b)
	if err != nil {
		return "", err
	}
	network := "pubnet"
	if i := strings.LastIndexByte(chain, ':'); i >= 0 {
		network = chain[i+1:]
	} else if chain != "" {
		network = chain
	}
	passphrase, ok := stellarPassphrases[network]
	if !ok {
		return "", fmt.Errorf("stellar: unknown network %q", network)
	}
	networkID := sha256.Sum256([]byte(passphrase))
	payload := append(networkID[:], 0, 0, 0, byte(envelopeType))
	payload = append(payload, b[bodyStart:sigs]...)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// stellarSignatureArrayOffset finds the trailing DecoratedSignature array
// without parsing the body, assuming ed25519 signatures. It scans from the
// largest count down so a signature ending in four zero bytes is not taken
// for an empty array.
func stellarSignatureArrayOffset(b []byte) (int, error) {
	for count := stellarMaxEnvelopeSigs; count >= 0; count-- {
		offset := len(b) - 4 - stellarDecoratedSigLen*count
		if offset < 4 {
			continue
		}
		if binary.BigEndian.Uint32(b[offset:offset+4]) != uint32(count) {
			continue
		}
		valid := true
		for i := 0; i < count; i++ {
			entry := offset + 4 + stellarDecoratedSigLen*i
			if binary.BigEndian.Uint32(b[entry+4:entry+8]) != stellarEd25519SigLen {
				valid = false
				break
			}
		}
		if valid {
			return offset, nil
		}
	}
	return 0, errors.New("stellar: could not locate the envelope signature array")
}

// ---- polkadot ----

type polkadotPayload struct {
	Method  string          `json:"method"`
	Era     string          `json:"era"`
	Nonce   string          `json:"nonce"`
	Tip     string          `json:"tip"`
	Address string          `json:"address"`
	Version json.RawMessage `json:"version"`
}

// polkadotExtrinsicHash rebuilds the signed extrinsic from the payload the
// dApp sent and the signature the wallet returned, and hashes it with
// blake2b-256 the way the chain derives an extrinsic hash.
func polkadotExtrinsicHash(p polkadotPayload, signatureHex string) (string, error) {
	signature, err := decodeHex(signatureHex)
	if err != nil {
		return "", err
	}
	address, err := base58.Decode(p.Address)
	if err != nil {
		return "", err
	}
	if len(address) < 33 {
		return "", errors.New("polkadot: address too short to contain a public key")
	}
	publicKey := address[1:33]
	var signatureType byte = 0x01 // sr25519
	switch address[0] {
	case 42:
		signatureType = 0x00 // ed25519
	case 60:
		signatureType = 0x02 // secp256k1
	}
	method, err := decodeHex(p.Method)
	if err != nil {
		return "", err
	}
	version := 4
	if len(p.Version) > 0 {
		var v float64
		if err := json.Unmarshal(p.Version, &v); err == nil {
			version = int(v)
		} else {
			var s string
			if json.Unmarshal(p.Version, &s) == nil {
				if n, err := strconv.Atoi(s); err == nil {
					version = n
				}
			}
		}
	}
	era := []byte{0x00}
	if p.Era != "00" {
		if era, err = decodeHex(p.Era); err != nil {
			return "", err
		}
		if len(era) != 1 && len(era) != 2 {
			return "", errors.New("polkadot: invalid era length")
		}
	}
	nonce, err := strconv.ParseUint(strings.TrimPrefix(p.Nonce, "0x"), 16, 64)
	if err != nil {
		return "", err
	}
	tip, ok := new(big.Int).SetString(strings.TrimPrefix(p.Tip, "0x"), 16)
	if !ok {
		return "", errors.New("polkadot: invalid tip")
	}
	tipBytes, err := scaleCompact(tip)
	if err != nil {
		return "", err
	}

	body := []byte{0x00} // MultiAddress::Id
	body = append(body, publicKey...)
	body = append(body, signatureType)
	body = append(body, signature...)
	body = append(body, era...)
	body = append(body, byte(nonce&0xff), byte((nonce>>8)&0xff))
	body = append(body, tipBytes...)
	body = append(body, method...)

	prefix, err := scaleCompact(big.NewInt(int64(len(body) + 1)))
	if err != nil {
		return "", err
	}
	extrinsic := append(prefix, 0x80|byte(version))
	extrinsic = append(extrinsic, body...)
	sum := blake2b.Sum256(extrinsic)
	return "0x" + hex.EncodeToString(sum[:]), nil
}

// scaleCompact is the SCALE compact encoding of an integer below 2^30.
func scaleCompact(v *big.Int) ([]byte, error) {
	if v.Sign() < 0 {
		return nil, errors.New("polkadot: negative compact value")
	}
	n := v.Uint64()
	switch {
	case !v.IsUint64() || n >= 1<<30:
		return nil, errors.New("polkadot: compact encoding above 2^30 is not supported")
	case n < 1<<6:
		return []byte{byte(n << 2)}, nil
	case n < 1<<14:
		x := n<<2 | 0x01
		return []byte{byte(x), byte(x >> 8)}, nil
	default:
		x := n<<2 | 0x02
		return []byte{byte(x), byte(x >> 8), byte(x >> 16), byte(x >> 24)}, nil
	}
}

// ---- EIP-5792 ----

// walletSendCallsHashes is the bundle id and, when the wallet reports them
// through the CAIP-345 capability, the transaction hashes.
func walletSendCallsHashes(result json.RawMessage) ([]string, error) {
	var s string
	if err := json.Unmarshal(result, &s); err == nil {
		return nonEmpty(s), nil
	}
	var r struct {
		ID           string `json:"id"`
		Capabilities struct {
			CAIP345 struct {
				TransactionHashes []string `json:"transactionHashes"`
			} `json:"caip345"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return nil, err
	}
	var out []string
	if r.ID != "" {
		out = append(out, r.ID)
	}
	return append(out, r.Capabilities.CAIP345.TransactionHashes...), nil
}

// ---- canton ----

// cantonSupportedChoices are the Daml choices whose transfer amount is
// readable from the request itself.
var cantonSupportedChoices = map[string]bool{
	"TransferFactory_Transfer":               true,
	"DelegateProxy_TransferFactory_Transfer": true,
}

// cantonTransactionHashes is the ledger update id, paired with each inline
// transfer's amount and instrument as "updateId:amount:instrument" so the
// value can be approximated downstream, or the bare update id when the
// request carries no readable transfer.
func cantonTransactionHashes(params, result json.RawMessage) ([]string, error) {
	var r struct {
		Payload struct {
			UpdateID json.RawMessage `json:"updateId"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(result, &r); err != nil {
		return nil, err
	}
	updateID := jsonScalar(r.Payload.UpdateID)
	if updateID == "" {
		return nil, nil
	}
	var p struct {
		Commands []struct {
			ExerciseCommand *struct {
				Choice         string `json:"choice"`
				ChoiceArgument struct {
					Transfer struct {
						Amount       json.RawMessage `json:"amount"`
						InstrumentID struct {
							ID json.RawMessage `json:"id"`
						} `json:"instrumentId"`
					} `json:"transfer"`
				} `json:"choiceArgument"`
			} `json:"ExerciseCommand"`
		} `json:"commands"`
	}
	_ = json.Unmarshal(params, &p)
	var out []string
	for _, cmd := range p.Commands {
		ex := cmd.ExerciseCommand
		if ex == nil || !cantonSupportedChoices[ex.Choice] {
			continue
		}
		amount, instrument := jsonScalar(ex.ChoiceArgument.Transfer.Amount), jsonScalar(ex.ChoiceArgument.Transfer.InstrumentID.ID)
		if amount == "" || instrument == "" {
			continue
		}
		out = append(out, updateID+":"+amount+":"+instrument)
	}
	if len(out) == 0 {
		return []string{updateID}, nil
	}
	return out, nil
}

// jsonScalar is a JSON string's value, or a number's text, or "".
func jsonScalar(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}
