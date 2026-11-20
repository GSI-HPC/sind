// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
)

// faults collects what a page does wrong while a shot is taken: an
// exception that no script catches, a call of console.error or
// console.assert, a request that fails and a response with a status of 400
// or more. A shot of such a page does not show sind ui as it is meant, so
// the shot fails with them.
type faults struct {
	mu sync.Mutex
	// requests are the requests of the tab that have no response yet, as
	// the method and the address.
	requests map[network.RequestID]string
	list     []string
}

// event is the listener of a tab. It keeps the events that are faults,
// and returns fast, as a listener must.
func (f *faults) event(ev any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch e := ev.(type) {
	case *runtime.EventExceptionThrown:
		f.add("uncaught exception: " + exceptionText(e.ExceptionDetails))
	case *runtime.EventConsoleAPICalled:
		if e.Type != runtime.ConsoleAPICalledTypeError && e.Type != runtime.ConsoleAPICalledTypeAssert {
			return
		}
		args := make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = objectText(a)
		}
		f.add("console." + string(e.Type) + ": " + strings.Join(args, " "))
	case *network.EventRequestWillBeSent:
		if f.requests == nil {
			f.requests = map[network.RequestID]string{}
		}
		f.requests[e.RequestID] = e.Request.Method + " " + pathOf(e.Request.URL)
	case *network.EventResponseReceived:
		if e.Response.Status >= 400 {
			f.add(fmt.Sprintf("%s: status %d", f.request(e.RequestID, e.Response.URL), e.Response.Status))
		}
	case *network.EventLoadingFailed:
		// A request that the page itself ends, as a live stream when the
		// tab closes, is no fault.
		if !e.Canceled {
			f.add(fmt.Sprintf("%s: %s", f.request(e.RequestID, ""), e.ErrorText))
		}
	}
}

// add keeps a fault once.
func (f *faults) add(fault string) {
	if !slices.Contains(f.list, fault) {
		f.list = append(f.list, fault)
	}
}

// request names the request with the ID: its method and address, or the
// address alone when the tab did not report the request.
func (f *faults) request(id network.RequestID, address string) string {
	if r, ok := f.requests[id]; ok {
		return r
	}
	if address == "" {
		return "a request"
	}
	return pathOf(address)
}

// err returns the faults of the page as one error, nil for none.
func (f *faults) err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.list) == 0 {
		return nil
	}
	return errors.New("the page reports " + strings.Join(f.list, "; "))
}

// pathOf returns the address without its scheme and host, which change
// from run to run with the port of sind ui.
func pathOf(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return address
	}
	return u.RequestURI()
}

// exceptionText returns the message of an exception and where it was
// thrown.
func exceptionText(d *runtime.ExceptionDetails) string {
	text := d.Text
	if d.Exception != nil {
		// The description of an Error is its message and its stack.
		if first, _, _ := strings.Cut(objectText(d.Exception), "\n"); first != "" {
			text = first
		}
	}
	if d.URL == "" {
		return text
	}
	return fmt.Sprintf("%s (%s:%d)", text, pathOf(d.URL), d.LineNumber+1)
}

// objectText returns a value of the page as text: a string as it is, any
// other JSON value as JSON, and an object by its description.
func objectText(o *runtime.RemoteObject) string {
	if len(o.Value) == 0 {
		return o.Description
	}
	var s string
	if json.Unmarshal(o.Value, &s) == nil {
		return s
	}
	return string(o.Value)
}
