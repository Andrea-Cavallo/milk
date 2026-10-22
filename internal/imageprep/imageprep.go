// Package imageprep shrinks images once, before they enter the conversation.
package imageprep

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif" // register the GIF decoder for image.Decode/DecodeConfig
	"image/jpeg"
	"image/png"
	"runtime"
	"strings"
	"sync"
)

// Images are shrunk once, when they are attached, so every consumer (multipart
// vision payloads, CLI @path staging, the request-size estimate, the
// backend's own 5 MiB image cap) sees the small version.
const (
	// imageMaxDimension bounds the long edge. Vision backends downscale past
	// ~1.5-2K px anyway; the extra pixels only cost upload time and tokens.
	imageMaxDimension = 2000
	// imageMaxBytes bounds the encoded image. Base64 adds a third, so 500 KB
	// is ~670 KB on the wire: well under backends' 5 MiB per-image limit and
	// small enough to leave room for conversation text inside milk's 900 KB
	// default request-size guard (a larger image would force that guard to
	// drop history on every request of the turn).
	imageMaxBytes = 500_000
	// imageMaxDecodePixels guards against decompression bombs (a 32 MB PNG
	// can declare billions of pixels).
	imageMaxDecodePixels = 50_000_000
)

var jpegQualities = []int{88, 78, 66, 52, 40}

// Shrink returns data downscaled and/or re-encoded when it exceeds
// imageMaxDimension or imageMaxBytes, with a human-readable note describing
// the change. Images already within limits are returned byte-identical (no
// generation loss), as is anything this function cannot safely decode (webp,
// bmp, corrupt data, oversized pixel counts): note is "" then.
func Shrink(data []byte, mime string) (out []byte, outMime, note string) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "gif") {
		return data, mime, ""
	}
	if max(cfg.Width, cfg.Height) <= imageMaxDimension && len(data) <= imageMaxBytes {
		return data, mime, ""
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > imageMaxDecodePixels {
		return data, mime, ""
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, mime, ""
	}
	rgba := toRGBA(src)
	if format == "jpeg" {
		rgba = applyOrientation(rgba, jpegOrientation(data))
	}

	scale := 1.0
	if long := max(rgba.Bounds().Dx(), rgba.Bounds().Dy()); long > imageMaxDimension {
		scale = float64(imageMaxDimension) / float64(long)
	}
	var enc []byte
	var encMime string
	var final *image.RGBA
	for range 10 {
		w := max(1, int(float64(rgba.Bounds().Dx())*scale+0.5))
		h := max(1, int(float64(rgba.Bounds().Dy())*scale+0.5))
		final = boxResize(rgba, w, h)
		if enc, encMime = encodeWithin(final, format == "jpeg", imageMaxBytes); enc != nil {
			break
		}
		scale *= 0.75
	}
	if enc == nil {
		return data, mime, ""
	}
	b := final.Bounds()
	note = fmt.Sprintf("resized %d×%d → %d×%d, %s → %s (%s)",
		cfg.Width, cfg.Height, b.Dx(), b.Dy(), humanBytes(len(data)), humanBytes(len(enc)), strings.TrimPrefix(encMime, "image/"))
	return enc, encMime, note
}

// ShrinkDataURI is Shrink for a "data:<mime>;base64,<payload>" URI (tool
// results such as MCP screenshots). Anything that is not such a URI, or
// needs no change, comes back unchanged.
func ShrinkDataURI(uri string) (string, string) {
	rest, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return uri, ""
	}
	mime, b64, ok := strings.Cut(rest, ";base64,")
	if !ok {
		return uri, ""
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return uri, ""
	}
	out, outMime, note := Shrink(raw, mime)
	if note == "" {
		return uri, ""
	}
	return "data:" + outMime + ";base64," + base64.StdEncoding.EncodeToString(out), note
}

func humanBytes(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", (n+512)/1024)
}

// encodeWithin encodes img as PNG (lossless — best for screenshots and
// diagrams) or JPEG (best for photos) and returns the first encoding that
// fits limit, or nil when none does at this size. Photos (jpegFirst, or a
// JPEG less than half the PNG's size) go JPEG; images with transparency stay
// PNG and are only flattened to JPEG as a last resort.
func encodeWithin(img *image.RGBA, jpegFirst bool, limit int) ([]byte, string) {
	opaque := img.Opaque()
	if !opaque {
		if p := encodePNG(img); len(p) <= limit {
			return p, "image/png"
		}
		img = flattenOnWhite(img)
	}
	if jpegFirst {
		if j := encodeJPEG(img, jpegQualities[0]); len(j) <= limit {
			return j, "image/jpeg"
		}
	} else {
		p := encodePNG(img)
		if j := encodeJPEG(img, jpegQualities[0]); len(j)*2 < len(p) && len(j) <= limit {
			return j, "image/jpeg"
		}
		if len(p) <= limit {
			return p, "image/png"
		}
	}
	for _, q := range jpegQualities[1:] {
		if j := encodeJPEG(img, q); len(j) <= limit {
			return j, "image/jpeg"
		}
	}
	return nil, ""
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func encodeJPEG(img image.Image, quality int) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil
	}
	return buf.Bytes()
}

