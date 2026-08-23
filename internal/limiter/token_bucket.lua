-- Gatekeeper's atomic Redis token-bucket transition.
--
-- KEYS[1]  Bucket key
-- ARGV[1]  Capacity
-- ARGV[2]  Refill tokens
-- ARGV[3]  Refill interval in microseconds

local error_prefix = "gatekeeper token bucket: "

if #KEYS ~= 1 or KEYS[1] == "" then
    return redis.error_reply(error_prefix .. "exactly one non-empty key is required")
end

if #ARGV ~= 3 then
    return redis.error_reply(error_prefix .. "exactly three arguments are required")
end

local capacity = tonumber(ARGV[1])
local refill_tokens = tonumber(ARGV[2])
local interval_us = tonumber(ARGV[3])

if capacity == nil or capacity <= 0 or capacity ~= math.floor(capacity) then
    return redis.error_reply(error_prefix .. "capacity must be a positive integer")
end

if refill_tokens == nil or refill_tokens <= 0 or refill_tokens ~= math.floor(refill_tokens) then
    return redis.error_reply(error_prefix .. "refill tokens must be a positive integer")
end

if interval_us == nil or interval_us <= 0 or interval_us ~= math.floor(interval_us) then
    return redis.error_reply(error_prefix .. "refill interval must be a positive integer")
end

local redis_time = redis.call("TIME")
local now_us = tonumber(redis_time[1]) * 1000000 + tonumber(redis_time[2])

local state = redis.call("HMGET", KEYS[1], "tokens", "last_refill_us")
local stored_tokens = state[1]
local stored_last_refill = state[2]

local available
local last_refill_us

if stored_tokens == false and stored_last_refill == false then
    available = capacity
    last_refill_us = now_us
elseif stored_tokens == false or stored_last_refill == false then
    return redis.error_reply(error_prefix .. "stored state is incomplete")
else
    available = tonumber(stored_tokens)
    last_refill_us = tonumber(stored_last_refill)

    if available == nil or last_refill_us == nil then
        return redis.error_reply(error_prefix .. "stored state is nonnumeric")
    end

    if available < 0 or last_refill_us < 0 then
        return redis.error_reply(error_prefix .. "stored state is negative")
    end

    local elapsed_us = math.max(0, now_us - last_refill_us)
    local refilled = elapsed_us * refill_tokens / interval_us
    available = math.min(capacity, available + refilled)
end

local allowed = 0
local retry_after_ms = 0

if available >= 1 then
    allowed = 1
    available = available - 1
else
    local missing_tokens = 1 - available
    local retry_us = math.ceil(missing_tokens * interval_us / refill_tokens)
    retry_after_ms = math.max(1, math.ceil(retry_us / 1000))
end

local remaining = math.floor(available)
local ttl_ms = math.max(1, math.ceil(capacity * interval_us / refill_tokens / 1000))

redis.call(
    "HSET",
    KEYS[1],
    "tokens",
    string.format("%.17g", available),
    "last_refill_us",
    string.format("%.0f", now_us)
)
redis.call("PEXPIRE", KEYS[1], ttl_ms)

return {allowed, remaining, retry_after_ms}

