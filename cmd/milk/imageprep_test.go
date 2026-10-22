package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/scoutme/milk/internal/transport/acp"
)

func bigPNG(t *testing.T, w, h int) []byte {
	t.Helper()
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
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPrepareImageAttachment_RenamesAndNotes(t *testing.T) {
	a := prepareImageAttachment(PendingAttachment{Name: "shot.png", MIMEType: "image/png", Data: bigPNG(t, 3000, 3000)})
	if !strings.Contains(a.Note, "image shot.png resized 3000×3000 → 2000×2000") {
		t.Errorf("note: %q", a.Note)
	}
	svg := PendingAttachment{Name: "a.svg", MIMEType: "image/svg+xml", Data: []byte("<svg/>")}
	if got := prepareImageAttachment(svg); !bytes.Equal(got.Data, svg.Data) || got.Note != "" {
		t.Error("svg must be untouched")
	}
}

func TestDecodeACPImage_ResizesAndNotes(t *testing.T) {
	data := bigPNG(t, 3000, 3000)
	blk := acp.ContentBlock{Type: "image", MimeType: "image/png", Data: base64.StdEncoding.EncodeToString(data)}
	_, media := parseACPPrompt([]acp.ContentBlock{{Type: "text", Text: "look"}, blk})
	if media == nil || len(media.images) != 1 {
		t.Fatal("expected one image")
	}
	w, _, _ := imgDims(t, media.images[0].Data)
	if w != 2000 {
		t.Errorf("width %d", w)
	}
	found := false
	for _, n := range media.notes {
		if strings.Contains(n, "resized") {
			found = true
		}
	}
	if !found {
		t.Errorf("no resize note in %v", media.notes)
	}
}

func imgDims(t *testing.T, data []byte) (int, int, string) {
	t.Helper()
	cfg, f, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height, f
}
