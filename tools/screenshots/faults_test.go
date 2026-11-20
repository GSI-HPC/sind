// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"testing"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
)

// faultsOf returns the error of a tab that reported the events.
func faultsOf(events ...any) string {
	var f faults
	for _, ev := range events {
		f.event(ev)
	}
	if err := f.err(); err != nil {
		return err.Error()
	}
	return ""
}

func TestFaults(t *testing.T) {
	text := func(s string) *runtime.RemoteObject { return &runtime.RemoteObject{Value: []byte(`"` + s + `"`)} }
	sent := func(id, method, url string) *network.EventRequestWillBeSent {
		return &network.EventRequestWillBeSent{RequestID: network.RequestID(id), Request: &network.Request{Method: method, URL: url}}
	}
	got := func(id, url string, status int64) *network.EventResponseReceived {
		return &network.EventResponseReceived{RequestID: network.RequestID(id), Response: &network.Response{URL: url, Status: status}}
	}
	const base = "http://127.0.0.1:41000"

	for _, tc := range []struct {
		name   string
		events []any
		want   string
	}{
		{"a page without a fault", []any{
			sent("1", "GET", base+"/clusters"), got("1", base+"/clusters", 200),
			&runtime.EventConsoleAPICalled{Type: runtime.ConsoleAPICalledTypeLog, Args: []*runtime.RemoteObject{text("ready")}},
			&runtime.EventConsoleAPICalled{Type: runtime.ConsoleAPICalledTypeWarning, Args: []*runtime.RemoteObject{text("slow")}},
		}, ""},
		{"an exception with its place", []any{&runtime.EventExceptionThrown{ExceptionDetails: &runtime.ExceptionDetails{
			Text: "Uncaught", URL: base + "/static/app.js", LineNumber: 11,
			Exception: &runtime.RemoteObject{Description: "TypeError: x is not a function\n    at app.js:12:3"},
		}}}, "the page reports uncaught exception: TypeError: x is not a function (/static/app.js:12)"},
		{"an exception without an object and a place", []any{&runtime.EventExceptionThrown{ExceptionDetails: &runtime.ExceptionDetails{
			Text: "Uncaught SyntaxError: Unexpected token",
		}}}, "the page reports uncaught exception: Uncaught SyntaxError: Unexpected token"},
		{"console.error with a string, a number and an element", []any{&runtime.EventConsoleAPICalled{
			Type: runtime.ConsoleAPICalledTypeError,
			Args: []*runtime.RemoteObject{text("htmx:oobErrorNoTarget"), {Value: []byte(`3`)}, {Description: "span#act-copy-foot"}},
		}}, "the page reports console.error: htmx:oobErrorNoTarget 3 span#act-copy-foot"},
		{"console.assert", []any{&runtime.EventConsoleAPICalled{
			Type: runtime.ConsoleAPICalledTypeAssert, Args: []*runtime.RemoteObject{text("no row")},
		}}, "the page reports console.assert: no row"},
		{"a response with a status of 400 or more", []any{
			sent("1", "GET", base+"/static/none.css?v=1"), got("1", base+"/static/none.css?v=1", 404),
			sent("2", "POST", base+"/actions/power/preview"), got("2", base+"/actions/power/preview", 500),
			got("3", base+"/favicon.ico", 400),
			got("4", base+"/ok", 399),
		}, "the page reports GET /static/none.css?v=1: status 404; POST /actions/power/preview: status 500; /favicon.ico: status 400"},
		{"a request that fails", []any{
			sent("1", "GET", base+"/events"),
			&network.EventLoadingFailed{RequestID: "1", ErrorText: "net::ERR_CONNECTION_REFUSED"},
			&network.EventLoadingFailed{RequestID: "2", ErrorText: "net::ERR_FAILED"},
		}, "the page reports GET /events: net::ERR_CONNECTION_REFUSED; a request: net::ERR_FAILED"},
		{"a request that the page ends", []any{
			sent("1", "GET", base+"/events"),
			&network.EventLoadingFailed{RequestID: "1", ErrorText: "net::ERR_ABORTED", Canceled: true},
		}, ""},
		{"a fault that comes twice", []any{
			&runtime.EventConsoleAPICalled{Type: runtime.ConsoleAPICalledTypeError, Args: []*runtime.RemoteObject{text("again")}},
			&runtime.EventConsoleAPICalled{Type: runtime.ConsoleAPICalledTypeError, Args: []*runtime.RemoteObject{text("again")}},
		}, "the page reports console.error: again"},
	} {
		if got := faultsOf(tc.events...); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

func TestPathOf(t *testing.T) {
	for address, want := range map[string]string{
		"http://127.0.0.1:41000/clusters/new?from=x": "/clusters/new?from=x",
		"http://127.0.0.1:41000":                     "/",
		"data:image/png;base64,AAAA":                 "data:image/png;base64,AAAA",
		"/static/app.js":                             "/static/app.js",
		"http://[::1":                                "http://[::1",
	} {
		if got := pathOf(address); got != want {
			t.Errorf("pathOf(%q) is %q, want %q", address, got, want)
		}
	}
}
