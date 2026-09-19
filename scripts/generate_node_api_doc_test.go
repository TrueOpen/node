package main

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The REST cells of this document are the only place many clients look for a
// path, so an empty or truncated cell reads as "not implemented". These tests
// pin the claims the generator now fails on rather than silently emitting.

func TestAssertRESTContractAcceptsTheFrozenSurface(t *testing.T) {
	if err := assertRESTContract(frozenQueryPaths()); err != nil {
		t.Fatalf("frozen surface rejected: %v", err)
	}
}

func TestAssertRESTContractRejectsMissingOrTruncatedPaths(t *testing.T) {
	for name, mutate := range map[string]func(map[string][]string){
		"no query methods at all": func(paths map[string][]string) {
			for key := range paths {
				delete(paths, key)
			}
		},
		"query with an empty path list": func(paths map[string][]string) {
			paths["hub.Model"] = nil
		},
		// The regression this generator shipped for: BuilderSet loses one of its
		// two frozen selector paths and the document still renders.
		"builder set with one selector path": func(paths map[string][]string) {
			paths["hub.BuilderSet"] = []string{"/TrueOpen/hub/v1/builder_set/by_height/{height}"}
		},
		"service descriptor without participant type": func(paths map[string][]string) {
			paths["hub.ServiceDescriptor"] = []string{"/TrueOpen/hub/v1/service_descriptor/{operator_address}"}
		},
		"models missing": func(paths map[string][]string) {
			delete(paths, "hub.Models")
		},
		"builders on the wrong path": func(paths map[string][]string) {
			paths["hub.Builders"] = []string{"/TrueOpen/hub/v1/builder"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			paths := frozenQueryPaths()
			mutate(paths)
			if err := assertRESTContract(paths); err == nil {
				t.Fatal("expected a contract failure")
			}
		})
	}
}

func TestRESTPathCellListsEveryBinding(t *testing.T) {
	cell := restPathCell([]string{"/a", "/b", "/c"})
	if got := strings.Count(cell, "<br>"); got != 2 {
		t.Fatalf("cell %q joined %d paths, want 3", cell, got+1)
	}
	for _, path := range []string{"`/a`", "`/b`", "`/c`"} {
		if !strings.Contains(cell, path) {
			t.Errorf("cell %q is missing %s", cell, path)
		}
	}
}

func TestFieldTypeUsesDescriptorIdentityInsteadOfFieldName(t *testing.T) {
	raw, err := os.ReadFile("../wire/wire.binpb")
	if err != nil {
		t.Fatal(err)
	}
	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(raw, set); err != nil {
		t.Fatal(err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		t.Fatal(err)
	}
	field := func(messageName protoreflect.FullName, fieldName protoreflect.Name) protoreflect.FieldDescriptor {
		descriptor, err := files.FindDescriptorByName(messageName)
		if err != nil {
			t.Fatal(err)
		}
		return descriptor.(protoreflect.MessageDescriptor).Fields().ByName(fieldName)
	}
	if got := fieldType(field("hub.v1.BuilderFaultState", "canonical_evidence_digest")); !strings.Contains(got, "Hash32") {
		t.Fatalf("BuilderFaultState.canonical_evidence_digest type %q does not use its Hash32 option", got)
	}
	// The unannotated half of the same-name pair has to be a message outside every
	// registered RPC closure; event streams are inside it now, so the internal
	// Task -> Hub fact is what carries the binary-only side.
	if got := fieldType(field("shared.v1.BuilderObjectiveEvidenceFactV2", "canonical_evidence_digest")); strings.Contains(got, "Hash32") {
		t.Fatalf("binary-only BuilderObjectiveEvidenceFactV2.canonical_evidence_digest inherited another field's option: %q", got)
	}
	if got := fieldType(field("task.v1.MsgBatchSubmitVerifyCommitResponse", "batch_digest")); strings.Contains(got, "base64") {
		t.Fatalf("unannotated binary-only batch_digest must not be documented as Base64: %q", got)
	}
	if got := fieldType(field("hub.v1.BeaconState", "proof_digest")); !strings.Contains(got, "optional") || !strings.Contains(got, "Hash32") {
		t.Fatalf("optional Hash32 presence is missing from type %q", got)
	}
}

func frozenQueryPaths() map[string][]string {
	return map[string][]string{
		"hub.Model":    {"/TrueOpen/hub/v1/model/{model_id}"},
		"hub.Models":   {"/TrueOpen/hub/v1/models"},
		"hub.Builder":  {"/TrueOpen/hub/v1/builder/{builder_address}"},
		"hub.Builders": {"/TrueOpen/hub/v1/builders"},
		"hub.BuilderSet": {
			"/TrueOpen/hub/v1/builder_set/by_height/{height}",
			"/TrueOpen/hub/v1/builder_set/by_id/{builder_set_id}",
		},
		"hub.ServiceDescriptor": {"/TrueOpen/hub/v1/service_descriptor/{participant_type}/{operator_address}"},
	}
}
