package media

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

var lim = Limits{MaxBytes: 2 << 20, MaxPixels: 4_000_000}

func solid(w, h int, c color.Color) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func encJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encPNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// exifSegment is an APP1 block with an orientation tag and a fake GPS
// string standing in for private metadata.
func exifSegment(bo binary.ByteOrder, orientation uint16) []byte {
	var t bytes.Buffer
	if bo == binary.LittleEndian {
		t.WriteString("II")
	} else {
		t.WriteString("MM")
	}
	w := func(v any) { _ = binary.Write(&t, bo, v) }
	w(uint16(42))
	w(uint32(8))
	w(uint16(1))      // one entry
	w(uint16(0x0112)) // orientation
	w(uint16(3))      // SHORT
	w(uint32(1))
	w(orientation)
	w(uint16(0))
	w(uint32(0)) // no next IFD
	t.WriteString("GPS:30.0444,31.2357 secret-home")
	payload := append([]byte("Exif\x00\x00"), t.Bytes()...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2)) //nolint:gosec // small test payload
	return append(seg, payload...)
}

func withExif(jpg, seg []byte) []byte {
	out := append([]byte{}, jpg[:2]...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

// bombPNG is a valid PNG signature and IHDR that claims w × h, with no data.
func bombPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	_ = binary.Write(&b, binary.BigEndian, uint32(13))
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func TestProcess(t *testing.T) {
	t.Parallel()
	red := color.NRGBA{R: 200, A: 255}
	opaqueJPEG := encJPEG(t, solid(40, 20, red))
	var g bytes.Buffer
	if err := gif.Encode(&g, solid(10, 10, red), nil); err != nil {
		t.Fatal(err)
	}
	webpData, err := os.ReadFile("testdata/sample.webp")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		in     []byte
		lim    Limits
		format string
		w, h   int
		reject string
	}{
		{"jpeg", opaqueJPEG, lim, "jpeg", 40, 20, ""},
		{"jpeg rotated 90", withExif(opaqueJPEG, exifSegment(binary.BigEndian, 6)), lim, "jpeg", 20, 40, ""},
		{"jpeg rotated 180 little endian", withExif(opaqueJPEG, exifSegment(binary.LittleEndian, 3)), lim, "jpeg", 40, 20, ""},
		{"opaque png becomes jpeg", encPNG(t, solid(8, 8, red)), lim, "jpeg", 8, 8, ""},
		{"transparent png stays png", encPNG(t, solid(8, 8, color.NRGBA{G: 9, A: 100})), lim, "png", 8, 8, ""},
		{"gif first frame", g.Bytes(), lim, "jpeg", 10, 10, ""},
		{"webp", webpData, lim, "jpeg", 150, 100, ""},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), lim, "", 0, 0, "unsupported_type"},
		{"svg with xml header", []byte(`<?xml version="1.0"?><svg/>`), lim, "", 0, 0, "unsupported_type"},
		{"html", []byte(`<html><body>hi</body></html>`), lim, "", 0, 0, "unsupported_type"},
		{"empty", nil, lim, "", 0, 0, "unsupported_type"},
		{"pdf", []byte("%PDF-1.7\n..."), lim, "", 0, 0, "unsupported_type"},
		{"truncated jpeg", opaqueJPEG[:len(opaqueJPEG)/2], lim, "", 0, 0, "corrupt_image"},
		{"png header only", bombPNG(100, 100), lim, "", 0, 0, "corrupt_image"},
		{"decompression bomb (pixels)", bombPNG(3000, 3000), lim, "", 0, 0, "too_many_pixels"},
		{"decompression bomb (side)", bombPNG(20000, 10), Limits{MaxBytes: 1 << 20, MaxPixels: 1 << 40}, "", 0, 0, "too_many_pixels"},
		{"too large", opaqueJPEG, Limits{MaxBytes: 100, MaxPixels: lim.MaxPixels}, "", 0, 0, "file_too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := Process(bytes.NewReader(tt.in), tt.lim)
			if tt.reject != "" {
				if code, ok := IsReject(err); !ok || code != tt.reject {
					t.Fatalf("err = %v, want reject %s", err, tt.reject)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if m.Format != tt.format || m.Width != tt.w || m.Height != tt.h {
				t.Fatalf("got %s %dx%d, want %s %dx%d", m.Format, m.Width, m.Height, tt.format, tt.w, tt.h)
			}
			if bytes.Contains(m.Data, []byte("Exif")) || bytes.Contains(m.Data, []byte("secret-home")) {
				t.Fatal("metadata survived re-encoding")
			}
			cfg, kind, err := image.DecodeConfig(bytes.NewReader(m.Data))
			if err != nil || kind != m.Format || cfg.Width != m.Width || cfg.Height != m.Height {
				t.Fatalf("master does not decode as declared: %v %s %+v", err, kind, cfg)
			}
		})
	}
}

