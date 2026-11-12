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

	"github.com/chromedp/cdproto/cdp"
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
	if err := chromedp.Do(bctx, chromedp.Navigate(base)); err != nil {
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

	steps := []chromedp.Action[chromedp.Void]{
		setWindow(size.Width, size.Height),
		freezeClock(epoch),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			_, err := cdp.Call(ctx, t, emulation.SetEmulatedMedia, emulation.SetEmulatedMediaParams{Features: []*emulation.MediaFeature{
				{Name: "prefers-color-scheme", Value: theme},
				{Name: "prefers-reduced-motion", Value: "reduce"},
			}})
			return err
		}),
		chromedp.Navigate(root + s.Path),
		chromedp.WaitVisible(chromedp.CSS("main")),
		quiet(),
	}
	for _, st := range s.Steps {
		steps = append(steps, step(st), quiet())
	}
	if s.Hide != "" {
		steps = append(steps, hide(s.Hide))
	}
	if s.Fit {
		steps = append(steps, shorten(size), quiet())
	}
	// Nothing is left focused or hovered, so that no ring or tooltip shows.
	steps = append(steps,
		chromedp.Evaluate[chromedp.Void](`document.activeElement && document.activeElement.blur()`),
		chromedp.MouseEvent(chromedp.MouseMoved, 0, 0),
	)
	shot := chromedp.CaptureScreenshot()
	if s.Crop != "" {
		steps = append(steps, fit(s.Crop, size), quiet())
		shot = chromedp.Screenshot(chromedp.CSS(s.Crop), chromedp.NodeVisible)
	}
	if err := chromedp.Do(ctx, steps...); err != nil {
		return nil, err
	}
	return chromedp.Run(ctx, shot)
}

// setWindow sets the size of the window, at a device scale factor of 1.
func setWindow(width, height int) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
			Width: int64(width), Height: int64(height), DeviceScaleFactor: 1,
		})
		return err
	})
}

// freezeClock makes Date.now of the page the epoch of the still demo, which
// the clocks of the run page count from: the server's pages stand at the
// epoch, and the browser's clock must too, or a run that goes on would show
// how long ago, by the real time, it started.
func freezeClock(epoch time.Time) chromedp.Action[chromedp.Void] {
	js := fmt.Sprintf(`(() => { const t = %d; Date.now = () => t; })()`, epoch.UnixMilli())
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: js})
		return err
	})
}

// step returns the action of a step.
func step(st Step) chromedp.Action[chromedp.Void] {
	switch {
	case st.Click != "":
		return chromedp.Steps(chromedp.WaitVisible(chromedp.CSS(st.Click)), chromedp.Click(chromedp.CSS(st.Click)))
	case st.Dialog != "":
		// The dialog's button asks for the same address.
		q, _ := json.Marshal("/dialogs/" + st.Dialog)
		return chromedp.Steps(
			chromedp.Evaluate[chromedp.Void](fmt.Sprintf(`htmx.ajax('GET', %s, {target: '#layer', swap: 'innerHTML'})`, q), chromedp.EvalAwaitPromise),
			chromedp.WaitVisible(chromedp.CSS(`#layer [role=dialog]`)),
		)
	case st.Wait != "":
		return chromedp.WaitVisible(chromedp.CSS(st.Wait))
	default:
		return chromedp.Steps(chromedp.WaitVisible(chromedp.CSS(st.Into)), chromedp.SendKeys(chromedp.CSS(st.Into), st.Type))
	}
}

// quiet waits until the page has loaded its fonts and htmx has no request
// in flight, then a moment more. It first lets a request start that htmx
// delays, such as the preview of a dialog after a keystroke.
func quiet() chromedp.Action[chromedp.Void] {
	const js = `document.fonts.ready.then(() => new Promise(r => setTimeout(r, 200))).then(() => new Promise(resolve => {
		const tick = () => document.querySelector('.htmx-request') ? setTimeout(tick, 50) : resolve(true);
		tick();
	}))`
	return chromedp.Steps(chromedp.Evaluate[chromedp.Void](js, chromedp.EvalAwaitPromise), chromedp.Sleep(settle))
}

// fit makes the window tall enough for the element, which a dialog taller
// than the window is not otherwise.
func fit(selector string, size Size) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		js := fmt.Sprintf(`(() => { const r = document.querySelector(%q).getBoundingClientRect(); return r.top + r.height; })()`, selector)
		bottom, err := chromedp.Evaluate[float64](js)(ctx, t)
		if err != nil {
			return err
		}
		// The dialog sits as far from the bottom as from the top.
		height := max(size.Height, int(math.Ceil(bottom))+size.Height/10)
		_, err = setWindow(size.Width, height)(ctx, t)
		return err
	})
}

// minHeight is the shortest window of a fitted shot: the navigation on the
// left needs it.
const minHeight = 560

// shorten makes the window as tall as the page, if that is less than size.
func shorten(size Size) chromedp.Action[chromedp.Void] {
	return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		const js = `(() => { const m = document.querySelector('main'); return m.getBoundingClientRect().top + m.scrollHeight; })()`
		bottom, err := chromedp.Evaluate[float64](js)(ctx, t)
		if err != nil {
			return err
		}
		height := min(size.Height, max(minHeight, int(math.Ceil(bottom))+24))
		_, err = setWindow(size.Width, height)(ctx, t)
		return err
	})
}

// hide hides the elements that match the selector, keeping their space.
func hide(selector string) chromedp.Action[chromedp.Void] {
	js := fmt.Sprintf(`document.querySelectorAll(%q).forEach(e => e.style.visibility = 'hidden')`, selector)
	return chromedp.Evaluate[chromedp.Void](js)
}
