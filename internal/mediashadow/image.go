package mediashadow

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // Telegram photos are JPEG; PNG is accepted as it comes.
)

// JEV rejects (does not shrink) a picture outside these bounds, and one bad
// picture fails the whole request.
const (
	minSide = 280
	maxSide = 1568
	// maxSourceSide refuses absurd headers before decoding them: a
	// 30000×30000 JPEG is a few KB on the wire and gigabytes in memory.
	maxSourceSide = 10000
	jpegQuality   = 90
)

var errUnsupportedImage = errors.New("mediashadow: not a JPEG or PNG picture")

// prepare returns the picture in a form JEV accepts and its format name: as
// it came when both sides already fit, otherwise shrunk so the longer side is
// at most maxSide, then padded with white so the shorter side is at least
// minSide, and re-encoded as JPEG. EXIF orientation is ignored on purpose:
// Telegram re-encodes photos without it, and a sideways flyer still reads as
// a flyer.
func prepare(data []byte) ([]byte, string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, "", errUnsupportedImage
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxSourceSide || cfg.Height > maxSourceSide {
		return nil, "", errors.New("mediashadow: picture dimensions out of range")
	}
	if fits(cfg.Width, cfg.Height) {
		return data, format, nil
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", errUnsupportedImage
	}
	w, h := targetSize(cfg.Width, cfg.Height)
	img := shrink(src, w, h)
	canvasW, canvasH := max(w, minSide), max(h, minSide)
	canvas := image.NewRGBA(image.Rect(0, 0, canvasW, canvasH))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	off := image.Pt((canvasW-w)/2, (canvasH-h)/2)
	draw.Draw(canvas, image.Rectangle{Min: off, Max: off.Add(image.Pt(w, h))}, img, image.Point{}, draw.Src)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", errors.New("mediashadow: cannot encode picture")
	}
	return out.Bytes(), "jpeg", nil
}

func fits(w, h int) bool {
	return w >= minSide && h >= minSide && w <= maxSide && h <= maxSide
}

// targetSize scales (w, h) down so neither side exceeds maxSide, keeping the
// aspect ratio; it never scales up, which padding handles instead.
func targetSize(w, h int) (int, int) {
	long := max(w, h)
	if long <= maxSide {
		return w, h
	}
	return max(1, w*maxSide/long), max(1, h*maxSide/long)
}

// shrink box-averages src into a w×h image. It is not a resampler worth
// keeping a dependency for: the classifier needs a legible picture, not a
// pretty one.
func shrink(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == w && sh == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0 := y * sh / h
		y1 := max((y+1)*sh/h, y0+1)
		for x := 0; x < w; x++ {
			x0 := x * sw / w
			x1 := max((x+1)*sw/w, x0+1)
			var r, g, bl, a, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, ca := src.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
					r, g, bl, a = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca)
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), uint8(a / n >> 8)})
		}
	}
	return dst
}
