package main

import (
	"image"
	"image/color"
	"path/filepath"
	"testing"
)

func TestScaleAveragesAndRoundTripsPNG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.SetRGBA(0, 0, color.RGBA{R: 100, A: 100})
	src.SetRGBA(1, 0, color.RGBA{R: 200, A: 200})
	src.SetRGBA(0, 1, color.RGBA{R: 100, A: 100})
	src.SetRGBA(1, 1, color.RGBA{R: 200, A: 200})
	scaled := scale(src, 1)
	want := color.RGBA{R: 150, A: 150}
	if got := scaled.RGBAAt(0, 0); got != want {
		t.Fatalf("pixel=%v want %v", got, want)
	}
	path := filepath.Join(t.TempDir(), "icon.png")
	if err := save(path, scaled); err != nil {
		t.Fatal(err)
	}
	loaded, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.RGBAAt(0, 0); got != want {
		t.Fatalf("round trip=%v", got)
	}
	if averageByte(0, 0) != 0 || averageByte(1000, 1) != 255 {
		t.Fatal("channel bounds not enforced")
	}
}
