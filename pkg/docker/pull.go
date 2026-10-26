// SPDX-License-Identifier: LGPL-3.0-or-later

package docker

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/GSI-HPC/go-clikit/progress"
)

// layerLine matches a line of the status docker pull writes, "<layer>:
// <status>", for the layer's short ID: 12 or more hexadecimal digits, and
// what follows the status, such as the bytes of "Downloading  1.05MB/45.6MB
// 3s" that docker writes to a terminal. A line that is none, such as
// "latest: Pulling from library/busybox", "Digest: sha256:..." or "Status:
// Downloaded newer image", names no layer.
var layerLine = regexp.MustCompile(`^([0-9a-f]{12,64}): ([A-Za-z]+(?: [A-Za-z]+)*)(?:\s+(.*))?$`)

// layerBytes matches the bytes of a layer that docker has downloaded, out of
// its size: "1.05MB/45.6MB", in the decimal units of go-units' HumanSize,
// with four significant digits, after the bar "[==>   ]" that docker draws
// first where it finds the terminal wider than 110 columns, or no width.
var layerBytes = regexp.MustCompile(`^(?:\[[=> ]*\]\s*)?([0-9.]+)([kMGTP]?B)/([0-9.]+)([kMGTP]?B)\b`)

// layerState is how far a layer has got.
type layerState int

const (
	// layerPending is a layer docker has told of, that it downloads or
	// will.
	layerPending layerState = iota
	// layerDownloaded is a layer docker has downloaded, and extracts or
	// will.
	layerDownloaded
	// layerDone is a layer that docker extracted ("Pull complete") or that
	// the daemon had ("Already exists").
	layerDone
)

// layerStatuses are the statuses of a layer that docker pull writes, and
// how far the layer has got with each. A status that is not here is
// ignored, lines of layers included, as docker may write others in
// releases that come.
var layerStatuses = map[string]layerState{
	"Pulling fs layer":   layerPending,
	"Waiting":            layerPending,
	"Downloading":        layerPending,
	"Verifying Checksum": layerDownloaded,
	"Download complete":  layerDownloaded,
	"Extracting":         layerDownloaded,
	"Pull complete":      layerDone,
	"Already exists":     layerDone,
}

// layer is what docker pull told of a layer.
type layer struct {
	state layerState
	// had is set for a layer the daemon had, which downloads nothing.
	had bool
	// got and size are the bytes downloaded and the size, 0 while not
	// known.
	got, size int64
}

// pullUnit is the unit a pull reports its work in, once it knows it.
type pullUnit int

const (
	unitUnknown pullUnit = iota
	unitBytes
	unitLayers
)

// layerCount follows the layers of an image that docker pull tells of, and
// reports the pull as the work of a progress span, in one of two units, as
// a span keeps the unit it is given first:
//
//   - Bytes, once docker writes the bytes of a layer it downloads, as it
//     does to a terminal (cmdexec.WithTerminal): the amount is the bytes
//     downloaded, and the size that of the layers, those the daemon has
//     left out. docker tells the size of a layer once it downloads it, a
//     few at a time, so the size of the layers still waiting is guessed
//     to be the mean of those known; and so that the share done never
//     goes back when a guess turns out short, the size is held down to
//     what keeps the share where it was until the bytes catch up.
//   - Layers, when a layer is complete before docker wrote any bytes, as
//     it does where it is no terminal: the amount is the layers that are
//     done out of those docker has told of.
//
// Until the unit is known, nothing is reported. It is safe for concurrent
// use, as the lines of the two streams are parsed from different
// goroutines.
type layerCount struct {
	span *progress.Span

	mu     sync.Mutex
	layers map[string]*layer
	unit   pullUnit
	// size and amount are what was reported last, share the share done
	// they gave.
	size, amount int64
	share        float64
}

// line parses a line docker pull wrote, and has the span report the work
// when it changes. A line that tells nothing of a layer is ignored.
func (l *layerCount) line(_ progress.Stream, line string) {
	m := layerLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return
	}
	state, known := layerStatuses[m[2]]
	if !known {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.layers == nil {
		l.layers = map[string]*layer{}
	}
	ly := l.layers[m[1]]
	if ly == nil {
		ly = &layer{had: m[2] == "Already exists"}
		l.layers[m[1]] = ly
	}
	ly.state = max(ly.state, state)
	if got, size, ok := downloadBytes(m[2], m[3]); ok {
		ly.got, ly.size = max(ly.got, min(got, size)), size
		if l.unit == unitUnknown {
			l.unit = unitBytes
		}
	}
	if ly.state >= layerDownloaded {
		ly.got = ly.size
	}
	if l.unit == unitUnknown && ly.state == layerDone && !ly.had {
		l.unit = unitLayers
	}
	l.report()
}

// downloadBytes returns the bytes of a line of a layer that downloads, and
// whether it has them.
func downloadBytes(status, rest string) (got, size int64, ok bool) {
	if status != "Downloading" {
		return 0, 0, false
	}
	m := layerBytes.FindStringSubmatch(rest)
	if m == nil {
		return 0, 0, false
	}
	got, okGot := humanBytes(m[1], m[2])
	size, okSize := humanBytes(m[3], m[4])
	return got, size, okGot && okSize && size > 0
}

// byteUnits are the decimal units of go-units' HumanSize.
var byteUnits = map[string]float64{"B": 1, "kB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "PB": 1e15}

// humanBytes reads a number of bytes as HumanSize writes it.
func humanBytes(num, unit string) (int64, bool) {
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	return int64(f * byteUnits[unit]), true
}

// report has the span report the work in its unit, if it changed. l.mu is
// held.
func (l *layerCount) report() {
	var size, amount int64
	switch l.unit {
	case unitUnknown:
		return
	case unitLayers:
		size = int64(len(l.layers))
		for _, ly := range l.layers {
			if ly.state == layerDone {
				amount++
			}
		}
	case unitBytes:
		size, amount = l.bytes()
	}
	l.send(size, amount)
}

// bytes returns the size of the layers to download, guessed for those not
// known, and the bytes downloaded, with the size held down so that the share
// done does not go back. l.mu is held.
func (l *layerCount) bytes() (size, amount int64) {
	var known, sized, waiting int64
	for _, ly := range l.layers {
		switch {
		case ly.had:
		case ly.size > 0:
			known += ly.size
			sized++
			amount += ly.got
		case ly.state == layerPending:
			waiting++
		}
	}
	size = known
	if sized > 0 {
		size += waiting * known / sized
	}
	if share := float64(amount) / float64(max(size, 1)); share < l.share {
		size = int64(float64(amount) / l.share)
	}
	return size, amount
}

// send reports size and amount, those that changed. l.mu is held.
func (l *layerCount) send(size, amount int64) {
	unit := progress.UnitItems
	if l.unit == unitBytes {
		unit = progress.UnitBytes
	}
	if size != l.size {
		l.size = size
		l.span.Update(progress.Work(unit, size))
	}
	if amount != l.amount {
		l.amount = amount
		l.span.SetAmount(amount)
	}
	if size > 0 {
		l.share = float64(amount) / float64(size)
	}
}

// finish reports a pull that succeeded as done: every layer docker told of,
// in bytes the size of those it downloaded, or the one item that an image
// the daemon had already is, since docker writes no layer for it.
func (l *layerCount) finish() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unit == unitUnknown {
		l.unit = unitLayers
	}
	for _, ly := range l.layers {
		ly.state, ly.got = layerDone, ly.size
	}
	l.share = 0
	l.report()
	if l.size == 0 {
		l.send(1, 1)
	}
}
