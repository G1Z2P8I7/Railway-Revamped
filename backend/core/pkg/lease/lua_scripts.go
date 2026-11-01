package lease

const leaseSegmentLua = `-- Atomic Seat-Hold Lua Script
-- KEYS[1]: {train:date}:inv:class:quota (e.g. {12301:20261105}:inv:3A:GN)
-- KEYS[2]: {train:date}:active_leases
-- KEYS[3]: {train:date}:lease_expiry_zset
-- KEYS[4]: user:{user_id}:active_lease
-- ARGV[1]: Requested Berth Count (e.g. 2)
-- ARGV[2]: User Account ID
-- ARGV[3]: Lease TTL in seconds (e.g. 300)
-- ARGV[4]: Lease Token (UUIDv4)
-- ARGV[5]: Segment Bitmask (e.g. 3 for stops 0->2)

-- 1. Check user lease cap (Active_Leases(User) <= 1)
local user_active = redis.call('GET', KEYS[4])
if user_active then
    return {0, "ERR_USER_ACTIVE_LEASE_EXISTS", user_active}
end

-- 2. Check inventory availability
local current_available = redis.call('GET', KEYS[1])
if not current_available then
    return {0, "ERR_INVENTORY_KEY_NOT_FOUND", 0}
end

local available_num = tonumber(current_available)
local requested_num = tonumber(ARGV[1])

if available_num < requested_num then
    return {0, "INSUFFICIENT_INVENTORY", available_num}
end

-- 3. Decrement available counter
redis.call('DECRBY', KEYS[1], requested_num)

local time_resp = redis.call('TIME')
local now = tonumber(time_resp[1])
local ttl_seconds = tonumber(ARGV[3])
local expires_at = now + ttl_seconds

-- 4. Store full lease metadata in hash
local lease_payload = cjson.encode({
    lease_token = ARGV[4],
    user_id = ARGV[2],
    count = requested_num,
    inv_key = KEYS[1],
    seg_mask = ARGV[5],
    created_at = now,
    expires_at = expires_at
})
redis.call('HSET', KEYS[2], ARGV[4], lease_payload)

-- 5. Push to sorted set scored by absolute expiration timestamp
redis.call('ZADD', KEYS[3], expires_at, ARGV[4])

-- 6. Lock user active lease with matching TTL
redis.call('SET', KEYS[4], ARGV[4], 'EX', ttl_seconds)

return {1, ARGV[4], available_num - requested_num, expires_at}
`

const releaseLeaseLua = `-- Atomic Seat-Release Lua Script
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
`