func TestOrient(t *testing.T) {
	t.Parallel()
	// 2×1: left red, right blue.
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{B: 255, A: 255})
	red := func(c color.Color) bool { r, _, b, _ := c.RGBA(); return r > b }
	tests := []struct {
		o      int
		w, h   int
		redAtX int
		redAtY int
	}{
		{1, 2, 1, 0, 0}, {2, 2, 1, 1, 0}, {3, 2, 1, 1, 0}, {4, 2, 1, 0, 0},
		{5, 1, 2, 0, 0}, {6, 1, 2, 0, 0}, {7, 1, 2, 0, 1}, {8, 1, 2, 0, 1},
		{9, 2, 1, 0, 0},
	}
	for _, tt := range tests {
		out := orient(src, tt.o)
		if b := out.Bounds(); b.Dx() != tt.w || b.Dy() != tt.h {
			t.Errorf("o=%d: size %dx%d, want %dx%d", tt.o, b.Dx(), b.Dy(), tt.w, tt.h)
			continue
		}
		if !red(out.At(tt.redAtX, tt.redAtY)) {
			t.Errorf("o=%d: red not at (%d,%d)", tt.o, tt.redAtX, tt.redAtY)
		}
	}
}

func TestJPEGOrientation(t *testing.T) {
	t.Parallel()
	jpg := encJPEG(t, solid(4, 4, color.Black))
	tests := []struct {
		name string
		in   []byte
		want int
	}{
		{"no exif", jpg, 1},
		{"big endian 6", withExif(jpg, exifSegment(binary.BigEndian, 6)), 6},
		{"little endian 8", withExif(jpg, exifSegment(binary.LittleEndian, 8)), 8},
		{"out of range", withExif(jpg, exifSegment(binary.BigEndian, 9)), 1},
		{"not jpeg", []byte("GIF89a"), 1},
		{"truncated segment", append([]byte{0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF}, "Exif"...), 1},
		{"empty", nil, 1},
	}
	for _, tt := range tests {
		if got := jpegOrientation(tt.in); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

// FuzzJPEGOrientation: hostile metadata never panics and yields 1..8.
func FuzzJPEGOrientation(f *testing.F) {
	jpg := encJPEG(f, solid(2, 2, color.Black))
	f.Add(jpg)
	f.Add(withExif(jpg, exifSegment(binary.BigEndian, 6)))
	f.Add(withExif(jpg, exifSegment(binary.LittleEndian, 3)))
	f.Fuzz(func(t *testing.T, b []byte) {
		if o := jpegOrientation(b); o < 1 || o > 8 {
			t.Fatalf("orientation %d", o)
		}
	})
}

// FuzzProcess: any upload either becomes a valid master within the
// limits or is rejected; it never panics.
func FuzzProcess(f *testing.F) {
	small := Limits{MaxBytes: 64 << 10, MaxPixels: 64 * 64}
	jpg := encJPEG(f, solid(8, 8, color.White))
	f.Add(jpg)
	f.Add(withExif(jpg, exifSegment(binary.BigEndian, 6)))
	f.Add(encPNG(f, solid(4, 4, color.NRGBA{A: 10})))
	f.Add(bombPNG(1<<20, 1<<20))
	f.Add([]byte("<svg/>"))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := Process(bytes.NewReader(b), small)
		if err != nil {
			if _, ok := IsReject(err); !ok {
				t.Fatalf("non-reject error on in-memory input: %v", err)
			}
			return
		}
		if m.Width*m.Height > int(small.MaxPixels) || (m.Format != "jpeg" && m.Format != "png") ||
			!strings.HasPrefix(m.ContentType, "image/") {
			t.Fatalf("bad master %s %dx%d", m.Format, m.Width, m.Height)
		}
	})
}
