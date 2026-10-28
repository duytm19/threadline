package tokenfamily_test

import (
	"errors"
	"testing"
	"time"

	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

func TestState_StringAndPredicates(t *testing.T) {
	tests := []struct {
		name          string
		state         tokenfamily.State
		expectedStr   string
		isActive      bool
		isRotated     bool
		isCompromised bool
		isRevoked     bool
		isValid       bool
	}{
		{
			name:          "Active state",
			state:         tokenfamily.StateActive,
			expectedStr:   "ACTIVE",
			isActive:      true,
			isRotated:     false,
			isCompromised: false,
			isRevoked:     false,
			isValid:       true,
		},
		{
			name:          "Rotated state",
			state:         tokenfamily.StateRotated,
			expectedStr:   "ROTATED",
			isActive:      false,
			isRotated:     true,
			isCompromised: false,
			isRevoked:     false,
			isValid:       true,
		},
		{
			name:          "Compromised state",
			state:         tokenfamily.StateCompromised,
			expectedStr:   "COMPROMISED",
			isActive:      false,
			isRotated:     false,
			isCompromised: true,
			isRevoked:     false,
			isValid:       true,
		},
		{
			name:          "Revoked state",
			state:         tokenfamily.StateRevoked,
			expectedStr:   "REVOKED",
			isActive:      false,
			isRotated:     false,
			isCompromised: false,
			isRevoked:     true,
			isValid:       true,
		},
		{
			name:          "Invalid state",
			state:         tokenfamily.State("UNKNOWN"),
			expectedStr:   "UNKNOWN",
			isActive:      false,
			isRotated:     false,
			isCompromised: false,
			isRevoked:     false,
			isValid:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.state.String() != tc.expectedStr {
				t.Fatalf("expected string %q, got %q", tc.expectedStr, tc.state.String())
			}
			if tc.state.IsActive() != tc.isActive {
				t.Fatalf("expected IsActive %v, got %v", tc.isActive, tc.state.IsActive())
			}
			if tc.state.IsRotated() != tc.isRotated {
				t.Fatalf("expected IsRotated %v, got %v", tc.isRotated, tc.state.IsRotated())
			}
			if tc.state.IsCompromised() != tc.isCompromised {
				t.Fatalf("expected IsCompromised %v, got %v", tc.isCompromised, tc.state.IsCompromised())
			}
			if tc.state.IsRevoked() != tc.isRevoked {
				t.Fatalf("expected IsRevoked %v, got %v", tc.isRevoked, tc.state.IsRevoked())
			}
			if tc.state.IsValid() != tc.isValid {
				t.Fatalf("expected IsValid %v, got %v", tc.isValid, tc.state.IsValid())
			}
		})
	}
}

