// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// settle is how long a page rests after its last request, so that the
// transitions that no request marks have ended.
const settle = 250 * time.Millisecond

// takeShots takes the shots of the list that o asks for, from the sind ui
// at base, which is its URL with the token.
func takeShots(ctx context.Context, o options, l Shots, base string) error {
	opts := append(slices.Clone(chromedp.DefaultExecAllocatorOptions[:]),
		chromedp.ExecPath(o.Chromium),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("font-render-hinting", "none"),
		chromedp.Flag("disable-lcd-text", true),
		chromedp.Flag("force-device-scale-factor", "1"),
	)
	if o.NoSandbox {
		opts = append(opts, chromedp.NoSandbox)
	}
	actx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()
	bctx, cancel := chromedp.NewContext(actx)
	defer cancel()

	// The first visit swaps the token for the cookie that all tabs share.
	if err := chromedp.Run(bctx, chromedp.Navigate(base)); err != nil {
		return fmt.Errorf("opening sind ui in %s: %w", o.Chromium, err)
	}
	root, err := rootURL(base)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil { //nolint:gosec // G301: the docs' images
		return err
	}
	taken := 0
	for _, s := range l.Shots {
		if len(o.Only) > 0 && !slices.Contains(o.Only, s.Name) {
			continue
		}
		for _, size := range s.WindowSizes() {
			for _, theme := range Themes {
				png, err := take(bctx, root, s, l.Sizes[size], theme, o.Epoch)
				if err != nil {
					return fmt.Errorf("%s (%s, %s): %w", s.Name, size, theme, err)
				}
				if png, err = shrink(png); err != nil {
					return fmt.Errorf("%s (%s, %s): %w", s.Name, size, theme, err)
				}
				if err := os.WriteFile(filepath.Join(o.Out, s.File(size, theme)), png, 0o644); err != nil { //nolint:gosec // G306: the docs' images
					return err
				}
				taken++
			}
		}
	}
	if taken == 0 {
		return fmt.Errorf("no shot is named %v", []string(o.Only))
	}
	fmt.Printf("took %d screenshots into %s\n", taken, o.Out)
	return nil
}

// rootURL returns the address of sind ui without the token.
func rootURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.RawQuery, u.Path = "", ""
	return u.String(), nil
}

// take returns the PNG of the shot in one window and theme, on a tab of its
// own.
func take(parent context.Context, root string, s Shot, size Size, theme string, epoch time.Time) ([]byte, error) {
	ctx, cancel := chromedp.NewContext(parent)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var png []byte
	tasks := chromedp.Tasks{
		emulation.SetDeviceMetricsOverride(int64(size.Width), int64(size.Height), 1, false),
		freezeClock(epoch),
		emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{
			{Name: "prefers-color-scheme", Value: theme},
			{Name: "prefers-reduced-motion", Value: "reduce"},
		}),
		chromedp.Navigate(root + s.Path),
		chromedp.WaitVisible("main", chromedp.ByQuery),
		quiet(),
	}
	for _, st := range s.Steps {
		tasks = append(tasks, step(st), quiet())
	}
	if s.Fit {
		tasks = append(tasks, shorten(size), quiet())
	}
	// Nothing is left focused or hovered, so that no ring or tooltip shows.
	tasks = append(tasks,
		chromedp.Evaluate(`document.activeElement && document.activeElement.blur()`, nil),
		chromedp.MouseEvent("mouseMoved", 0, 0),
	)
	if s.Crop != "" {
		tasks = append(tasks, fit(s.Crop, size), quiet(), chromedp.Screenshot(s.Crop, &png, chromedp.ByQuery, chromedp.NodeVisible))
	} else {
		tasks = append(tasks, chromedp.CaptureScreenshot(&png))
	}
	if err := chromedp.Run(ctx, tasks); err != nil {
		return nil, err
	}
	return png, nil
}

// freezeClock makes Date.now of the page the epoch of the still demo, which
// the clocks of the run page count from: the server's pages stand at the
// epoch, and the browser's clock must too, or a run that goes on would show
// how long ago, by the real time, it started.
func freezeClock(epoch time.Time) chromedp.Action {
	js := fmt.Sprintf(`(() => { const t = %d; Date.now = () => t; })()`, epoch.UnixMilli())
	return chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(js).Do(ctx)
		return err
	})
}

// step returns the action of a step.
func step(st Step) chromedp.Action {
	switch {
	case st.Click != "":
		return chromedp.Tasks{chromedp.WaitVisible(st.Click, chromedp.ByQuery), chromedp.Click(st.Click, chromedp.ByQuery)}
	case st.Dialog != "":
		// The dialog's button asks for the same address.
		q, _ := json.Marshal("/dialogs/" + st.Dialog)
		return chromedp.Tasks{
			chromedp.Evaluate(fmt.Sprintf(`htmx.ajax('GET', %s, {target: '#layer', swap: 'innerHTML'})`, q), nil, awaitPromise),
			chromedp.WaitVisible(`#layer [role=dialog]`, chromedp.ByQuery),
		}
	case st.Wait != "":
		return chromedp.WaitVisible(st.Wait, chromedp.ByQuery)
	default:
		return chromedp.Tasks{chromedp.WaitVisible(st.Into, chromedp.ByQuery), chromedp.SendKeys(st.Into, st.Type, chromedp.ByQuery)}
	}
}

// quiet waits until the page has loaded its fonts and htmx has no request
// in flight, then a moment more. It first lets a request start that htmx
// delays, such as the preview of a dialog after a keystroke.
func quiet() chromedp.Action {
	const js = `document.fonts.ready.then(() => new Promise(r => setTimeout(r, 200))).then(() => new Promise(resolve => {
		const tick = () => document.querySelector('.htmx-request') ? setTimeout(tick, 50) : resolve(true);
		tick();
	}))`
	return chromedp.Tasks{chromedp.Evaluate(js, nil, awaitPromise), chromedp.Sleep(settle)}
}

// fit makes the window tall enough for the element, which a dialog taller
// than the window is not otherwise.
func fit(selector string, size Size) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var bottom float64
		js := fmt.Sprintf(`(() => { const r = document.querySelector(%q).getBoundingClientRect(); return r.top + r.height; })()`, selector)
		if err := chromedp.Evaluate(js, &bottom).Do(ctx); err != nil {
			return err
		}
		// The dialog sits as far from the bottom as from the top.
		height := max(int64(size.Height), int64(math.Ceil(bottom))+int64(size.Height)/10)
		return emulation.SetDeviceMetricsOverride(int64(size.Width), height, 1, false).Do(ctx)
	})
}

// minHeight is the shortest window of a fitted shot: the navigation on the
// left needs it.
const minHeight = 560

// shorten makes the window as tall as the page, if that is less than size.
func shorten(size Size) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		var bottom float64
		const js = `(() => { const m = document.querySelector('main'); return m.getBoundingClientRect().top + m.scrollHeight; })()`
		if err := chromedp.Evaluate(js, &bottom).Do(ctx); err != nil {
			return err
		}
		height := min(int64(size.Height), max(minHeight, int64(math.Ceil(bottom))+24))
		return emulation.SetDeviceMetricsOverride(int64(size.Width), height, 1, false).Do(ctx)
	})
}
