-- create_family.lua
-- Atomic initialization of a new TokenFamily and its initial active RefreshToken
--
-- KEYS[1] = family_key         (thrd:{tenant}:family:{family_id})
-- KEYS[2] = family_tokens_key  (thrd:{tenant}:family:{family_id}:tokens)
-- KEYS[3] = token_key          (thrd:{tenant}:token:{token_hash})
--
-- ARGV[1] = tenant_id          (string)
-- ARGV[2] = family_id          (string)
-- ARGV[3] = user_id            (string)
-- ARGV[4] = initial_jti        (string)
-- ARGV[5] = token_hash         (string)
-- ARGV[6] = now_str            (RFC3339 string)
-- ARGV[7] = now_unix           (integer unix seconds)
-- ARGV[8] = expires_at_str     (RFC3339 string)
-- ARGV[9] = ttl_seconds        (integer seconds)

local family_key        = KEYS[1]
local family_tokens_key = KEYS[2]
local token_key         = KEYS[3]

local tenant_id      = ARGV[1]
local family_id      = ARGV[2]
local user_id        = ARGV[3]
local initial_jti    = ARGV[4]
local token_hash     = ARGV[5]
local now_str        = ARGV[6]
local now_unix       = tonumber(ARGV[7])
local expires_at_str = ARGV[8]
local ttl_seconds    = tonumber(ARGV[9])

-- Fail if family already exists
if redis.call('EXISTS', family_key) == 1 then
    return { "STATUS", "FAMILY_ALREADY_EXISTS" }
end

-- Fail if token already exists
if redis.call('EXISTS', token_key) == 1 then
    return { "STATUS", "TOKEN_ALREADY_EXISTS" }
end

-- 1. Initialize Family metadata
redis.call('HSET', family_key,
    'family_id', family_id,
    'tenant_id', tenant_id,
    'user_id', user_id,
    'state', 'ACTIVE',
    'generation', '0',
    'current_jti', initial_jti,
    'current_hash', token_hash,
    'created_at', now_str,
    'updated_at', now_str
)
redis.call('EXPIRE', family_key, ttl_seconds)

-- 2. Store initial Token as ACTIVE
redis.call('HSET', token_key,
    'family_id', family_id,
    'tenant_id', tenant_id,
    'user_id', user_id,
    'jti', initial_jti,
    'token_hash', token_hash,
    'state', 'ACTIVE',
    'used', '0',
    'issued_at', now_str,
    'expires_at', expires_at_str,
    'expires_at_unix', tostring(now_unix + ttl_seconds)
)
redis.call('EXPIRE', token_key, ttl_seconds)

-- 3. Register token in family's tokens set
redis.call('SADD', family_tokens_key, token_hash)
redis.call('EXPIRE', family_tokens_key, ttl_seconds)

return { "STATUS", "SUCCESS" }