func TestNewTokenFamily(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(7 * 24 * time.Hour)
	rawToken := "rt_initial_secret_value"
	tokenHash := tokenfamily.HashToken(rawToken)

	t.Run("successfully creates active token family with initial token", func(t *testing.T) {
		family, err := tokenfamily.NewTokenFamily(
			"fam-01",
			"tenant-zalora-vn",
			"user-100",
			tokenHash,
			"jti-01",
			expiresAt,
			now,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if family.ID() != "fam-01" {
			t.Errorf("expected ID 'fam-01', got %q", family.ID())
		}
		if family.TenantID() != "tenant-zalora-vn" {
			t.Errorf("expected TenantID 'tenant-zalora-vn', got %q", family.TenantID())
		}
		if family.UserID() != "user-100" {
			t.Errorf("expected UserID 'user-100', got %q", family.UserID())
		}
		if family.State() != tokenfamily.StateActive {
			t.Errorf("expected state StateActive, got %q", family.State())
		}
		if family.Generation() != 0 {
			t.Errorf("expected initial generation 0, got %d", family.Generation())
		}
		if family.CurrentJTI() != "jti-01" {
			t.Errorf("expected CurrentJTI 'jti-01', got %q", family.CurrentJTI())
		}
		if family.CurrentHash() != tokenHash {
			t.Errorf("expected CurrentHash %q, got %q", tokenHash, family.CurrentHash())
		}
		if !family.CreatedAt().Equal(now) {
			t.Errorf("expected CreatedAt %v, got %v", now, family.CreatedAt())
		}
		if !family.UpdatedAt().Equal(now) {
			t.Errorf("expected UpdatedAt %v, got %v", now, family.UpdatedAt())
		}

		tokens := family.Tokens()
		if len(tokens) != 1 {
			t.Fatalf("expected 1 token, got %d", len(tokens))
		}
		tok := tokens[0]
		if tok.JTI() != "jti-01" {
			t.Errorf("expected token JTI 'jti-01', got %q", tok.JTI())
		}
		if tok.TokenHash() != tokenHash {
			t.Errorf("expected token hash %q, got %q", tokenHash, tok.TokenHash())
		}
		if tok.State() != tokenfamily.TokenStateActive {
			t.Errorf("expected token state TokenStateActive, got %q", tok.State())
		}
		if !tok.IssuedAt().Equal(now) {
			t.Errorf("expected IssuedAt %v, got %v", now, tok.IssuedAt())
		}
		if !tok.ExpiresAt().Equal(expiresAt) {
			t.Errorf("expected ExpiresAt %v, got %v", expiresAt, tok.ExpiresAt())
		}
		if _, used := tok.UsedAt(); used {
			t.Errorf("expected token not to be marked used")
		}

		events := family.Events()
		if len(events) != 1 {
			t.Fatalf("expected 1 domain event, got %d", len(events))
		}
		createdEvent, ok := events[0].(tokenfamily.TokenFamilyCreatedEvent)
		if !ok {
			t.Fatalf("expected TokenFamilyCreatedEvent, got %T", events[0])
		}
		if createdEvent.FamilyID != "fam-01" || createdEvent.TenantID != "tenant-zalora-vn" || createdEvent.UserID != "user-100" {
			t.Errorf("mismatched event payload: %+v", createdEvent)
		}

		family.ClearEvents()
		if len(family.Events()) != 0 {
			t.Errorf("expected 0 events after ClearEvents, got %d", len(family.Events()))
		}
	})

	t.Run("fails validation on empty inputs or past expiry", func(t *testing.T) {
		tests := []struct {
			name        string
			id          string
			tenantID    string
			userID      string
			tokenHash   string
			jti         string
			expiresAt   time.Time
			expectedErr error
		}{
			{
				name:        "empty family ID",
				id:          "",
				tenantID:    "tenant-1",
				userID:      "user-1",
				tokenHash:   tokenHash,
				jti:         "jti-1",
				expiresAt:   expiresAt,
				expectedErr: tokenfamily.ErrEmptyFamilyID,
			},
			{
				name:        "empty tenant ID (fail-closed per AD-4)",
				id:          "fam-1",
				tenantID:    "",
				userID:      "user-1",
				tokenHash:   tokenHash,
				jti:         "jti-1",
				expiresAt:   expiresAt,
				expectedErr: tokenfamily.ErrEmptyTenantID,
			},
			{
				name:        "empty user ID",
				id:          "fam-1",
				tenantID:    "tenant-1",
				userID:      "",
				tokenHash:   tokenHash,
				jti:         "jti-1",
				expiresAt:   expiresAt,
				expectedErr: tokenfamily.ErrEmptyUserID,
			},
			{
				name:        "empty token hash",
				id:          "fam-1",
				tenantID:    "tenant-1",
				userID:      "user-1",
				tokenHash:   "",
				jti:         "jti-1",
				expiresAt:   expiresAt,
				expectedErr: tokenfamily.ErrEmptyTokenHash,
			},
			{
				name:        "empty JTI",
				id:          "fam-1",
				tenantID:    "tenant-1",
				userID:      "user-1",
				tokenHash:   tokenHash,
				jti:         "",
				expiresAt:   expiresAt,
				expectedErr: tokenfamily.ErrEmptyJTI,
			},
			{
				name:        "expiry in the past",
				id:          "fam-1",
				tenantID:    "tenant-1",
				userID:      "user-1",
				tokenHash:   tokenHash,
				jti:         "jti-1",
				expiresAt:   now.Add(-1 * time.Minute),
				expectedErr: tokenfamily.ErrInvalidExpiry,
			},
			{
				name:        "expiry equal to now",
				id:          "fam-1",
				tenantID:    "tenant-1",
				userID:      "user-1",
				tokenHash:   tokenHash,
				jti:         "jti-1",
				expiresAt:   now,
				expectedErr: tokenfamily.ErrInvalidExpiry,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := tokenfamily.NewTokenFamily(
					tc.id,
					tc.tenantID,
					tc.userID,
					tc.tokenHash,
					tc.jti,
					tc.expiresAt,
					now,
				)
				if !errors.Is(err, tc.expectedErr) {
					t.Fatalf("expected error %v, got %v", tc.expectedErr, err)
				}
			})
		}
	})
}

