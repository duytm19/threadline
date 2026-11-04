-- exchange_token.lua
-- Atomic Refresh Token Rotation with Active Reuse Detection
--
-- KEYS[1] = presented_token_key  (e.g., thrd:{tenant}:token:{presented_hash})
-- KEYS[2] = new_token_key        (e.g., thrd:{tenant}:token:{new_hash})
-- KEYS[3] = family_key           (e.g., thrd:{tenant}:family:{family_id}, optional if empty)
-- KEYS[4] = family_tokens_key    (e.g., thrd:{tenant}:family:{family_id}:tokens, optional if empty)
--
-- ARGV[1] = tenant_id            (string)
-- ARGV[2] = new_jti              (string)
-- ARGV[3] = new_token_hash       (string)
-- ARGV[4] = now_str              (RFC3339 string)
-- ARGV[5] = now_unix             (integer unix seconds)
-- ARGV[6] = new_expires_at_str   (RFC3339 string)
-- ARGV[7] = ttl_seconds          (integer seconds)

local presented_token_key = KEYS[1]
local new_token_key       = KEYS[2]

local tenant_id          = ARGV[1]
local new_jti            = ARGV[2]
local new_token_hash     = ARGV[3]
local now_str            = ARGV[4]
local now_unix           = tonumber(ARGV[5])
local new_expires_at_str = ARGV[6]
local ttl_seconds        = tonumber(ARGV[7])

-- 1. Fetch presented token data
local token_data = redis.call('HGETALL', presented_token_key)
if #token_data == 0 then
    return { "STATUS", "TOKEN_NOT_FOUND" }
end

local tok = {}
for i = 1, #token_data, 2 do
    tok[token_data[i]] = token_data[i+1]
end

-- Tenant isolation check (Fail-closed per AD-4)
if tok.tenant_id ~= nil and tok.tenant_id ~= "" and tok.tenant_id ~= tenant_id then
    return { "STATUS", "CROSS_TENANT_VIOLATION" }
end

local family_id = tok.family_id
if not family_id or family_id == "" then
    return { "STATUS", "CORRUPTED_TOKEN_DATA" }
end

-- Resolve family_key and family_tokens_key (support cluster hash tags)
local family_key = KEYS[3]
if not family_key or family_key == "" then
    family_key = "thrd:{" .. tenant_id .. "}:family:" .. family_id
end

local family_tokens_key = KEYS[4]
if not family_tokens_key or family_tokens_key == "" then
    family_tokens_key = family_key .. ":tokens"
end

-- 2. Fetch Family state
local family_data = redis.call('HGETALL', family_key)
local fam = {}
for i = 1, #family_data, 2 do
    fam[family_data[i]] = family_data[i+1]
end

local fam_state = fam.state or "ACTIVE"

-- Check if family is already compromised
if fam_state == "COMPROMISED" then
    return {
        "STATUS", "FAMILY_COMPROMISED",
        "FAMILY_ID", family_id,
        "USER_ID", tok.user_id or fam.user_id or "",
        "ALERT_CODE", "A07_TOKEN_REUSE"
    }
end

-- Check if family was explicitly revoked
if fam_state == "REVOKED" then
    return {
        "STATUS", "FAMILY_REVOKED",
        "FAMILY_ID", family_id,
        "USER_ID", tok.user_id or fam.user_id or ""
    }
end

-- 3. Check for Token Expiration
if tok.expires_at_unix then
    local exp_unix = tonumber(tok.expires_at_unix)
    if exp_unix and now_unix > exp_unix then
        return {
            "STATUS", "TOKEN_EXPIRED",
            "FAMILY_ID", family_id,
            "JTI", tok.jti or ""
        }
    end
end

-- 4. ACTIVE REUSE DETECTION (Breach detection)
-- If the presented token is already marked as USED, a replay attack is occurring.
if tok.used == "1" or tok.used == "true" or tok.state == "USED" then
    -- Security Alert A07_TOKEN_REUSE: Active Replay Attack!
    -- Invariant: Revoke entire family, blacklist/revoke all active tokens
    redis.call('HSET', family_key,
        'state', 'COMPROMISED',
        'compromised_at', now_str,
        'compromise_reason', 'A07_TOKEN_REUSE: used token presented for rotation (jti=' .. (tok.jti or "unknown") .. ')',
        'updated_at', now_str
    )
    redis.call('EXPIRE', family_key, ttl_seconds)

    -- Invalidate all active tokens belonging to this family
    local all_tokens = redis.call('SMEMBERS', family_tokens_key)
    for _, t_hash in ipairs(all_tokens) do
        local t_key = "thrd:{" .. tenant_id .. "}:token:" .. t_hash
        if redis.call('EXISTS', t_key) == 1 then
            redis.call('HSET', t_key, 'state', 'REVOKED', 'revoked_at', now_str)
        end
    end

    return {
        "STATUS", "TOKEN_REUSED",
        "FAMILY_ID", family_id,
        "TENANT_ID", tenant_id,
        "USER_ID", tok.user_id or fam.user_id or "",
        "REPLAYED_JTI", tok.jti or "",
        "ALERT_CODE", "A07_TOKEN_REUSE"
    }
end

-- 5. Check if presented token is in REVOKED state
if tok.state == "REVOKED" then
    return {
        "STATUS", "TOKEN_REVOKED",
        "FAMILY_ID", family_id,
        "JTI", tok.jti or ""
    }
end

-- 6. LEGITIMATE ROTATION
-- Token is ACTIVE and unused -> perform atomic exchange
local old_jti = tok.jti or ""
local user_id = tok.user_id or fam.user_id or ""
local current_gen = tonumber(fam.generation or "0")
local next_gen = current_gen + 1

-- Mark presented token as USED and retain until TTL to detect replay
redis.call('HSET', presented_token_key,
    'used', '1',
    'state', 'USED',
    'used_at', now_str
)
redis.call('EXPIRE', presented_token_key, ttl_seconds)

-- Store new token as ACTIVE
redis.call('HSET', new_token_key,
    'family_id', family_id,
    'tenant_id', tenant_id,
    'user_id', user_id,
    'jti', new_jti,
    'token_hash', new_token_hash,
    'state', 'ACTIVE',
    'used', '0',
    'issued_at', now_str,
    'expires_at', new_expires_at_str,
    'expires_at_unix', tostring(now_unix + ttl_seconds)
)
redis.call('EXPIRE', new_token_key, ttl_seconds)

-- Add new token hash to family's token set
redis.call('SADD', family_tokens_key, new_token_hash)
redis.call('EXPIRE', family_tokens_key, ttl_seconds)

-- Update Family metadata
redis.call('HSET', family_key,
    'state', 'ROTATED',
    'generation', tostring(next_gen),
    'current_jti', new_jti,
    'current_hash', new_token_hash,
    'updated_at', now_str
)
redis.call('EXPIRE', family_key, ttl_seconds)

return {
    "STATUS", "SUCCESS",
    "FAMILY_ID", family_id,
    "TENANT_ID", tenant_id,
    "USER_ID", user_id,
    "OLD_JTI", old_jti,
    "NEW_JTI", new_jti,
    "GENERATION", tostring(next_gen)
}
