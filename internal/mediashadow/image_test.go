package mediashadow

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 0, 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func dims(t *testing.T, data []byte) (int, int, string) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height, format
}

func TestPrepareKeepsAPictureThatFits(t *testing.T) {
	for _, wh := range [][2]int{{280, 280}, {1568, 1568}, {1280, 960}} {
		in := jpegOf(t, wh[0], wh[1])
		out, format, err := prepare(in)
		if err != nil || format != "jpeg" || !bytes.Equal(in, out) {
			t.Fatalf("%v: re-encoded a fitting picture (format=%s err=%v)", wh, format, err)
		}
	}
}

func TestPrepareResizesIntoJEVBounds(t *testing.T) {
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{2560, 1920, 1568, 1176}, // shrink, aspect kept
		{1920, 2560, 1176, 1568},
		{1569, 1569, 1568, 1568},
		{100, 50, 280, 280},    // pad both sides
		{3000, 100, 1568, 280}, // shrink the long side, pad the short one
		{279, 1000, 280, 1000}, // pad one side only
		{2000, 200, 1568, 280}, // 200 -> 156 after shrinking, then padded
	} {
		out, format, err := prepare(jpegOf(t, tc.w, tc.h))
		if err != nil || format != "jpeg" {
			t.Fatalf("%dx%d: format=%s err=%v", tc.w, tc.h, format, err)
		}
		if w, h, f := dims(t, out); w != tc.wantW || h != tc.wantH || f != "jpeg" {
			t.Fatalf("%dx%d -> %dx%d %s, want %dx%d jpeg", tc.w, tc.h, w, h, f, tc.wantW, tc.wantH)
		}
	}
}

// The padding is white and the picture sits in the middle: a black border
// would read as part of the picture.
func TestPreparePadsWithWhiteAroundTheCentre(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for i := range src.Pix {
		src.Pix[i] = 0
		if i%4 == 3 {
			src.Pix[i] = 255
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, src)
	out, format, err := prepare(b.Bytes())
	if err != nil || format != "jpeg" {
		t.Fatalf("format=%s err=%v", format, err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := img.At(5, 5).RGBA(); r>>8 < 240 {
		t.Fatalf("corner is not white: %d", r>>8)
	}
	if r, _, _, _ := img.At(140, 140).RGBA(); r>>8 > 15 {
		t.Fatalf("centre is not the picture: %d", r>>8)
	}
}

func TestPrepareRejectsWhatJEVCannotTake(t *testing.T) {
	var gif bytes.Buffer
	gif.WriteString("GIF89a")
	huge := image.NewGray(image.Rect(0, 0, maxSourceSide+1, 1))
	var big bytes.Buffer
	if err := png.Encode(&big, huge); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"text": []byte("not a picture"), "gif": gif.Bytes(), "too wide": big.Bytes(), "empty": nil} {
		if _, _, err := prepare(data); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestShrinkAveragesBlocks(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		for y := 0; y < 2; y++ {
			v := uint8(0)
			if x%2 == 1 {
				v = 200
			}
			src.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	got := shrink(src, 2, 1).(*image.RGBA).RGBAAt(0, 0)
	if got.R != 100 || got.A != 255 {
		t.Fatalf("block average = %v, want R=100 A=255", got)
	}
}