func TestTokenFamily_Rotate_LegitimateRotation(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	expiresAt1 := now.Add(7 * 24 * time.Hour)
	tokenHash1 := tokenfamily.HashToken("rt-1-secret")

	family, err := tokenfamily.NewTokenFamily(
		"F-1",
		"tenant-zalora-vn",
		"USR-01",
		tokenHash1,
		"jti-rt-1",
		expiresAt1,
		now,
	)
	if err != nil {
		t.Fatalf("failed to create token family: %v", err)
	}
	family.ClearEvents()

	// Scenario 1: Legitimate Token Refresh Rotation
	rotationTime := now.Add(1 * time.Hour)
	expiresAt2 := rotationTime.Add(7 * 24 * time.Hour)
	tokenHash2 := tokenfamily.HashToken("rt-2-secret")

	newTok, err := family.Rotate(tokenHash1, tokenHash2, "jti-rt-2", expiresAt2, rotationTime)
	if err != nil {
		t.Fatalf("expected successful rotation, got: %v", err)
	}

	if newTok.JTI() != "jti-rt-2" {
		t.Errorf("expected new token JTI 'jti-rt-2', got %q", newTok.JTI())
	}
	if newTok.TokenHash() != tokenHash2 {
		t.Errorf("expected new token hash %q, got %q", tokenHash2, newTok.TokenHash())
	}
	if newTok.State() != tokenfamily.TokenStateActive {
		t.Errorf("expected new token state Active, got %q", newTok.State())
	}

	// State transitions from Active to Rotated
	if family.State() != tokenfamily.StateRotated {
		t.Errorf("expected family state StateRotated, got %q", family.State())
	}
	if family.Generation() != 1 {
		t.Errorf("expected generation 1, got %d", family.Generation())
	}
	if family.CurrentJTI() != "jti-rt-2" {
		t.Errorf("expected CurrentJTI 'jti-rt-2', got %q", family.CurrentJTI())
	}
	if family.CurrentHash() != tokenHash2 {
		t.Errorf("expected CurrentHash %q, got %q", tokenHash2, family.CurrentHash())
	}
	if !family.UpdatedAt().Equal(rotationTime) {
		t.Errorf("expected UpdatedAt %v, got %v", rotationTime, family.UpdatedAt())
	}

	// Verify old token RT-1 is marked as used
	oldTok, found := family.TokenByHash(tokenHash1)
	if !found {
		t.Fatalf("expected old token RT-1 to exist in family")
	}
	if oldTok.State() != tokenfamily.TokenStateUsed {
		t.Errorf("expected RT-1 state TokenStateUsed, got %q", oldTok.State())
	}
	usedAt, used := oldTok.UsedAt()
	if !used || !usedAt.Equal(rotationTime) {
		t.Errorf("expected RT-1 usedAt %v, got %v (used: %v)", rotationTime, usedAt, used)
	}

	// Verify domain event
	events := family.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	rotatedEvent, ok := events[0].(tokenfamily.TokenFamilyRotatedEvent)
	if !ok {
		t.Fatalf("expected TokenFamilyRotatedEvent, got %T", events[0])
	}
	if rotatedEvent.FamilyID != "F-1" || rotatedEvent.OldTokenJTI != "jti-rt-1" || rotatedEvent.NewTokenJTI != "jti-rt-2" || rotatedEvent.Generation != 1 {
		t.Errorf("mismatched rotated event: %+v", rotatedEvent)
	}

	// Rotate a second time (RT-2 -> RT-3)
	secondRotationTime := rotationTime.Add(2 * time.Hour)
	expiresAt3 := secondRotationTime.Add(7 * 24 * time.Hour)
	tokenHash3 := tokenfamily.HashToken("rt-3-secret")

	family.ClearEvents()
	thirdTok, err := family.Rotate(tokenHash2, tokenHash3, "jti-rt-3", expiresAt3, secondRotationTime)
	if err != nil {
		t.Fatalf("expected successful second rotation, got: %v", err)
	}
	if thirdTok.JTI() != "jti-rt-3" {
		t.Errorf("expected third token JTI 'jti-rt-3', got %q", thirdTok.JTI())
	}
	if family.State() != tokenfamily.StateRotated {
		t.Errorf("expected family state to remain StateRotated, got %q", family.State())
	}
	if family.Generation() != 2 {
		t.Errorf("expected generation 2, got %d", family.Generation())
	}
	if family.CurrentJTI() != "jti-rt-3" {
		t.Errorf("expected CurrentJTI 'jti-rt-3', got %q", family.CurrentJTI())
	}
}

