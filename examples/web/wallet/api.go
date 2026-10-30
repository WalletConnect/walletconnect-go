package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	wc "github.com/WalletConnect/walletconnect-go"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/evm"
	"github.com/WalletConnect/walletconnect-go/examples/web/internal/server"
)

type pairReq struct {
	URI string `json:"uri"`
}

type proposalReq struct {
	ProposalID int64 `json:"proposalId"`
}

type respondReq struct {
	Topic   string `json:"topic"`
	ID      int64  `json:"id"`
	Approve bool   `json:"approve"`
}

type chainReq struct {
	Topic   string `json:"topic"`
	ChainID string `json:"chainId"`
	Name    string `json:"name"`
}

type topicReq struct {
	Topic string `json:"topic"`
}

// errUnsupportedMethod is the EIP-1193 code for a method the wallet lacks.
var errUnsupportedMethod = &wc.RPCError{Code: 4200, Message: "Unsupported method"}

func registerAPI(srv *server.Server, c *wc.Client, signer *evm.Signer) {
	bg := context.Background()
	address := signer.Address.Hex()

	srv.Handle("GET /api/state", func(r *http.Request) (any, error) {
		sessions, err := c.Sessions(r.Context())
		if err != nil {
			return nil, err
		}
		proposals, err := c.Proposals(r.Context())
		if err != nil {
			return nil, err
		}
		requests, err := c.PendingRequests(r.Context())
		return map[string]any{"address": address, "clientId": c.ClientID(),
			"sessions": sessions, "proposals": proposals, "requests": requests}, err
	})

	srv.Handle("POST /api/pair", func(r *http.Request) (any, error) {
		in, err := server.Decode[pairReq](r)
		if err != nil {
			return nil, err
		}
		return map[string]bool{"paired": true}, c.Pair(r.Context(), in.URI)
	})

	srv.Handle("POST /api/approve", func(r *http.Request) (any, error) {
		in, err := server.Decode[proposalReq](r)
		if err != nil {
			return nil, err
		}
		proposal, err := findProposal(r.Context(), c, in.ProposalID)
		if err != nil {
			return nil, err
		}
		ns, err := approvedNamespaces(proposal, address)
		if err != nil {
			return nil, err
		}
		approval, err := c.Approve(r.Context(), wc.ApproveParams{ProposalID: in.ProposalID, Namespaces: ns,
			SessionProperties: proposal.SessionProperties, ScopedProperties: proposal.ScopedProperties})
		if err != nil {
			return nil, err
		}
		srv.Publish("proposal_answered", map[string]any{"proposalId": in.ProposalID, "answer": "approved"})
		srv.Async("approve_ack", map[string]any{"topic": approval.Session.Topic}, func() (any, error) {
			return "acknowledged", approval.Acknowledged(bg)
		})
		return map[string]string{"topic": approval.Session.Topic}, nil
	})

	srv.Handle("POST /api/reject", func(r *http.Request) (any, error) {
		in, err := server.Decode[proposalReq](r)
		if err != nil {
			return nil, err
		}
		if err := c.Reject(r.Context(), in.ProposalID, nil); err != nil {
			return nil, err
		}
		srv.Publish("proposal_answered", map[string]any{"proposalId": in.ProposalID, "answer": "rejected"})
		return map[string]bool{"rejected": true}, nil
	})

	srv.Handle("POST /api/respond", func(r *http.Request) (any, error) {
		in, err := server.Decode[respondReq](r)
		if err != nil {
			return nil, err
		}
		answer, err := respond(r.Context(), c, signer, in)
		if err != nil {
			return nil, err
		}
		srv.Publish("request_answered", map[string]any{"id": in.ID, "topic": in.Topic, "answer": answer})
		return map[string]string{"answer": answer}, nil
	})

	srv.Handle("POST /api/emit", func(r *http.Request) (any, error) {
		in, err := server.Decode[chainReq](r)
		if err != nil {
			return nil, err
		}
		var data any = []string{address}
		if in.Name == "chainChanged" {
			if data, err = evm.ChainNumber(in.ChainID); err != nil {
				return nil, err
			}
		}
		return map[string]bool{"emitted": true}, c.Emit(r.Context(), in.Topic, in.ChainID, in.Name, data)
	})

	srv.Handle("POST /api/update", func(r *http.Request) (any, error) {
		in, err := server.Decode[chainReq](r)
		if err != nil {
			return nil, err
		}
		session, err := findSession(r.Context(), c, in.Topic)
		if err != nil {
			return nil, err
		}
		ack, err := c.Update(r.Context(), in.Topic, withChain(session, in.ChainID, address))
		if err != nil {
			return nil, err
		}
		srv.Async("update_ack", map[string]any{"topic": in.Topic}, func() (any, error) { return "acknowledged", ack.Wait(bg) })
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

	srv.Handle("POST /api/ping", func(r *http.Request) (any, error) {
		in, err := server.Decode[topicReq](r)
		if err != nil {
			return nil, err
		}
		if _, err := findSession(r.Context(), c, in.Topic); err != nil {
			return nil, err
		}
		srv.Async("ping_result", map[string]any{"topic": in.Topic}, func() (any, error) { return "pong", c.Ping(bg, in.Topic) })
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

// respond answers a request: signed, rejected by the user, or unsupported.
// It returns a short description of the answer.
func respond(ctx context.Context, c *wc.Client, signer *evm.Signer, in respondReq) (string, error) {
	if !in.Approve {
		return "rejected", c.RespondError(ctx, in.Topic, in.ID, wc.SDKError(wc.ErrKeyUserRejected, ""))
	}
	req, err := findRequest(ctx, c, in.ID)
	if err != nil {
		return "", err
	}
	sig, signErr := signer.Sign(req.Method, req.Params)
	switch {
	case errors.Is(signErr, evm.ErrUnsupported):
		return "unsupported", c.RespondError(ctx, in.Topic, in.ID, errUnsupportedMethod)
	case signErr != nil:
		return signErr.Error(), c.RespondError(ctx, in.Topic, in.ID, &wc.RPCError{Code: -32602, Message: signErr.Error()})
	}
	return sig, c.Respond(ctx, in.Topic, in.ID, sig)
}

func findProposal(ctx context.Context, c *wc.Client, id int64) (wc.Proposal, error) {
	list, err := c.Proposals(ctx)
	if err != nil {
		return wc.Proposal{}, err
	}
	i := slices.IndexFunc(list, func(p wc.Proposal) bool { return p.ID == id })
	if i < 0 {
		return wc.Proposal{}, fmt.Errorf("proposal %d: %w", id, wc.ErrNotFound)
	}
	return list[i], nil
}

func findRequest(ctx context.Context, c *wc.Client, id int64) (wc.PendingRequest, error) {
	list, err := c.PendingRequests(ctx)
	if err != nil {
		return wc.PendingRequest{}, err
	}
	i := slices.IndexFunc(list, func(p wc.PendingRequest) bool { return p.ID == id })
	if i < 0 {
		return wc.PendingRequest{}, fmt.Errorf("request %d: %w", id, wc.ErrNotFound)
	}
	return list[i], nil
}

func findSession(ctx context.Context, c *wc.Client, topic string) (wc.Session, error) {
	if topic == "" {
		return wc.Session{}, fmt.Errorf("%w: topic is required", wc.ErrInvalidParams)
	}
	list, err := c.Sessions(ctx)
	if err != nil {
		return wc.Session{}, err
	}
	i := slices.IndexFunc(list, func(s wc.Session) bool { return s.Topic == topic })
	if i < 0 {
		return wc.Session{}, fmt.Errorf("session %s: %w", topic, wc.ErrNotFound)
	}
	return list[i], nil
}
