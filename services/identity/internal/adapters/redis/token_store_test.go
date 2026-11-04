package redis_test

import (
	"errors"
	"strings"
	"testing"

	adapter "github.com/threadline/services/identity/internal/adapters/redis"
	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

func TestKeys_FailClosedValidation(t *testing.T) {
	t.Run("empty tenant_id fails closed per AD-4", func(t *testing.T) {
		_, err := adapter.KeyPrefix("")
		if !errors.Is(err, adapter.ErrEmptyTenantID) {
			t.Fatalf("expected ErrEmptyTenantID, got: %v", err)
		}

		_, err = adapter.TokenKey("", "hash-123")
		if !errors.Is(err, adapter.ErrEmptyTenantID) {
			t.Fatalf("expected ErrEmptyTenantID, got: %v", err)
		}

		_, err = adapter.FamilyKey("", "fam-123")
		if !errors.Is(err, adapter.ErrEmptyTenantID) {
			t.Fatalf("expected ErrEmptyTenantID, got: %v", err)
		}

		_, err = adapter.FamilyTokensKey("", "fam-123")
		if !errors.Is(err, adapter.ErrEmptyTenantID) {
			t.Fatalf("expected ErrEmptyTenantID, got: %v", err)
		}
	})

	t.Run("empty token_hash or family_id returns error", func(t *testing.T) {
		_, err := adapter.TokenKey("tenant-1", "")
		if !errors.Is(err, adapter.ErrEmptyTokenHash) {
			t.Fatalf("expected ErrEmptyTokenHash, got: %v", err)
		}

		_, err = adapter.FamilyKey("tenant-1", "")
		if !errors.Is(err, adapter.ErrEmptyFamilyID) {
			t.Fatalf("expected ErrEmptyFamilyID, got: %v", err)
		}

		_, err = adapter.FamilyTokensKey("tenant-1", "")
		if !errors.Is(err, adapter.ErrEmptyFamilyID) {
			t.Fatalf("expected ErrEmptyFamilyID, got: %v", err)
		}
	})

	t.Run("valid keys include hash tags for Redis cluster slot affinity", func(t *testing.T) {
		tenantID := "zalora-vn"
		tokenHash := "abcdef123456"
		familyID := "fam-999"

		prefix, err := adapter.KeyPrefix(tenantID)
		if err != nil || prefix != "thrd:{zalora-vn}" {
			t.Fatalf("unexpected prefix: %s, err: %v", prefix, err)
		}

		tokKey, err := adapter.TokenKey(tenantID, tokenHash)
		if err != nil || tokKey != "thrd:{zalora-vn}:token:abcdef123456" {
			t.Fatalf("unexpected token key: %s, err: %v", tokKey, err)
		}

		famKey, err := adapter.FamilyKey(tenantID, familyID)
		if err != nil || famKey != "thrd:{zalora-vn}:family:fam-999" {
			t.Fatalf("unexpected family key: %s, err: %v", famKey, err)
		}

		famTokensKey, err := adapter.FamilyTokensKey(tenantID, familyID)
		if err != nil || famTokensKey != "thrd:{zalora-vn}:family:fam-999:tokens" {
			t.Fatalf("unexpected family tokens key: %s, err: %v", famTokensKey, err)
		}
	})
}

func TestLuaScripts_EmbeddedAndValid(t *testing.T) {
	if strings.TrimSpace(adapter.ExchangeTokenScriptSrc) == "" {
		t.Fatal("ExchangeTokenScriptSrc is empty")
	}
	if !strings.Contains(adapter.ExchangeTokenScriptSrc, "A07_TOKEN_REUSE") {
		t.Fatal("ExchangeTokenScriptSrc does not mention A07_TOKEN_REUSE")
	}
	if !strings.Contains(adapter.ExchangeTokenScriptSrc, "TOKEN_REUSED") {
		t.Fatal("ExchangeTokenScriptSrc does not return TOKEN_REUSED")
	}

	if strings.TrimSpace(adapter.CreateFamilyScriptSrc) == "" {
		t.Fatal("CreateFamilyScriptSrc is empty")
	}
	if strings.TrimSpace(adapter.RevokeFamilyScriptSrc) == "" {
		t.Fatal("RevokeFamilyScriptSrc is empty")
	}
}

func TestParseLuaResult(t *testing.T) {
	t.Run("successfully parses alternating slice into map", func(t *testing.T) {
		raw := []interface{}{
			"STATUS", "SUCCESS",
			"FAMILY_ID", "fam-01",
			"GENERATION", "2",
		}

		res, err := adapter.ParseLuaResult(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res["STATUS"] != "SUCCESS" {
			t.Errorf("expected STATUS=SUCCESS, got %q", res["STATUS"])
		}
		if res["FAMILY_ID"] != "fam-01" {
			t.Errorf("expected FAMILY_ID=fam-01, got %q", res["FAMILY_ID"])
		}
		if res["GENERATION"] != "2" {
			t.Errorf("expected GENERATION=2, got %q", res["GENERATION"])
		}
	})

	t.Run("fails on non-slice input", func(t *testing.T) {
		_, err := adapter.ParseLuaResult("invalid")
		if !errors.Is(err, adapter.ErrUnexpectedLuaResponse) {
			t.Fatalf("expected ErrUnexpectedLuaResponse, got: %v", err)
		}
	})
}

func TestMapStatusToError(t *testing.T) {
	tests := []struct {
		status      string
		expectedErr error
	}{
		{"SUCCESS", nil},
		{"TOKEN_NOT_FOUND", tokenfamily.ErrTokenNotFound},
		{"TOKEN_EXPIRED", tokenfamily.ErrTokenExpired},
		{"TOKEN_REUSED", tokenfamily.ErrTokenReused},
		{"FAMILY_COMPROMISED", tokenfamily.ErrFamilyCompromised},
		{"FAMILY_REVOKED", tokenfamily.ErrFamilyRevoked},
		{"TOKEN_REVOKED", tokenfamily.ErrFamilyRevoked},
		{"CROSS_TENANT_VIOLATION", adapter.ErrCrossTenantViolation},
		{"FAMILY_ALREADY_EXISTS", adapter.ErrFamilyAlreadyExists},
		{"FAMILY_NOT_FOUND", adapter.ErrFamilyNotFound},
		{"CORRUPTED_TOKEN_DATA", adapter.ErrCorruptedTokenData},
	}

	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			err := adapter.MapStatusToError(tc.status)
			if tc.expectedErr == nil {
				if err != nil {
					t.Fatalf("expected nil error, got: %v", err)
				}
			} else {
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected %v, got: %v", tc.expectedErr, err)
				}
			}
		})
	}
}
