package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/evm"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/server"
)

type topicReq struct {
	Topic string `json:"topic"`
}

type connectReq struct {
	Chains  []string `json:"chains"`
	Methods []string `json:"methods"`
	Events  []string `json:"events"`
}

type requestReq struct {
	Topic   string `json:"topic"`
	ChainID string `json:"chainId"`
	Method  string `json:"method"`
	// Account is the address to sign with; empty means the first one the
	// session has on ChainID.
	Account string `json:"account"`
}

// signedResult is a wallet's answer with the signer recovered from it.
type signedResult struct {
	Response json.RawMessage `json:"response"`
	Signer   string          `json:"signer,omitempty"`
	Verified *bool           `json:"verified,omitempty"`
}

func registerAPI(srv *server.Server, c *wc.Client) {
	bg := context.Background()
	requests := newRequestLog()

	srv.Handle("GET /api/state", func(r *http.Request) (any, error) {
		sessions, err := c.Sessions(r.Context())
		if err != nil {
			return nil, err
		}
		proposals, err := c.Proposals(r.Context())
		live := map[string]bool{}
		for _, s := range sessions {
			live[s.Topic] = true
		}
		requests.prune(live)
		return map[string]any{"clientId": c.ClientID(), "sessions": sessions, "proposals": proposals,
			"requests": requests.snapshot()}, err
	})

	srv.Handle("POST /api/connect", func(r *http.Request) (any, error) {
		in, err := server.Decode[connectReq](r)
		if err != nil {
			return nil, err
		}
		if len(in.Chains) == 0 {
			return nil, fmt.Errorf("%w: pick at least one chain", wc.ErrInvalidParams)
		}
		conn, err := c.Connect(r.Context(), wc.ConnectParams{OptionalNamespaces: map[string]wc.ProposalNamespace{
			"eip155": {Chains: in.Chains, Methods: in.Methods, Events: in.Events},
		}})
		if err != nil {
			return nil, err
		}
		png, err := qrcode.Encode(conn.URI, qrcode.Medium, 320)
		if err != nil {
			return nil, err
		}
		srv.Async("connect_result", map[string]any{"proposalId": conn.ProposalID}, func() (any, error) {
			s, err := conn.Wait(bg)
			if errors.Is(err, wc.ErrSessionDeleted) {
				return map[string]bool{"cancelled": true}, nil
			}
			if err != nil {
				return nil, err
			}
			return map[string]string{"topic": s.Topic, "wallet": s.Peer.Metadata.Name}, nil
		})
		return map[string]any{
			"uri": conn.URI, "proposalId": conn.ProposalID, "expiry": conn.Expiry, "pairingTopic": conn.PairingTopic,
			"qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		}, nil
	})

	srv.Handle("POST /api/request", func(r *http.Request) (any, error) {
		in, err := server.Decode[requestReq](r)
		if err != nil {
			return nil, err
		}
		session, err := findSession(r.Context(), c, in.Topic)
		if err != nil {
			return nil, err
		}
		account, err := accountFor(session, in.ChainID, in.Account)
		if err != nil {
			return nil, err
		}
		chain, err := evm.ChainNumber(in.ChainID)
		if err != nil {
			return nil, err
		}
		params, err := evm.RequestParams(in.Method, account, chain)
		if err != nil {
			return nil, err
		}
		entry := requests.start(in.Topic, in.Method, in.ChainID, account)
		go func() {
			raw, err := c.Request(bg, wc.RequestParams{Topic: in.Topic, ChainID: in.ChainID, Method: in.Method, Params: params})
			if err != nil {
				msg, code := server.ErrorDetails(err)
				srv.Publish("request_result", requests.finish(in.Topic, entry.LocalID, nil, msg, code))
				return
			}
			result := verify(in.Method, params, raw, account)
			srv.Publish("request_result", requests.finish(in.Topic, entry.LocalID, &result, "", 0))
		}()
		return entry, nil
	})

	srv.Handle("POST /api/ping", func(r *http.Request) (any, error) {
		in, err := server.Decode[topicReq](r)
		if err != nil {
			return nil, err
		}
		if _, err := findSession(r.Context(), c, in.Topic); err != nil {
			return nil, err
		}
		srv.Async("ping_result", map[string]any{"topic": in.Topic}, func() (any, error) {
			start := time.Now()
			if err := c.Ping(bg, in.Topic); err != nil {
				return nil, err
			}
			return map[string]string{"roundTrip": time.Since(start).Round(time.Millisecond).String()}, nil
		})
		return map[string]bool{"sent": true}, nil
	})

	srv.Handle("POST /api/extend", func(r *http.Request) (any, error) {
		in, err := server.Decode[topicReq](r)
		if err != nil {
			return nil, err
		}
		ack, err := c.Extend(r.Context(), in.Topic)
		if err != nil {
			return nil, err
		}
		srv.Async("extend_ack", map[string]any{"topic": in.Topic}, func() (any, error) { return "acknowledged", ack.Wait(bg) })
		return map[string]bool{"sent": true}, nil
	})

	srv.Handle("POST /api/disconnect", func(r *http.Request) (any, error) {
		in, err := server.Decode[topicReq](r)
		if err != nil {
			return nil, err
		}
		if err := c.Disconnect(r.Context(), in.Topic); err != nil {
			return nil, err
		}
		srv.Publish("session_disconnected", map[string]any{"topic": in.Topic})
		return map[string]bool{"disconnected": true}, nil
	})
}

func findSession(ctx context.Context, c *wc.Client, topic string) (wc.Session, error) {
	if topic == "" {
		return wc.Session{}, fmt.Errorf("%w: topic is required", wc.ErrInvalidParams)
	}
	sessions, err := c.Sessions(ctx)
	if err != nil {
		return wc.Session{}, err
	}
	i := slices.IndexFunc(sessions, func(s wc.Session) bool { return s.Topic == topic })
	if i < 0 {
		return wc.Session{}, fmt.Errorf("session %s: %w", topic, wc.ErrNotFound)
	}
	return sessions[i], nil
}

// accountFor returns want if the session has it on chainID, or the session's
// first address on chainID if want is empty.
func accountFor(s wc.Session, chainID, want string) (string, error) {
	for _, ns := range s.Namespaces {
		for _, a := range ns.Accounts {
			if !strings.HasPrefix(a, chainID+":") {
				continue
			}
			addr := evm.AccountAddress(a)
			if want == "" || strings.EqualFold(addr, want) {
				return addr, nil
			}
		}
	}
	if want != "" {
		return "", fmt.Errorf("%w: %s is not a session account on %s", wc.ErrInvalidParams, want, chainID)
	}
	return "", fmt.Errorf("%w: no account on %s", wc.ErrInvalidParams, chainID)
}

// verify recovers the signer of a signing request's result.
func verify(method string, params, raw json.RawMessage, account string) signedResult {
	out := signedResult{Response: raw}
	var sig string
	if json.Unmarshal(raw, &sig) != nil {
		return out
	}
	signer, err := evm.Recover(method, params, sig)
	if err != nil {
		return out
	}
	ok := strings.EqualFold(signer.Hex(), account)
	out.Signer, out.Verified = signer.Hex(), &ok
	return out
}
