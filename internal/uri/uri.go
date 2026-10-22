// Package uri formats and parses wc: pairing URIs.
package uri

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Params struct {
	Topic           string
	Version         int
	SymKey          string
	Relay           map[string]string
	ExpiryTimestamp int64
	Methods         []string
}

var ErrInvalid = errors.New("uri: invalid pairing uri")

// Format builds wc:<topic>@<version>?<sorted query>.
func Format(p Params) string {
	q := url.Values{}
	for k, v := range p.Relay {
		if v != "" {
			q.Set("relay-"+k, v)
		}
	}
	q.Set("symKey", p.SymKey)
	if p.ExpiryTimestamp != 0 {
		q.Set("expiryTimestamp", strconv.FormatInt(p.ExpiryTimestamp, 10))
	}
	if len(p.Methods) > 0 {
		q.Set("methods", strings.Join(p.Methods, ","))
	}
	return fmt.Sprintf("wc:%s@%d?%s", p.Topic, p.Version, q.Encode())
}

// Parse accepts wc:, wc:// and base64-wrapped URIs.
func Parse(s string) (Params, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "wc:") {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil && strings.Contains(string(b), "wc:") {
			s = string(b)
		}
	}
	switch {
	case strings.HasPrefix(s, "wc://"):
		s = strings.TrimPrefix(s, "wc://")
	case strings.HasPrefix(s, "wc:"):
		s = strings.TrimPrefix(s, "wc:")
	default:
		return Params{}, fmt.Errorf("%w: missing wc: scheme", ErrInvalid)
	}
	path, rawQuery, _ := strings.Cut(s, "?")
	path = strings.TrimPrefix(path, "//")
	topic, versionStr, ok := strings.Cut(path, "@")
	if !ok || topic == "" {
		return Params{}, fmt.Errorf("%w: missing topic or version", ErrInvalid)
	}
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return Params{}, fmt.Errorf("%w: version %q", ErrInvalid, versionStr)
	}
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return Params{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	p := Params{Topic: topic, Version: version, SymKey: q.Get("symKey"), Relay: map[string]string{}}
	for k := range q {
		if name, ok := strings.CutPrefix(k, "relay-"); ok {
			p.Relay[name] = q.Get(k)
		}
	}
	if v := q.Get("expiryTimestamp"); v != "" {
		if p.ExpiryTimestamp, err = strconv.ParseInt(v, 10, 64); err != nil {
			return Params{}, fmt.Errorf("%w: expiryTimestamp %q", ErrInvalid, v)
		}
	}
	if v := q.Get("methods"); v != "" {
		p.Methods = strings.Split(v, ",")
	}
	return p, nil
}
