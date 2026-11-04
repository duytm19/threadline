-- revoke_family.lua
-- Atomic revocation or compromise marking of a TokenFamily and its active tokens
--
-- KEYS[1] = family_key         (thrd:{tenant}:family:{family_id})
-- KEYS[2] = family_tokens_key  (thrd:{tenant}:family:{family_id}:tokens)
--
-- ARGV[1] = tenant_id          (string)
-- ARGV[2] = target_state       ("REVOKED" or "COMPROMISED")
-- ARGV[3] = reason             (string)
-- ARGV[4] = now_str            (RFC3339 string)
-- ARGV[5] = ttl_seconds        (integer seconds)

local family_key        = KEYS[1]
local family_tokens_key = KEYS[2]

local tenant_id    = ARGV[1]
local target_state = ARGV[2]
local reason       = ARGV[3]
local now_str      = ARGV[4]
local ttl_seconds  = tonumber(ARGV[5])

if redis.call('EXISTS', family_key) == 0 then
    return { "STATUS", "FAMILY_NOT_FOUND" }
end

local family_tenant = redis.call('HGET', family_key, 'tenant_id')
if family_tenant ~= nil and family_tenant ~= "" and family_tenant ~= tenant_id then
    return { "STATUS", "CROSS_TENANT_VIOLATION" }
end

local current_state = redis.call('HGET', family_key, 'state')
if current_state == "COMPROMISED" and target_state == "REVOKED" then
    return { "STATUS", "ALREADY_COMPROMISED" }
end

if target_state == "COMPROMISED" then
    redis.call('HSET', family_key,
        'state', 'COMPROMISED',
        'compromised_at', now_str,
        'compromise_reason', reason,
        'updated_at', now_str
    )
else
    redis.call('HSET', family_key,
        'state', 'REVOKED',
        'updated_at', now_str
    )
end
redis.call('EXPIRE', family_key, ttl_seconds)

-- Invalidate all tokens associated with this family
local all_tokens = redis.call('SMEMBERS', family_tokens_key)
for _, t_hash in ipairs(all_tokens) do
    local t_key = "thrd:{" .. tenant_id .. "}:token:" .. t_hash
    if redis.call('EXISTS', t_key) == 1 then
        redis.call('HSET', t_key, 'state', 'REVOKED', 'revoked_at', now_str)
    end
end
redis.call('EXPIRE', family_tokens_key, ttl_seconds)

return { "STATUS", "SUCCESS" }
