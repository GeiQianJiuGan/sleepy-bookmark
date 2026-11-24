package source

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Kinds of failure, sent to the client as the error code so that it can decide between
// retrying, switching source and asking for a rule fix without parsing the message.
const (
	KindTimeout  = "timeout"   // no answer in time
	KindNetwork  = "network"   // DNS, connection refused or reset, TLS
	KindBusy     = "busy"      // 429 and 5xx: the site is up but not serving right now
	KindBlocked  = "blocked"   // 403 or an anti-bot challenge page
	KindNotFound = "not_found" // 404 / 410
	KindHTTP     = "http"      // any other error status
	KindRule     = "rule"      // the page loaded but the rule matched nothing
)

// FetchError is a failed request to a source site. Its message is meant for the reader and
// never contains the URL.
type FetchError struct {
	Kind   string
	Status int
	Err    error
}

func (e *FetchError) Error() string {
	switch e.Kind {
	case KindTimeout:
		return "书源响应超时"
	case KindNetwork:
		return "无法连接书源站点"
	case KindBusy:
		return fmt.Sprintf("书源站点暂时不可用（HTTP %d）", e.Status)
	case KindBlocked:
		return "书源站点开启了访问验证，暂时无法抓取"
	case KindNotFound:
		return "书源页面不存在，可能已被删除或地址已变"
	}
	return fmt.Sprintf("书源返回 HTTP %d", e.Status)
}

func (e *FetchError) Unwrap() error { return e.Err }

// Temporary failures are worth retrying after a short pause.
func (e *FetchError) Temporary() bool {
	return e.Kind == KindTimeout || e.Kind == KindNetwork || e.Kind == KindBusy
}

// RuleError means the site answered but the source rule did not find what it should.
type RuleError string

func (e RuleError) Error() string { return string(e) }

// Kind classifies an error returned by the engine; it is "" for errors that are not about a source.
func Kind(err error) string {
	var fe *FetchError
	if errors.As(err, &fe) {
		return fe.Kind
	}
	var re RuleError
	if errors.As(err, &re) {
		return KindRule
	}
	return ""
}

// Unreachable reports whether the source site itself failed, as opposed to one page or one rule.
func Unreachable(err error) bool {
	switch Kind(err) {
	case KindTimeout, KindNetwork, KindBusy, KindBlocked:
		return true
	}
	return false
}

func transportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &FetchError{Kind: KindTimeout, Err: err}
	}
	return &FetchError{Kind: KindNetwork, Err: err}
}

func statusError(status int, challenged bool) error {
	kind := KindHTTP
	switch {
	case challenged || status == 403:
		kind = KindBlocked
	case status == 404 || status == 410:
		kind = KindNotFound
	case status == 429 || status >= 500:
		kind = KindBusy
	}
	return &FetchError{Kind: kind, Status: status}
}
