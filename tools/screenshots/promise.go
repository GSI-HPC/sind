// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import "github.com/chromedp/cdproto/runtime"

// awaitPromise makes Evaluate wait for the promise the script returns.
func awaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }
