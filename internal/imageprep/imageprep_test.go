package imageprep

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"strings"
	"testing"
)

type att struct {
	Data           []byte
	MIMEType, Note string
}

func prepare(data []byte, mime string) att {
	out, m, note := Shrink(data, mime)
	return att{out, m, note}
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// screenshotLike is flat regions plus sharp edges: compresses well as PNG.
func screenshotLike(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{240, 240, 240, 255}
			if (x/40+y/40)%2 == 0 {
				c = color.RGBA{30, 60, 200, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func noisy(w, h int) *image.RGBA {
	r := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(r.Intn(256)), byte(r.Intn(256)), byte(r.Intn(256)), 255
	}
	return img
}

func decodeDims(t *testing.T, data []byte) (int, int, string) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height, format
}

func TestPrepareImage_WithinLimitsIsByteIdentical(t *testing.T) {
	data := pngBytes(t, screenshotLike(800, 600))
	a := prepare(data, "image/png")
	if !bytes.Equal(a.Data, data) || a.Note != "" {
		t.Fatalf("in-limit image must pass through untouched (note %q)", a.Note)
	}
}

func TestPrepareImage_DownscalesLongEdgeAndKeepsAspect(t *testing.T) {
	data := pngBytes(t, screenshotLike(4000, 2500))
	a := prepare(data, "image/png")
	w, h, _ := decodeDims(t, a.Data)
	if w != 2000 || h != 1250 {
		t.Fatalf("got %dx%d, want 2000x1250", w, h)
	}
	if a.Note == "" || !strings.Contains(a.Note, "4000×2500 → 2000×1250") {
		t.Errorf("note: %q", a.Note)
	}
	if a.MIMEType != "image/png" {
		t.Errorf("flat screenshot should stay PNG, got %s", a.MIMEType)
	}
}

func TestPrepareImage_OversizedBytesShrinkBelowCap(t *testing.T) {
	// Noise at 1900x1900 is within the dimension cap but ~10 MB as PNG.
	data := pngBytes(t, noisy(1900, 1900))
	if len(data) <= imageMaxBytes {
		t.Skipf("fixture only %d bytes", len(data))
	}
	a := prepare(data, "image/png")
	if len(a.Data) > imageMaxBytes {
		t.Fatalf("still %d bytes", len(a.Data))
	}
	if a.MIMEType != "image/jpeg" {
		t.Errorf("noise should re-encode as JPEG: %s", a.MIMEType)
	}
}

func TestPrepareImage_TransparentStaysPNG(t *testing.T) {
	img := screenshotLike(3000, 3000)
	for i := 3; i < len(img.Pix); i += 4 {
		if (i/4)%7 == 0 {
			img.Pix[i] = 0
			img.Pix[i-1], img.Pix[i-2], img.Pix[i-3] = 0, 0, 0
		}
	}
	a := prepare(pngBytes(t, img), "image/png")
	if a.MIMEType != "image/png" {
		t.Fatalf("alpha image flattened: %s", a.MIMEType)
	}
}

func TestPrepareImage_UndecodableAndUnsupportedPassThrough(t *testing.T) {
	junk := []byte("not an image at all")
	a := prepare(junk, "image/png")
	if !bytes.Equal(a.Data, junk) {
		t.Error("junk modified")
	}
	web := []byte("RIFF....WEBPVP8 ")
	a = prepare(web, "image/webp")
	if !bytes.Equal(a.Data, web) {
		t.Error("webp modified")
	}
}

func TestBoxResize_AveragesExactly(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for x := 0; x < 4; x++ {
		v := uint8(0)
		if x%2 == 1 {
			v = 200
		}
		for y := 0; y < 2; y++ {
			src.SetRGBA(x, y, color.RGBA{v, v, v, 255})
		}
	}
	dst := boxResize(src, 2, 1)
	for x := 0; x < 2; x++ {
		if c := dst.RGBAAt(x, 0); c.R != 100 || c.A != 255 {
			t.Errorf("pixel %d = %+v, want R=100 A=255", x, c)
		}
	}
}

func TestBoxResize_NonIntegerRatioKeepsMean(t *testing.T) {
	src := screenshotLike(1000, 700)
	dst := boxResize(src, 333, 233)
	mean := func(img *image.RGBA) float64 {
		var s float64
		for i := 0; i < len(img.Pix); i += 4 {
			s += float64(img.Pix[i])
		}
		return s / float64(len(img.Pix)/4)
	}
	if d := mean(src) - mean(dst); d > 3 || d < -3 {
		t.Errorf("mean shifted by %.2f", d)
	}
}

// jpegWithOrientation splices an EXIF APP1 carrying orientation o after SOI.
func jpegWithOrientation(t *testing.T, img image.Image, o byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, o, 0, 0, 0, 0, 0, 0}
	seg := append([]byte("Exif\x00\x00"), tiff...)
	n := len(seg) + 2
	app1 := append([]byte{0xFF, 0xE1, byte(n >> 8), byte(n)}, seg...)
	raw := b.Bytes()
	return append(append(append([]byte{}, raw[:2]...), app1...), raw[2:]...)
}

func TestPrepareImage_AppliesExifOrientation(t *testing.T) {
	// Top half red, bottom half blue; 3000x1000 stored, orientation 6 =
	// rotate 90° CW → upright image is 1000x3000 with red on the RIGHT.
	img := image.NewRGBA(image.Rect(0, 0, 3000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 3000; x++ {
			if y < 500 {
				img.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				img.SetRGBA(x, y, color.RGBA{0, 0, 255, 255})
			}
		}
	}
	data := jpegWithOrientation(t, img, 6)
	if jpegOrientation(data) != 6 {
		t.Fatalf("orientation parse = %d", jpegOrientation(data))
	}
	a := prepare(data, "image/jpeg")
	out, _, err := image.Decode(bytes.NewReader(a.Data))
	if err != nil {
		t.Fatal(err)
	}
	b := out.Bounds()
	if b.Dx() != 667 && b.Dx() != 666 || b.Dy() != 2000 {
		t.Fatalf("rotated dims = %dx%d", b.Dx(), b.Dy())
	}
	r, _, bl, _ := out.At(b.Dx()-5, 1000).RGBA()
	if r < 0xC000 || bl > 0x4000 {
		t.Errorf("red should be on the right after rotation, got r=%x b=%x", r, bl)
	}
}

func TestShrinkDataURI(t *testing.T) {
	big := pngBytes(t, screenshotLike(3000, 3000))
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(big)
	out, note := ShrinkDataURI(uri)
	if note == "" || len(out) >= len(uri) || !strings.HasPrefix(out, "data:image/png;base64,") {
		t.Errorf("big data URI not shrunk (note %q, %d -> %d)", note, len(uri), len(out))
	}
	small := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(t, screenshotLike(100, 100)))
	if got, note := ShrinkDataURI(small); got != small || note != "" {
		t.Error("small URI must be unchanged")
	}
	for _, bad := range []string{"https://x/y.png", "data:text/plain,hi", "data:image/png;base64,!!!notb64"} {
		if got, note := ShrinkDataURI(bad); got != bad || note != "" {
			t.Errorf("%q must pass through", bad)
		}
	}
}
