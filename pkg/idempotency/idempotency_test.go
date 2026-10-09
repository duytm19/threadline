package idempotency_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/threadline/pkg/idempotency"
)

func TestComputeHash(t *testing.T) {
	payloadA := []byte(`{"sku":"TSHIRT-BLK-M","qty":2}`)
	payloadB := []byte(`{"sku":"TSHIRT-BLK-M","qty":2}`)
	payloadC := []byte(`{"sku":"TSHIRT-BLK-M","qty":3}`)

	hashA := idempotency.ComputeHash(payloadA)
	hashB := idempotency.ComputeHash(payloadB)
	hashC := idempotency.ComputeHash(payloadC)

	if hashA != hashB {
		t.Errorf("expected deterministic identical hash for identical payloads")
	}
	if hashA == hashC {
		t.Errorf("expected distinct hash for differing payloads")
	}
}

func TestValidateKey(t *testing.T) {
	validKey := uuid.NewString()
	if err := idempotency.ValidateKey(validKey); err != nil {
		t.Errorf("expected valid uuid to pass validation: %v", err)
	}

	invalidKey := "not-a-uuid"
	if err := idempotency.ValidateKey(invalidKey); err == nil {
		t.Errorf("expected invalid uuid to fail validation")
	}
}
