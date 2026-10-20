package local

import (
	"context"
	"testing"
)

// Issue #297: per-call ctx parts are the concurrency-safe path for a shared
// agent — they win over the instance field and never touch it; the field
// stays the single-threaded fallback, consumed once.
func TestTakeImageParts_CtxWinsOverInstanceField(t *testing.T) {
	a := &Agent{}
	fieldPart := ContentPart{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,ZmllbGQ="}}

	// No ctx key → instance field consumed exactly once (old semantics).
	a.SetPendingImageParts([]ContentPart{fieldPart})
	if got := a.takeImageParts(context.Background()); len(got) != 1 || got[0].ImageURL == nil {
		t.Fatalf("fallback parts = %+v, want the instance field part", got)
	}
	if a.pendingImageParts != nil {
		t.Error("instance field must be consumed after the fallback take")
	}

	// With a ctx key → ctx parts win and the field stays untouched, so a
	// concurrent holder of the same agent loses nothing.
	a.SetPendingImageParts([]ContentPart{fieldPart})
	ctx := WithImageParts(context.Background(), []ContentPart{{Type: "text"}})
	got := a.takeImageParts(ctx)
	if len(got) != 1 || got[0].Type != "text" {
		t.Fatalf("ctx parts = %+v, want the per-call parts", got)
	}
	if len(a.pendingImageParts) != 1 {
		t.Error("ctx take must not touch the instance field (shared-agent safety)")
	}
	// The field still serves the next call without a ctx key.
	if got := a.takeImageParts(context.Background()); len(got) != 1 || got[0].ImageURL == nil {
		t.Fatalf("field after ctx take = %+v, want the instance part still there", got)
	}
}

func TestImagePartsFromContext_Roundtrip(t *testing.T) {
	if _, ok := ImagePartsFromContext(context.Background()); ok {
		t.Error("bare ctx must report no parts")
	}
	parts := []ContentPart{{Type: "image_url", ImageURL: &ImageURLPart{URL: "data:image/png;base64,eA=="}}}
	got, ok := ImagePartsFromContext(WithImageParts(context.Background(), parts))
	if !ok || len(got) != 1 || got[0].ImageURL.URL != parts[0].ImageURL.URL {
		t.Errorf("roundtrip = %+v ok=%v, want the same parts", got, ok)
	}
}
