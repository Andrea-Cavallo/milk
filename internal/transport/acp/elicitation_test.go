package acp

import (
	"context"
	"strings"
	"testing"
)

// elicitation wiring (elicitation.go): elicitation/create +
// elicitation/complete behind Host.Elicit, wish/willing confirmations.

func TestWishConfirmationRequestShape(t *testing.T) {
	req := WishConfirmation("publish the wire contract")
	if !strings.Contains(req.Message, "keep wish alive or mark fulfilled") {
		t.Fatalf("message = %q", req.Message)
	}
	schema := req.Schema
	if schema.Type != "object" {
		t.Fatalf("schema type = %q", schema.Type)
	}
	prop, ok := schema.Properties["disposition"]
	if !ok {
		t.Fatalf("no disposition property: %+v", schema.Properties)
	}
	if prop.Type != "string" || len(prop.OneOf) != 2 {
		t.Fatalf("disposition = %+v", prop)
	}
	byConst := map[string]string{}
	for _, opt := range prop.OneOf {
		byConst[opt.Const] = opt.Title
	}
	if byConst[WishKeepAlive] == "" || byConst[WishFulfilled] == "" {
		t.Fatalf("enum options = %v", byConst)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "disposition" {
		t.Fatalf("required = %v", schema.Required)
	}
}

func TestElicitSessionRoundTrip(t *testing.T) {
	conn := &fakeConn{respond: func(method string, params any) (any, error) {
		if method != MethodElicitationCreate {
			t.Fatalf("unexpected request %q", method)
		}
		return CreateElicitationResponse{
			Action:  ElicitationAccept,
			Content: map[string]any{"disposition": WishFulfilled},
		}, nil
	}}
	host := NewACPHost(conn, "sess-9")
	res, err := host.Elicit(context.Background(), WishConfirmation("ship it"))
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}

	// elicitation/create: form mode, session scope, schema, _meta id.
	reqs := conn.requested()
	if len(reqs) != 1 || reqs[0].Method != MethodElicitationCreate {
		t.Fatalf("requests = %+v", reqs)
	}
	body := jsonBody(t, reqs[0].Params)
	if body["mode"] != "form" {
		t.Fatalf("mode = %v", body["mode"])
	}
	if body["sessionId"] != "sess-9" {
		t.Fatalf("sessionId = %v", body["sessionId"])
	}
	if _, ok := body["requestedSchema"]; !ok {
		t.Fatalf("requestedSchema missing: %v", body)
	}
	meta, _ := body["_meta"].(map[string]any)
	if meta[ElicitationIDText] != "elicit-1" {
		t.Fatalf("_meta = %v", meta)
	}

	// elicitation/complete: same elicitation ID, sent after the result.
	nots := conn.sent()
	if len(nots) != 1 || nots[0].Method != MethodElicitationComplete {
		t.Fatalf("notifications = %+v", nots)
	}
	done := jsonBody(t, nots[0].Params)
	if done["elicitationId"] != "elicit-1" {
		t.Fatalf("complete params = %v", done)
	}

	// Result + wish/willing parsing.
	decision, err := ParseWishConfirmation(res)
	if err != nil {
		t.Fatalf("ParseWishConfirmation: %v", err)
	}
	if decision.Action != ElicitationAccept || decision.Disposition != WishFulfilled {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestElicitSessionDecline(t *testing.T) {
	conn := &fakeConn{respond: func(string, any) (any, error) {
		return CreateElicitationResponse{Action: ElicitationDecline}, nil
	}}
	host := NewACPHost(conn, "sess-9")
	res, err := host.Elicit(context.Background(), WishConfirmation("keep going?"))
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}
	decision, err := ParseWishConfirmation(res)
	if err != nil {
		t.Fatalf("ParseWishConfirmation: %v", err)
	}
	if decision.Action != ElicitationDecline || decision.Disposition != "" {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestParseWishConfirmationUnknownDisposition(t *testing.T) {
	_, err := ParseWishConfirmation(ElicitationResult{
		Action:  ElicitationAccept,
		Content: map[string]any{"disposition": "banana"},
	})
	if err == nil {
		t.Fatal("expected error for unknown disposition")
	}
}

func TestMultiSelectProperty(t *testing.T) {
	prop := MultiSelectProperty("Tags", StringMultiSelect([]string{"docs", "release"}))
	body := jsonBody(t, prop)
	if body["type"] != "array" {
		t.Fatalf("type = %v", body["type"])
	}
	items, ok := body["items"].(map[string]any)
	if !ok || items["type"] != "string" {
		t.Fatalf("items = %v", body["items"])
	}
	enum, ok := items["enum"].([]any)
	if !ok || len(enum) != 2 {
		t.Fatalf("items.enum = %v", items)
	}
	titled := MultiSelectProperty("Pick", TitledMultiSelect([]EnumOption{{Const: "a", Title: "A"}}))
	tb := jsonBody(t, titled)
	ti := tb["items"].(map[string]any)
	if _, ok := ti["anyOf"]; !ok {
		t.Fatalf("titled items = %v", ti)
	}
}
