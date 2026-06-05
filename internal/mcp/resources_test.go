package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClientCapabilityDetection(t *testing.T) {
	t.Parallel()
	withResources := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		return initializeResult{
			ProtocolVersion: protocolVersion,
			ServerInfo:      ServerInfo{Name: "res"},
			Capabilities:    serverCapabilities{Resources: &resourceCapability{}},
		}, nil
	})
	if err := withResources.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if !withResources.SupportsResources() {
		t.Fatal("server advertising resources should report SupportsResources() true")
	}

	without := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		return initializeResult{ProtocolVersion: protocolVersion, ServerInfo: ServerInfo{Name: "plain"}}, nil
	})
	if err := without.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if without.SupportsResources() {
		t.Fatal("server without resources capability should report false")
	}
}

func TestClientListResourcesPaginates(t *testing.T) {
	t.Parallel()
	page := 0
	client := newTestClient(t, func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "resources/list" {
			t.Errorf("unexpected method %q", method)
		}
		page++
		if page == 1 {
			return listResourcesResult{
				Resources:  []ResourceDescriptor{{URI: "file:///a", Name: "a"}},
				NextCursor: "next",
			}, nil
		}
		var p listResourcesParams
		_ = json.Unmarshal(params, &p)
		if p.Cursor != "next" {
			t.Errorf("second page cursor = %q, want next", p.Cursor)
		}
		return listResourcesResult{Resources: []ResourceDescriptor{{URI: "file:///b", Name: "b"}}}, nil
	})

	resources, err := client.ListResources(context.Background())
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 || resources[0].URI != "file:///a" || resources[1].URI != "file:///b" {
		t.Fatalf("resources = %#v, want a+b across two pages", resources)
	}
}

func TestClientReadResource(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(method string, params json.RawMessage) (any, *rpcError) {
		if method != "resources/read" {
			t.Errorf("unexpected method %q", method)
		}
		var p readResourceParams
		if err := json.Unmarshal(params, &p); err != nil {
			t.Errorf("decode params: %v", err)
		}
		if p.URI != "file:///doc" {
			t.Errorf("uri = %q, want file:///doc", p.URI)
		}
		return ReadResourceResult{Contents: []ResourceContents{{URI: p.URI, MimeType: "text/plain", Text: "hello"}}}, nil
	})

	result, err := client.ReadResource(context.Background(), "file:///doc")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) != 1 || result.Contents[0].Text != "hello" {
		t.Fatalf("contents = %#v, want one text item", result.Contents)
	}
}

func TestClientReadResourceSurfacesError(t *testing.T) {
	t.Parallel()
	client := newTestClient(t, func(_ string, _ json.RawMessage) (any, *rpcError) {
		return nil, &rpcError{Code: -32002, Message: "resource not found"}
	})
	if _, err := client.ReadResource(context.Background(), "file:///missing"); err == nil {
		t.Fatal("expected error from a missing resource")
	}
}
