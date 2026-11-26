package handlers

import (
	"encoding/json"
	"testing"

	"github.com/vietbui/chat-quality-agent/api/contracts"
)

func TestNormalizePersonalZaloAccountOwnersRejectsAmbiguousBootstrapMappings(t *testing.T) {
	users := map[string]TenantUserResponse{
		"user-1": {UserID: "user-1", Name: "User 1", Email: "u1@example.com"},
		"user-2": {UserID: "user-2", Name: "User 2", Email: "u2@example.com"},
	}

	_, err := normalizePersonalZaloAccountOwners([]contracts.PersonalZaloAccountOwner{
		{UserID: "user-1"},
		{UserID: "user-2"},
	}, users)
	if err == nil {
		t.Fatalf("expected ambiguous bootstrap mappings to fail")
	}
}

func TestEncodeAndDecodePersonalZaloAccountOwnersPreservesExistingMetadata(t *testing.T) {
	raw := `{"sync_interval":15,"sync_files":true}`
	owners := []contracts.PersonalZaloAccountOwner{
		{AccountExternalID: "zalo-1", UserID: "user-1", UserName: "User 1", UserEmail: "u1@example.com"},
	}

	encoded, err := encodePersonalZaloAccountOwners(raw, owners)
	if err != nil {
		t.Fatalf("encodePersonalZaloAccountOwners returned error: %v", err)
	}

	decodedOwners, err := decodePersonalZaloAccountOwners(encoded)
	if err != nil {
		t.Fatalf("decodePersonalZaloAccountOwners returned error: %v", err)
	}
	if len(decodedOwners) != 1 {
		t.Fatalf("expected 1 owner, got %d", len(decodedOwners))
	}

	meta, err := decodeMetadataObject(encoded)
	if err != nil {
		t.Fatalf("decodeMetadataObject returned error: %v", err)
	}
	if meta["sync_interval"].(float64) != 15 {
		t.Fatalf("expected sync_interval to be preserved")
	}
	if meta["sync_files"].(bool) != true {
		t.Fatalf("expected sync_files to be preserved")
	}
}

func TestMergePersonalZaloMetadataPreservingAccountOwners(t *testing.T) {
	existing := `{"sync_interval":15,"sync_scope":"direct","account_owners":[{"account_external_id":"zalo-1","user_id":"user-1"}]}`
	incoming := `{"sync_interval":30,"sync_files":true,"session_keyword":"Chào bot"}`

	merged, err := mergeChannelMetadata(existing, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if merged["sync_scope"] != "direct" || merged["session_keyword"] != "Chào bot" {
		t.Fatal("sync scope and chatbot settings must survive the same metadata update")
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	owners, err := decodePersonalZaloAccountOwners(string(raw))
	if err != nil {
		t.Fatalf("decodePersonalZaloAccountOwners returned error: %v", err)
	}
	if len(owners) != 1 || owners[0].AccountExternalID != "zalo-1" {
		t.Fatalf("expected account owners to survive metadata update")
	}
}