func TestTokenFamily_Rotate_MaliciousReplayReuseDetection(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	expiresAt1 := now.Add(7 * 24 * time.Hour)
	tokenHash1 := tokenfamily.HashToken("rt-1-secret")

	family, err := tokenfamily.NewTokenFamily(
		"F-1",
		"tenant-zalora-vn",
		"USR-01",
		tokenHash1,
		"jti-rt-1",
		expiresAt1,
		now,
	)
	if err != nil {
		t.Fatalf("failed to create token family: %v", err)
	}

	// Rotate RT-1 to RT-2 legitimately
	rotationTime := now.Add(1 * time.Hour)
	expiresAt2 := rotationTime.Add(7 * 24 * time.Hour)
	tokenHash2 := tokenfamily.HashToken("rt-2-secret")

	_, err = family.Rotate(tokenHash1, tokenHash2, "jti-rt-2", expiresAt2, rotationTime)
	if err != nil {
		t.Fatalf("first rotation failed: %v", err)
	}
	family.ClearEvents()

	// Scenario 2: Malicious Replay of Already-Used Token (RT-1 presented again)
	attackerAttemptTime := rotationTime.Add(10 * time.Minute)
	attackerHash := tokenfamily.HashToken("attacker-rt-secret")

	_, err = family.Rotate(tokenHash1, attackerHash, "jti-attacker", attackerAttemptTime.Add(7*24*time.Hour), attackerAttemptTime)
	if err == nil {
		t.Fatalf("expected error on replay of already-used token, got nil")
	}
	if !errors.Is(err, tokenfamily.ErrTokenReused) {
		t.Fatalf("expected ErrTokenReused, got: %v", err)
	}

	// Invariants asserting Compromised state
	if family.State() != tokenfamily.StateCompromised {
		t.Errorf("expected family state StateCompromised, got %q", family.State())
	}
	compAt, compromised := family.CompromisedAt()
	if !compromised || !compAt.Equal(attackerAttemptTime) {
		t.Errorf("expected CompromisedAt %v, got %v (compromised: %v)", attackerAttemptTime, compAt, compromised)
	}
	if family.CompromiseReason() == "" {
		t.Errorf("expected non-empty compromise reason")
	}

	// Verify security event with A07_TOKEN_REUSE
	events := family.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	compEvent, ok := events[0].(tokenfamily.TokenFamilyCompromisedEvent)
	if !ok {
		t.Fatalf("expected TokenFamilyCompromisedEvent, got %T", events[0])
	}
	if compEvent.FamilyID != "F-1" {
		t.Errorf("expected FamilyID 'F-1', got %q", compEvent.FamilyID)
	}
	if compEvent.AlertCode != tokenfamily.SecurityAlertTokenReuse {
		t.Errorf("expected AlertCode %q, got %q", tokenfamily.SecurityAlertTokenReuse, compEvent.AlertCode)
	}
	if compEvent.ReplayedTokenJTI != "jti-rt-1" {
		t.Errorf("expected ReplayedTokenJTI 'jti-rt-1', got %q", compEvent.ReplayedTokenJTI)
	}

	// Invariant: RT-2 is now blacklisted/revoked due to family compromise
	tok2, found := family.TokenByHash(tokenHash2)
	if !found {
		t.Fatalf("expected token RT-2 to be found")
	}
	if tok2.State() != tokenfamily.TokenStateRevoked {
		t.Errorf("expected RT-2 to be revoked after compromise, got %q", tok2.State())
	}

	// Invariant: Once compromised, subsequent rotation attempts (even with RT-2 or RT-1) must be rejected
	_, err = family.Rotate(tokenHash2, tokenfamily.HashToken("rt-new"), "jti-new", attackerAttemptTime.Add(24*time.Hour), attackerAttemptTime.Add(1*time.Minute))
	if !errors.Is(err, tokenfamily.ErrFamilyCompromised) {
		t.Fatalf("expected ErrFamilyCompromised, got: %v", err)
	}
}

