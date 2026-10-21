// SPDX-License-Identifier: LGPL-3.0-or-later

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// gradient is a screenshot with more colours than a palette holds.
func gradient(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 120, 90))
	for y := range 90 {
		for x := range 120 {
			img.Set(x, y, color.NRGBA{uint8(x * 2), uint8(y * 2), uint8(x + y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestShrinkKeepsTheImage(t *testing.T) {
	in := gradient(t)
	out, err := shrink(in)
	if err != nil {
		t.Fatal(err)
	}
	got, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	pal, ok := got.(*image.Paletted)
	if !ok || len(pal.Palette) > maxColors {
		t.Fatalf("the image is %T with a palette of more than %d colours", got, maxColors)
	}
	want, _ := png.Decode(bytes.NewReader(in))
	if got.Bounds() != want.Bounds() {
		t.Fatalf("the size changed from %v to %v", want.Bounds(), got.Bounds())
	}
	// Median cut of 10800 colours into 256: each channel is within a few
	// steps of the original.
	const tolerance = 24
	for y := range 90 {
		for x := range 120 {
			w := color.NRGBAModel.Convert(want.At(x, y)).(color.NRGBA)
			g := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
			for _, d := range []int{int(w.R) - int(g.R), int(w.G) - int(g.G), int(w.B) - int(g.B)} {
				if d < -tolerance || d > tolerance {
					t.Fatalf("pixel %d,%d is %v, not near %v", x, y, got.At(x, y), want.At(x, y))
				}
			}
		}
	}
}

func TestShrinkRepeats(t *testing.T) {
	in := gradient(t)
	a, err := shrink(in)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		b, err := shrink(in)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatal("the same screenshot gave different images")
		}
	}
}

func TestShrinkKeepsFewColoursExact(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	cols := []color.NRGBA{{255, 255, 255, 255}, {0, 0, 0, 255}, {200, 80, 10, 255}, {10, 20, 30, 255}}
	for i, c := range cols {
		img.Set(i, 0, c)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	out, err := shrink(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := png.Decode(bytes.NewReader(out))
	for i, c := range cols {
		if n := color.NRGBAModel.Convert(got.At(i, 0)).(color.NRGBA); n != c {
			t.Errorf("colour %d changed from %v to %v", i, c, got.At(i, 0))
		}
	}
}
