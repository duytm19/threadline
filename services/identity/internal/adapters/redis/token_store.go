package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/threadline/services/identity/internal/application/ports"
	"github.com/threadline/services/identity/internal/domain/tokenfamily"
)

// TokenStore implements the ports.TokenStore driven port using Redis and atomic Lua scripts.
type TokenStore struct {
	client goredis.UniversalClient
}

// NewTokenStore initializes a TokenStore with a Redis universal client.
func NewTokenStore(client goredis.UniversalClient) *TokenStore {
	return &TokenStore{
		client: client,
	}
}

var _ ports.TokenStore = (*TokenStore)(nil)

// CreateFamily persists a newly created TokenFamily and its initial active RefreshToken into Redis atomically.
func (s *TokenStore) CreateFamily(ctx context.Context, family *tokenfamily.TokenFamily, ttl time.Duration) error {
	if family == nil {
		return tokenfamily.ErrEmptyFamilyID
	}
	tenantID := family.TenantID()
	familyID := family.ID()

	famKey, err := FamilyKey(tenantID, familyID)
	if err != nil {
		return err
	}
	famTokensKey, err := FamilyTokensKey(tenantID, familyID)
	if err != nil {
		return err
	}

	tokens := family.Tokens()
	if len(tokens) == 0 {
		return tokenfamily.ErrEmptyTokenHash
	}
	initialTok := tokens[0]

	tokKey, err := TokenKey(tenantID, initialTok.TokenHash())
	if err != nil {
		return err
	}

	keys := []string{famKey, famTokensKey, tokKey}
	now := family.CreatedAt()
	nowStr := now.Format(time.RFC3339)
	nowUnix := strconv.FormatInt(now.Unix(), 10)
	expiresAtStr := initialTok.ExpiresAt().Format(time.RFC3339)
	ttlSec := strconv.FormatInt(int64(ttl.Seconds()), 10)

	args := []interface{}{
		tenantID,
		familyID,
		family.UserID(),
		initialTok.JTI(),
		initialTok.TokenHash(),
		nowStr,
		nowUnix,
		expiresAtStr,
		ttlSec,
	}

	rawRes, err := createFamilyScript.Run(ctx, s.client, keys, args...).Result()
	if err != nil {
		return fmt.Errorf("redis: failed to execute create_family script: %w", err)
	}

	resMap, err := ParseLuaResult(rawRes)
	if err != nil {
		return err
	}

	status := resMap["STATUS"]
	return MapStatusToError(status)
}

// ExchangeToken atomically executes the Lua script for token exchange and reuse detection.
func (s *TokenStore) ExchangeToken(
	ctx context.Context,
	tenantID string,
	presentedTokenHash string,
	newTokenHash string,
	newJTI string,
	newExpiresAt time.Time,
	now time.Time,
	ttl time.Duration,
) (*ports.ExchangeResult, error) {
	if tenantID == "" {
		return nil, ErrEmptyTenantID
	}
	if presentedTokenHash == "" || newTokenHash == "" {
		return nil, ErrEmptyTokenHash
	}
	if newJTI == "" {
		return nil, tokenfamily.ErrEmptyJTI
	}

	presentedKey, err := TokenKey(tenantID, presentedTokenHash)
	if err != nil {
		return nil, err
	}
	newKey, err := TokenKey(tenantID, newTokenHash)
	if err != nil {
		return nil, err
	}

	// We pass KEYS[1]=presentedKey and KEYS[2]=newKey.
	// KEYS[3] and KEYS[4] are left empty ("") to allow Lua script to dynamically derive them
	// from the presented token's family_id while maintaining cluster hash tag compatibility.
	keys := []string{presentedKey, newKey, "", ""}

	nowStr := now.Format(time.RFC3339)
	nowUnix := strconv.FormatInt(now.Unix(), 10)
	newExpiresAtStr := newExpiresAt.Format(time.RFC3339)
	ttlSec := strconv.FormatInt(int64(ttl.Seconds()), 10)

	args := []interface{}{
		tenantID,
		newJTI,
		newTokenHash,
		nowStr,
		nowUnix,
		newExpiresAtStr,
		ttlSec,
	}

	rawRes, err := exchangeTokenScript.Run(ctx, s.client, keys, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: failed to execute exchange_token script: %w", err)
	}

	resMap, err := ParseLuaResult(rawRes)
	if err != nil {
		return nil, err
	}

	status := resMap["STATUS"]
	if status != "SUCCESS" {
		return nil, MapStatusToError(status)
	}

	gen, _ := strconv.Atoi(resMap["GENERATION"])

	return &ports.ExchangeResult{
		FamilyID:    resMap["FAMILY_ID"],
		TenantID:    resMap["TENANT_ID"],
		UserID:      resMap["USER_ID"],
		OldTokenJTI: resMap["OLD_JTI"],
		NewTokenJTI: resMap["NEW_JTI"],
		Generation:  gen,
		RotatedAt:   now,
	}, nil
}