// toRGBA converts any decoded image to a standalone premultiplied RGBA copy
// (stdlib draw has fast paths for the YCbCr/Paletted/Gray decoders return).
func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func flattenOnWhite(src *image.RGBA) *image.RGBA {
	dst := image.NewRGBA(src.Bounds())
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Over)
	return dst
}

// boxResize shrinks src to w×h by area averaging (every output pixel is the
// mean of the source pixels it covers, edge pixels weighted fractionally).
// For large reductions this is sharper and alias-free compared with
// nearest/bilinear, and keeps screenshot text legible. Premultiplied RGBA
// averages correctly across transparency. w×h at or above the source size
// returns src unchanged.
func boxResize(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if w >= sw && h >= sh {
		return src
	}
	w, h = min(w, sw), min(h, sh)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xr, yr := float64(sw)/float64(w), float64(sh)/float64(h)
	// Precompute per-column source spans and weights once.
	type span struct {
		lo, hi int
		wlo    float64 // weight of the first pixel (partial coverage)
		whi    float64 // weight of the last pixel
	}
	spans := func(n int, ratio float64, limit int) []span {
		s := make([]span, n)
		for i := range s {
			a, b := float64(i)*ratio, float64(i+1)*ratio
			lo, hi := int(a), min(int(b+0.999999), limit)-1
			if hi < lo {
				hi = lo
			}
			s[i] = span{lo: lo, hi: hi, wlo: float64(lo+1) - a, whi: b - float64(hi)}
			if lo == hi {
				s[i].wlo, s[i].whi = b-a, 0
			}
		}
		return s
	}
	cols, rows := spans(w, xr, sw), spans(h, yr, sh)
	weight := func(s span, i int) float64 {
		switch {
		case s.lo == s.hi:
			return s.wlo
		case i == s.lo:
			return s.wlo
		case i == s.hi:
			return s.whi
		}
		return 1
	}
	// Output rows are independent: split them across CPUs.
	workers := min(runtime.NumCPU(), h)
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for y := k; y < h; y += workers {
				ry := rows[y]
				for x, cx := range cols {
					var r, g, b, a, tot float64
					for sy := ry.lo; sy <= ry.hi; sy++ {
						wy := weight(ry, sy)
						row := src.Pix[sy*src.Stride:]
						for sx := cx.lo; sx <= cx.hi; sx++ {
							wt := wy * weight(cx, sx)
							p := row[sx*4 : sx*4+4]
							r += float64(p[0]) * wt
							g += float64(p[1]) * wt
							b += float64(p[2]) * wt
							a += float64(p[3]) * wt
							tot += wt
						}
					}
					o := dst.Pix[y*dst.Stride+x*4:]
					o[0], o[1], o[2], o[3] = uint8(r/tot+0.5), uint8(g/tot+0.5), uint8(b/tot+0.5), uint8(a/tot+0.5)
				}
			}
		}(k)
	}
	wg.Wait()
	return dst
}

// jpegOrientation reads the EXIF Orientation tag (1-8) from a JPEG, 1 when
// absent or unparsable. Re-encoding drops EXIF, so without applying it a
// phone photo shot sideways would reach the model rotated.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) && data[i] == 0xFF {
		marker := data[i+1]
		if marker == 0xDA || marker == 0xD9 { // scan data / EOI: no more headers
			return 1
		}
		size := int(data[i+2])<<8 | int(data[i+3])
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		seg := data[i+4 : i+2+size]
		if marker == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var u16 func([]byte) int
	var u32 func([]byte) int
	switch string(t[:2]) {
	case "II":
		u16 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 }
		u32 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24 }
	case "MM":
		u16 = func(b []byte) int { return int(b[0])<<8 | int(b[1]) }
		u32 = func(b []byte) int { return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3]) }
	default:
		return 1
	}
	ifd := u32(t[4:8])
	if ifd < 8 || ifd+2 > len(t) {
		return 1
	}
	n := u16(t[ifd:])
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if u16(t[e:]) == 0x0112 {
			if o := u16(t[e+8:]); o >= 1 && o <= 8 {
				return o
			}
			return 1
		}
	}
	return 1
}

// applyOrientation rotates/flips src so EXIF orientation o becomes upright.
func applyOrientation(src *image.RGBA, o int) *image.RGBA {
	if o <= 1 || o > 8 {
		return src
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := sw, sh
	if o >= 5 {
		dw, dh = sh, sw
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = sw-1-x, y
			case 3:
				dx, dy = sw-1-x, sh-1-y
			case 4:
				dx, dy = x, sh-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = sh-1-y, x
			case 7:
				dx, dy = sh-1-y, sw-1-x
			case 8:
				dx, dy = y, sw-1-x
			}
			copy(dst.Pix[dy*dst.Stride+dx*4:dy*dst.Stride+dx*4+4], src.Pix[y*src.Stride+x*4:y*src.Stride+x*4+4])
		}
	}
	return dst
}
