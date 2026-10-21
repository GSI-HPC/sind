// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"slices"
)

// maxColors is the size of the palette of a screenshot.
const maxColors = 256

// shrink re-encodes a screenshot with at most 256 colours. The pages are
// flat colours and the grey steps of anti-aliased text, which the palette
// holds without a difference the eye sees, in about 40% of the bytes: the
// images sit in the repository, and in its history each time they change.
func shrink(data []byte) ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	// Count the colours.
	type rgb [3]uint8
	counts := map[rgb]int{}
	pix := make([]rgb, 0, b.Dx()*b.Dy())
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			n := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			c := rgb{n.R, n.G, n.B}
			counts[c]++
			pix = append(pix, c)
		}
	}
	type entry struct {
		c rgb
		n int
	}
	entries := make([]entry, 0, len(counts))
	for c, n := range counts {
		entries = append(entries, entry{c, n})
	}
	// Map order is random; the palette must not depend on it.
	slices.SortFunc(entries, func(a, b entry) int { return slices.Compare(a.c[:], b.c[:]) })

	// Median cut: split the box that holds the most pixels, at the weighted
	// median of its widest channel, until there are enough boxes.
	boxes := [][]entry{entries}
	for len(boxes) < maxColors {
		best, bestN := -1, 0
		for i, bx := range boxes {
			n := 0
			for _, e := range bx {
				n += e.n
			}
			if len(bx) > 1 && n > bestN {
				best, bestN = i, n
			}
		}
		if best < 0 {
			break
		}
		bx := boxes[best]
		var lo, hi [3]uint8
		lo = [3]uint8{255, 255, 255}
		for _, e := range bx {
			for ch := range 3 {
				lo[ch], hi[ch] = min(lo[ch], e.c[ch]), max(hi[ch], e.c[ch])
			}
		}
		ch := 0
		for i := 1; i < 3; i++ {
			if hi[i]-lo[i] > hi[ch]-lo[ch] {
				ch = i
			}
		}
		slices.SortStableFunc(bx, func(a, b entry) int { return int(a.c[ch]) - int(b.c[ch]) })
		half, run := bestN/2, 0
		cut := 1
		for i, e := range bx {
			if run += e.n; run >= half {
				cut = min(max(i+1, 1), len(bx)-1)
				break
			}
		}
		boxes[best] = bx[:cut:cut]
		boxes = append(boxes, bx[cut:])
	}

	// The palette is the average of each box, weighted by its pixels.
	pal := make(color.Palette, len(boxes))
	for i, bx := range boxes {
		var sum [3]int
		n := 0
		for _, e := range bx {
			for ch := range 3 {
				sum[ch] += int(e.c[ch]) * e.n
			}
			n += e.n
		}
		avg := func(ch int) uint8 { return uint8((sum[ch] + n/2) / n) } //nolint:gosec // G115: the average of bytes is a byte
		pal[i] = color.RGBA{avg(0), avg(1), avg(2), 255}
	}
	index := map[rgb]uint8{}
	for i, bx := range boxes {
		for _, e := range bx {
			index[e.c] = uint8(i)
		}
	}

	dst := image.NewPaletted(b, pal)
	for i, c := range pix {
		dst.Pix[i] = index[c]
	}
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, dst); err != nil {
		return nil, fmt.Errorf("encoding the palette image: %w", err)
	}
	return out.Bytes(), nil
}