// GetFamily fetches a TokenFamily and all its associated tokens from Redis and reconstitutes the domain aggregate.
func (s *TokenStore) GetFamily(ctx context.Context, tenantID, familyID string) (*tokenfamily.TokenFamily, error) {
	famKey, err := FamilyKey(tenantID, familyID)
	if err != nil {
		return nil, err
	}

	famData, err := s.client.HGetAll(ctx, famKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: failed to get family metadata: %w", err)
	}
	if len(famData) == 0 {
		return nil, ErrFamilyNotFound
	}

	famTokensKey, err := FamilyTokensKey(tenantID, familyID)
	if err != nil {
		return nil, err
	}

	tokenHashes, err := s.client.SMembers(ctx, famTokensKey).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: failed to get family token hashes: %w", err)
	}

	tokens := make([]tokenfamily.RefreshToken, 0, len(tokenHashes))
	for _, th := range tokenHashes {
		tokKey, err := TokenKey(tenantID, th)
		if err != nil {
			continue
		}
		tokData, err := s.client.HGetAll(ctx, tokKey).Result()
		if err != nil || len(tokData) == 0 {
			continue
		}

		jti := tokData["jti"]
		tokState := tokenfamily.TokenState(tokData["state"])
		issuedAt, _ := time.Parse(time.RFC3339, tokData["issued_at"])
		expiresAt, _ := time.Parse(time.RFC3339, tokData["expires_at"])

		var usedAt *time.Time
		if uStr, exists := tokData["used_at"]; exists && uStr != "" {
			if t, err := time.Parse(time.RFC3339, uStr); err == nil {
				usedAt = &t
			}
		}

		tok := tokenfamily.ReconstituteRefreshToken(
			jti,
			th,
			tokState,
			issuedAt,
			expiresAt,
			usedAt,
		)
		tokens = append(tokens, tok)
	}

	gen, _ := strconv.Atoi(famData["generation"])
	state := tokenfamily.State(famData["state"])
	createdAt, _ := time.Parse(time.RFC3339, famData["created_at"])
	updatedAt, _ := time.Parse(time.RFC3339, famData["updated_at"])

	var compromisedAt *time.Time
	if cStr, exists := famData["compromised_at"]; exists && cStr != "" {
		if t, err := time.Parse(time.RFC3339, cStr); err == nil {
			compromisedAt = &t
		}
	}

	return tokenfamily.ReconstituteTokenFamily(
		familyID,
		tenantID,
		famData["user_id"],
		state,
		gen,
		famData["current_jti"],
		famData["current_hash"],
		tokens,
		compromisedAt,
		famData["compromise_reason"],
		createdAt,
		updatedAt,
	)
}

// RevokeFamily terminates a TokenFamily and invalidates its active tokens in Redis.
func (s *TokenStore) RevokeFamily(ctx context.Context, tenantID, familyID, reason string, now time.Time) error {
	return s.setFinalFamilyState(ctx, tenantID, familyID, "REVOKED", reason, now)
}

// CompromiseFamily flags a TokenFamily as compromised and invalidates its active tokens in Redis.
func (s *TokenStore) CompromiseFamily(ctx context.Context, tenantID, familyID, reason string, now time.Time) error {
	return s.setFinalFamilyState(ctx, tenantID, familyID, "COMPROMISED", reason, now)
}

func (s *TokenStore) setFinalFamilyState(
	ctx context.Context,
	tenantID string,
	familyID string,
	targetState string,
	reason string,
	now time.Time,
) error {
	famKey, err := FamilyKey(tenantID, familyID)
	if err != nil {
		return err
	}
	famTokensKey, err := FamilyTokensKey(tenantID, familyID)
	if err != nil {
		return err
	}

	keys := []string{famKey, famTokensKey}
	nowStr := now.Format(time.RFC3339)
	ttlSec := strconv.FormatInt(int64((30 * 24 * time.Hour).Seconds()), 10)

	args := []interface{}{
		tenantID,
		targetState,
		reason,
		nowStr,
		ttlSec,
	}

	rawRes, err := revokeFamilyScript.Run(ctx, s.client, keys, args...).Result()
	if err != nil {
		return fmt.Errorf("redis: failed to execute revoke_family script: %w", err)
	}

	resMap, err := ParseLuaResult(rawRes)
	if err != nil {
		return err
	}

	return MapStatusToError(resMap["STATUS"])
}
