// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// A browser opens at most six HTTP/1.1 connections to sind ui, for all its
// tabs together, and each page holds one for its live stream. A page that
// is hidden lets its stream go after a moment (app.js), so that the next
// tab can load. tabsHeld is the number of pages that take every connection,
// and tabsMore the number of tabs that the check opens after them.
const (
	tabsHeld = 6
	tabsMore = 2
)

// tabLoad is how long a tab may take to show its page and open its stream,
// and tabSleep how long a hidden page may take to let its stream go: the
// 10 seconds that it waits, and a margin.
const (
	tabLoad  = 15 * time.Second
	tabSleep = 30 * time.Second
)

// watchStream is the script of each tab of the check: it notes that the
// live stream of the page opened.
const watchStream = `addEventListener('htmx:sseOpen', () => { window.sindStreamOpen = true; }, true);`

// hidePage makes the page one that the browser hides, as a tab in the
// background is, and resolves once the page lets its streams go
// (sind:sleep). A headless browser hides no tab itself.
const hidePage = `new Promise(resolve => {
	document.addEventListener('sind:sleep', () => resolve(true), { once: true });
	Object.defineProperty(document, 'hidden', { get: () => true, configurable: true });
	Object.defineProperty(document, 'visibilityState', { get: () => 'hidden', configurable: true });
	document.dispatchEvent(new Event('visibilitychange'));
})`

// streamOpen resolves once the live stream of the page is open.
const streamOpen = `new Promise(resolve => {
	const tick = () => window.sindStreamOpen ? resolve(true) : setTimeout(tick, 50);
	tick();
})`

// checkTabs opens the first page of sind ui in tabsHeld tabs, which take
// every connection of the browser, hides them, and opens tabsMore tabs
// more: each must show its page and open its stream. It fails when a
// hidden page keeps its stream, as then the next tab waits for a
// connection and stays empty, and when a page reports a fault.
func checkTabs(bctx context.Context, root string) error {
	var held []context.Context
	for i := 1; i <= tabsHeld; i++ {
		ctx, seen, cancel, err := openTab(bctx, root)
		defer cancel()
		if err != nil {
			return fmt.Errorf("tab %d: %w", i, errors.Join(err, seen.err()))
		}
		held = append(held, ctx)
	}
	// Each page hides at once, so that they wait together.
	slept := make(chan error, len(held))
	for i, ctx := range held {
		go func() {
			ctx, cancel := context.WithTimeout(ctx, tabSleep)
			defer cancel()
			if err := chromedp.Do(ctx, chromedp.Evaluate[chromedp.Void](hidePage, chromedp.EvalAwaitPromise)); err != nil {
				slept <- fmt.Errorf("tab %d is hidden and keeps its stream: %w", i+1, err)
				return
			}
			slept <- nil
		}()
	}
	var errs []error
	for range held {
		errs = append(errs, <-slept)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	for i := tabsHeld + 1; i <= tabsHeld+tabsMore; i++ {
		_, seen, cancel, err := openTab(bctx, root)
		defer cancel()
		if err != nil {
			return fmt.Errorf("tab %d, with %d hidden tabs: %w", i, tabsHeld, errors.Join(err, seen.err()))
		}
		if err := seen.err(); err != nil {
			return fmt.Errorf("tab %d: %w", i, err)
		}
	}
	return nil
}

// openTab opens the first page of sind ui on a tab of its own and returns
// when its live stream is open. The tab stays open until cancel; seen holds
// the faults that its page reports.
func openTab(bctx context.Context, root string) (context.Context, *faults, context.CancelFunc, error) {
	ctx, cancel := chromedp.NewContext(bctx)
	seen := &faults{}
	if err := chromedp.Listen(ctx, seen.event); err != nil {
		return ctx, seen, cancel, err
	}
	load, stop := context.WithTimeout(ctx, tabLoad)
	defer stop()
	err := chromedp.Do(load,
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: watchStream})
			return err
		}),
		chromedp.Navigate(root),
		chromedp.WaitVisible(chromedp.CSS("main")),
		chromedp.Evaluate[chromedp.Void](streamOpen, chromedp.EvalAwaitPromise),
	)
	if err != nil {
		err = fmt.Errorf("the page does not load, or its live stream does not open: %w", err)
	}
	return ctx, seen, cancel, err
}
