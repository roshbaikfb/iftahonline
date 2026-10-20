// Package media turns merchant uploads into clean image masters, stores
// them in S3-compatible storage, and builds signed imgproxy delivery URLs
// (ADR-0012). Everything here treats the upload as hostile input.
package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"

	"golang.org/x/image/webp"
)

// MaxSide bounds each dimension, whatever the pixel budget.
const MaxSide = 16384

// JPEGQuality of masters. imgproxy re-encodes for delivery, so the master
// keeps detail.
const JPEGQuality = 90

// RejectError says why an upload was refused. Code is stable and shown to
// the merchant (product_images.failure_reason).
type RejectError struct{ Code string }

func (e *RejectError) Error() string { return "media: rejected: " + e.Code }

// Rejection codes.
var (
	ErrTooLarge    = &RejectError{Code: "file_too_large"}
	ErrUnsupported = &RejectError{Code: "unsupported_type"}
	ErrTooManyPx   = &RejectError{Code: "too_many_pixels"}
	ErrCorrupt     = &RejectError{Code: "corrupt_image"}
)

// Limits bound what Process accepts.
type Limits struct {
	MaxBytes  int64 // largest original
	MaxPixels int64 // width × height, checked from the header before decoding
}

// Master is a re-encoded image: no metadata, upright, JPEG when opaque
// and PNG when it has transparency.
type Master struct {
	Data        []byte
	Format      string // "jpeg" or "png"
	ContentType string
	Width       int
	Height      int
}

type format struct {
	name   string
	config func(io.Reader) (image.Config, error)
	decode func(io.Reader) (image.Image, error)
}

// formats are the accepted originals, keyed by sniffed content type. SVG
// and everything else is refused: only raster formats we decode in pure
// Go reach storage.
func formats() map[string]format {
	return map[string]format{
		"image/jpeg": {"jpeg", jpeg.DecodeConfig, jpeg.Decode},
		"image/png":  {"png", png.DecodeConfig, png.Decode},
		"image/gif":  {"gif", gif.DecodeConfig, gif.Decode}, // first frame only
		"image/webp": {"webp", webp.DecodeConfig, webp.Decode},
	}
}

// Process reads an original (at most lim.MaxBytes), detects its real type
// from its bytes, refuses anything but JPEG, PNG, GIF and WebP, checks
// the dimensions before decoding (decompression bombs), applies the EXIF
// orientation, and re-encodes it. Re-encoding drops every metadata block
// (EXIF, GPS, XMP, ICC, comments). A *RejectError means the upload is bad;
// any other error is I/O.
func Process(r io.Reader, lim Limits) (Master, error) {
	data, err := io.ReadAll(io.LimitReader(r, lim.MaxBytes+1))
	if err != nil {
		return Master{}, fmt.Errorf("media: read original: %w", err)
	}
	if int64(len(data)) > lim.MaxBytes {
		return Master{}, ErrTooLarge
	}
	f, ok := formats()[http.DetectContentType(data)]
	if !ok {
		return Master{}, ErrUnsupported
	}
	cfg, err := f.config(bytes.NewReader(data))
	if err != nil {
		return Master{}, ErrCorrupt
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxSide || cfg.Height > MaxSide ||
		int64(cfg.Width)*int64(cfg.Height) > lim.MaxPixels {
		return Master{}, ErrTooManyPx
	}
	img, err := f.decode(bytes.NewReader(data))
	if err != nil {
		return Master{}, ErrCorrupt
	}
	if b := img.Bounds(); b.Dx() != cfg.Width || b.Dy() != cfg.Height {
		return Master{}, ErrCorrupt // the header lied about the size
	}
	if f.name == "jpeg" {
		img = orient(img, jpegOrientation(data))
	}
	return encode(img)
}

func encode(img image.Image) (Master, error) {
	var buf bytes.Buffer
	b := img.Bounds()
	m := Master{Width: b.Dx(), Height: b.Dy()}
	if opaque(img) {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: JPEGQuality}); err != nil {
			return Master{}, fmt.Errorf("media: encode jpeg: %w", err)
		}
		m.Format, m.ContentType = "jpeg", "image/jpeg"
	} else {
		enc := png.Encoder{CompressionLevel: png.DefaultCompression}
		if err := enc.Encode(&buf, img); err != nil {
			return Master{}, fmt.Errorf("media: encode png: %w", err)
		}
		m.Format, m.ContentType = "png", "image/png"
	}
	m.Data = buf.Bytes()
	return m, nil
}

// opaque reports whether every pixel is fully opaque. Most decoded types
// answer directly; others are checked pixel by pixel.
func opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return false
			}
		}
	}
	return true
}

// orient returns img turned upright for an EXIF orientation (1..8).
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}
	src := image.NewRGBA(img.Bounds().Sub(img.Bounds().Min))
	draw.Draw(src, src.Bounds(), img, img.Bounds().Min, draw.Src)
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := w, h
	if o >= 5 { // 5..8 swap axes
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 clockwise
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 90 counter-clockwise
				dx, dy = y, w-1-x
			}
			i, j := src.PixOffset(x, y), dst.PixOffset(dx, dy)
			copy(dst.Pix[j:j+4], src.Pix[i:i+4])
		}
	}
	return dst
}

// IsReject reports whether err is a *RejectError and returns its code.
func IsReject(err error) (string, bool) {
	var r *RejectError
	if errors.As(err, &r) {
		return r.Code, true
	}
	return "", false
}
