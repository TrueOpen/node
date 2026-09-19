package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeDocumentsCombinesPathsAndDefinitions(t *testing.T) {
	descriptors := testOpenAPIDescriptors(t)
	hub := baseHubDocument()
	task := map[string]any{
		"paths": map[string]any{
			"/TrueOpen/task/v1/params": map[string]any{"get": map[string]any{"parameters": []any{}}},
		},
		"definitions": map[string]any{
			"task.v1.QueryTaskRequest": hash32Definition("task_id"),
		},
		"tags": []any{map[string]any{"name": "Query"}},
	}

	require.NoError(t, mergeDocuments(hub, task, descriptors))
	require.Contains(t, hub["paths"], "/TrueOpen/hub/v1/builders")
	require.Contains(t, hub["paths"], "/TrueOpen/task/v1/params")
	require.Contains(t, hub["definitions"], "task.v1.QueryTaskRequest")
	require.Len(t, hub["tags"], 1)
	require.Equal(t, "TrueOpen Node API (hub + task)", hub["info"].(map[string]any)["title"])
}

func TestMergeDocumentsRejectsConflictingSharedDefinition(t *testing.T) {
	hub := baseHubDocument()
	hub["definitions"] = map[string]any{"hub.v1.QueryFaultRequest": map[string]any{"type": "object"}}
	task := map[string]any{
		"paths": map[string]any{},
		"definitions": map[string]any{
			"hub.v1.QueryFaultRequest": map[string]any{"type": "string"},
		},
	}

	require.ErrorContains(t, mergeDocuments(hub, task, testOpenAPIDescriptors(t)), `conflicting definitions entry "hub.v1.QueryFaultRequest"`)
}

func TestMergeDocumentsDocumentsKeysetPaginationParameters(t *testing.T) {
	descriptors := testOpenAPIDescriptors(t)
	hub := baseHubDocument()
	task := map[string]any{"paths": map[string]any{}, "definitions": map[string]any{}}

	require.NoError(t, mergeDocuments(hub, task, descriptors))
	listPath := hub["paths"].(map[string]any)["/TrueOpen/hub/v1/builders"].(map[string]any)
	parameters := listPath["get"].(map[string]any)["parameters"].([]any)
	require.Contains(t, parameters[0].(map[string]any)["description"], "byte for byte")
	require.Contains(t, parameters[1].(map[string]any)["description"], "max_query_page_limit")
}

func TestMergeDocumentsProjectsRESTBytesByDescriptorIdentity(t *testing.T) {
	descriptors := testOpenAPIDescriptors(t)
	hub := baseHubDocument()
	publicKey := map[string]any{"type": "string", "format": "byte"}
	hub["definitions"].(map[string]any)["hub.v1.BuilderState"] = map[string]any{
		"type":       "object",
		"properties": map[string]any{"current_service_pubkey": publicKey},
	}
	task := map[string]any{"paths": map[string]any{}, "definitions": map[string]any{}}

	require.NoError(t, mergeDocuments(hub, task, descriptors))
	parameter := hub["paths"].(map[string]any)["/TrueOpen/hub/v1/fault/{fault_id}"].(map[string]any)["get"].(map[string]any)["parameters"].([]any)[0].(map[string]any)
	require.Equal(t, "trueopen-hash32", parameter["format"])
	require.Equal(t, "^[0-9a-f]{64}$", parameter["pattern"])
	faultID := hub["definitions"].(map[string]any)["hub.v1.QueryFaultRequest"].(map[string]any)["properties"].(map[string]any)["fault_id"].(map[string]any)
	require.Equal(t, "trueopen-hash32", faultID["format"])
	require.Equal(t, "byte", publicKey["format"])
	require.Contains(t, publicKey["description"], "canonical padded standard-alphabet form")
}

// A merged document with no keyset parameter at all means the list queries were
// dropped from the descriptor, which used to surface as a silently thinner
// OpenAPI document rather than a failed generation.
func TestMergeDocumentsRejectsDocumentWithoutKeysetPagination(t *testing.T) {
	hub := map[string]any{
		"info": map[string]any{},
		"paths": map[string]any{
			"/TrueOpen/hub/v1/fault/{fault_id}": faultPath(),
		},
		"definitions": map[string]any{"hub.v1.QueryFaultRequest": hash32Definition("fault_id")},
	}
	task := map[string]any{"paths": map[string]any{}, "definitions": map[string]any{}}

	require.ErrorContains(t, mergeDocuments(hub, task, testOpenAPIDescriptors(t)), "lost its list queries")
}

func TestMergeDocumentsRejectsCosmosPaginationParameters(t *testing.T) {
	for _, name := range []string{"pagination.key", "pagination.offset", "pagination.count_total"} {
		hub := baseHubDocument()
		hub["paths"].(map[string]any)["/TrueOpen/hub/v1/legacy"] = map[string]any{
			"get": map[string]any{"parameters": []any{map[string]any{"name": name}}},
		}
		task := map[string]any{"paths": map[string]any{}, "definitions": map[string]any{}}

		require.ErrorContains(t, mergeDocuments(hub, task, testOpenAPIDescriptors(t)), "keyset pagination only", name)
	}
}

func baseHubDocument() map[string]any {
	return map[string]any{
		"info": map[string]any{"title": "hub"},
		"paths": map[string]any{
			"/TrueOpen/hub/v1/builders":         keysetListPath(),
			"/TrueOpen/hub/v1/fault/{fault_id}": faultPath(),
		},
		"definitions": map[string]any{
			"hub.v1.QueryFaultRequest": hash32Definition("fault_id"),
		},
		"tags": []any{map[string]any{"name": "Query"}},
	}
}

func keysetListPath() map[string]any {
	return map[string]any{"get": map[string]any{"parameters": []any{
		map[string]any{"name": "page.page_token", "in": "query", "type": "string", "format": "byte", "description": "generic"},
		map[string]any{"name": "page.limit", "in": "query", "type": "integer", "description": "generic"},
	}}}
}

func faultPath() map[string]any {
	return map[string]any{"get": map[string]any{"parameters": []any{
		map[string]any{"name": "fault_id", "in": "path", "type": "string", "format": "byte"},
	}}}
}

func hash32Definition(name string) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		name: map[string]any{"type": "string", "format": "byte"},
	}}
}

func testOpenAPIDescriptors(t *testing.T) *openAPIDescriptors {
	t.Helper()
	descriptors, err := loadOpenAPIDescriptors("../../wire/wire.binpb")
	require.NoError(t, err)
	return descriptors
}