func TestTokenFamily_Rotate_EdgeCases(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	expiresAt1 := now.Add(2 * time.Hour)
	tokenHash1 := tokenfamily.HashToken("rt-1-secret")

	t.Run("fails when token is not found in family", func(t *testing.T) {
		family, _ := tokenfamily.NewTokenFamily("F-1", "tenant-1", "user-1", tokenHash1, "jti-1", expiresAt1, now)
		unknownHash := tokenfamily.HashToken("unknown-token")

		_, err := family.Rotate(unknownHash, tokenfamily.HashToken("rt-new"), "jti-new", now.Add(24*time.Hour), now.Add(1*time.Hour))
		if !errors.Is(err, tokenfamily.ErrTokenNotFound) {
			t.Fatalf("expected ErrTokenNotFound, got: %v", err)
		}
		// Family should not be compromised by unknown token
		if family.State() != tokenfamily.StateActive {
			t.Errorf("family state should remain Active, got %q", family.State())
		}
	})

	t.Run("fails when token has expired without compromising family", func(t *testing.T) {
		family, _ := tokenfamily.NewTokenFamily("F-1", "tenant-1", "user-1", tokenHash1, "jti-1", expiresAt1, now)
		expiredTime := expiresAt1.Add(10 * time.Minute)

		_, err := family.Rotate(tokenHash1, tokenfamily.HashToken("rt-new"), "jti-new", expiredTime.Add(24*time.Hour), expiredTime)
		if !errors.Is(err, tokenfamily.ErrTokenExpired) {
			t.Fatalf("expected ErrTokenExpired, got: %v", err)
		}
		// Natural expiration is not an intrusion; family is not compromised
		if family.State() != tokenfamily.StateActive {
			t.Errorf("family state should remain Active, got %q", family.State())
		}
	})

	t.Run("fails when new token parameters are invalid", func(t *testing.T) {
		family, _ := tokenfamily.NewTokenFamily("F-1", "tenant-1", "user-1", tokenHash1, "jti-1", expiresAt1, now)

		// Empty new token hash
		_, err := family.Rotate(tokenHash1, "", "jti-new", now.Add(24*time.Hour), now.Add(10*time.Minute))
		if !errors.Is(err, tokenfamily.ErrEmptyTokenHash) {
			t.Errorf("expected ErrEmptyTokenHash, got: %v", err)
		}

		// Empty new JTI
		_, err = family.Rotate(tokenHash1, tokenfamily.HashToken("rt-new"), "", now.Add(24*time.Hour), now.Add(10*time.Minute))
		if !errors.Is(err, tokenfamily.ErrEmptyJTI) {
			t.Errorf("expected ErrEmptyJTI, got: %v", err)
		}

		// Past expiry for new token
		_, err = family.Rotate(tokenHash1, tokenfamily.HashToken("rt-new"), "jti-new", now, now.Add(10*time.Minute))
		if !errors.Is(err, tokenfamily.ErrInvalidExpiry) {
			t.Errorf("expected ErrInvalidExpiry, got: %v", err)
		}

		// Reused JTI
		_, err = family.Rotate(tokenHash1, tokenfamily.HashToken("rt-new"), "jti-1", now.Add(24*time.Hour), now.Add(10*time.Minute))
		if !errors.Is(err, tokenfamily.ErrTokenConflict) {
			t.Errorf("expected ErrTokenConflict for duplicate JTI, got: %v", err)
		}

		// Reused token hash
		_, err = family.Rotate(tokenHash1, tokenHash1, "jti-new-2", now.Add(24*time.Hour), now.Add(10*time.Minute))
		if !errors.Is(err, tokenfamily.ErrTokenConflict) {
			t.Errorf("expected ErrTokenConflict for duplicate hash, got: %v", err)
		}
	})

	t.Run("fails when rotating a revoked family", func(t *testing.T) {
		family, _ := tokenfamily.NewTokenFamily("F-1", "tenant-1", "user-1", tokenHash1, "jti-1", expiresAt1, now)
		err := family.Revoke("user_logout", now.Add(15*time.Minute))
		if err != nil {
			t.Fatalf("unexpected error revoking: %v", err)
		}

		if family.State() != tokenfamily.StateRevoked {
			t.Errorf("expected state StateRevoked, got %q", family.State())
		}

		_, err = family.Rotate(tokenHash1, tokenfamily.HashToken("rt-new"), "jti-new", now.Add(24*time.Hour), now.Add(20*time.Minute))
		if !errors.Is(err, tokenfamily.ErrFamilyRevoked) {
			t.Fatalf("expected ErrFamilyRevoked, got: %v", err)
		}
	})
}

