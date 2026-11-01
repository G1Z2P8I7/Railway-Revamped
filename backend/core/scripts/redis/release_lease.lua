-- Atomic Seat-Release Lua Script
-- KEYS[1]: {train:date}:active_leases
-- KEYS[2]: {train:date}:lease_expiry_zset
-- KEYS[3]: user:{user_id}:active_lease
-- ARGV[1]: Lease Token (UUIDv4)
-- ARGV[2]: Expected User ID (optional, "" to bypass check)

local lease_raw = redis.call('HGET', KEYS[1], ARGV[1])
if not lease_raw then
    return {0, "ERR_LEASE_NOT_FOUND"}
end

local lease = cjson.decode(lease_raw)

-- Check user match if supplied
if ARGV[2] ~= "" and lease.user_id ~= ARGV[2] then
    return {0, "ERR_USER_MISMATCH"}
end

-- 1. Restore inventory counter
redis.call('INCRBY', lease.inv_key, lease.count)

-- 2. Remove from active leases hash
redis.call('HDEL', KEYS[1], ARGV[1])

-- 3. Remove from expiry ZSET
redis.call('ZREM', KEYS[2], ARGV[1])

-- 4. Clean user lock if it matches this token
local current_user_lock = redis.call('GET', KEYS[3])
if current_user_lock == ARGV[1] then
    redis.call('DEL', KEYS[3])
end

return {1, "LEASE_RELEASED", lease.count}