func TestTokenFamily_Compromise(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	family, err := tokenfamily.NewTokenFamily(
		"F-1",
		"tenant-1",
		"user-1",
		tokenfamily.HashToken("rt-1"),
		"jti-1",
		now.Add(24*time.Hour),
		now,
	)
	if err != nil {
		t.Fatalf("failed to create family: %v", err)
	}
	family.ClearEvents()

	compTime := now.Add(30 * time.Minute)
	err = family.Compromise("manual_security_lockdown", compTime)
	if err != nil {
		t.Fatalf("expected compromise to succeed, got: %v", err)
	}

	if family.State() != tokenfamily.StateCompromised {
		t.Errorf("expected StateCompromised, got %q", family.State())
	}
	if family.CompromiseReason() != "manual_security_lockdown" {
		t.Errorf("expected reason 'manual_security_lockdown', got %q", family.CompromiseReason())
	}

	// Idempotent compromise
	err = family.Compromise("second_call", compTime.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("expected idempotent second compromise to return nil, got: %v", err)
	}
	if family.CompromiseReason() != "manual_security_lockdown" {
		t.Errorf("original compromise reason should be preserved")
	}
}

func TestTokenFamily_Revoke(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	family, err := tokenfamily.NewTokenFamily(
		"F-1",
		"tenant-1",
		"user-1",
		tokenfamily.HashToken("rt-1"),
		"jti-1",
		now.Add(24*time.Hour),
		now,
	)
	if err != nil {
		t.Fatalf("failed to create family: %v", err)
	}
	family.ClearEvents()

	revokeTime := now.Add(10 * time.Minute)
	err = family.Revoke("user_sign_out", revokeTime)
	if err != nil {
		t.Fatalf("expected revocation to succeed: %v", err)
	}
	if family.State() != tokenfamily.StateRevoked {
		t.Errorf("expected StateRevoked, got %q", family.State())
	}

	events := family.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	revEvent, ok := events[0].(tokenfamily.TokenFamilyRevokedEvent)
	if !ok {
		t.Fatalf("expected TokenFamilyRevokedEvent, got %T", events[0])
	}
	if revEvent.FamilyID != "F-1" || revEvent.Reason != "user_sign_out" {
		t.Errorf("mismatched revocation event: %+v", revEvent)
	}

	// Revoking compromised family should fail
	compFamily, _ := tokenfamily.NewTokenFamily("F-2", "tenant-1", "user-1", tokenfamily.HashToken("rt-2"), "jti-2", now.Add(24*time.Hour), now)
	_ = compFamily.Compromise("breach", now)
	err = compFamily.Revoke("sign_out", now)
	if !errors.Is(err, tokenfamily.ErrFamilyCompromised) {
		t.Errorf("expected ErrFamilyCompromised when revoking compromised family, got: %v", err)
	}
}

func TestReconstituteTokenFamily(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	expiresAt := now.Add(7 * 24 * time.Hour)
	usedTime := now.Add(2 * time.Hour)

	tok1 := tokenfamily.ReconstituteRefreshToken("jti-1", "hash-1", tokenfamily.TokenStateUsed, now, expiresAt, &usedTime)
	tok2 := tokenfamily.ReconstituteRefreshToken("jti-2", "hash-2", tokenfamily.TokenStateActive, usedTime, expiresAt.Add(24*time.Hour), nil)

	family, err := tokenfamily.ReconstituteTokenFamily(
		"fam-recon",
		"tenant-1",
		"user-1",
		tokenfamily.StateRotated,
		1,
		"jti-2",
		"hash-2",
		[]tokenfamily.RefreshToken{tok1, tok2},
		nil,
		"",
		now,
		usedTime,
	)
	if err != nil {
		t.Fatalf("failed to reconstitute token family: %v", err)
	}

	if family.ID() != "fam-recon" {
		t.Errorf("expected ID 'fam-recon', got %q", family.ID())
	}
	if family.State() != tokenfamily.StateRotated {
		t.Errorf("expected StateRotated, got %q", family.State())
	}
	if family.Generation() != 1 {
		t.Errorf("expected generation 1, got %d", family.Generation())
	}
	if len(family.Tokens()) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(family.Tokens()))
	}
	// Reconstitution should not emit domain events
	if len(family.Events()) != 0 {
		t.Errorf("expected 0 events on reconstitution, got %d", len(family.Events()))
	}
}
