local current_key = KEYS[1]
local previous_key = KEYS[2]

local current_start_ms = tonumber(ARGV[1])
local previous_start_ms = tonumber(ARGV[2])
local now_ms = tonumber(ARGV[3])
local limit = tonumber(ARGV[4])
local amount = tonumber(ARGV[5])

local window_ms = 60000
local reset_ms = (current_start_ms + window_ms) - now_ms

local function read_bucket(key, expected_start)
  local start = tonumber(redis.call("HGET", key, "start") or "-1")
  local count = tonumber(redis.call("HGET", key, "count") or "0")

  if start == expected_start then
    return count
  end

  if start ~= -1 and start == previous_start_ms and key == current_key then
    redis.call("HSET", previous_key, "start", start, "count", count)
    redis.call("PEXPIRE", previous_key, window_ms * 2)
  elseif key == previous_key then
    redis.call("DEL", previous_key)
  end

  if key == current_key then
    redis.call("DEL", current_key)
  end

  return 0
end

local current_count = read_bucket(current_key, current_start_ms)
local previous_count = read_bucket(previous_key, previous_start_ms)

local elapsed_ms = now_ms - current_start_ms
if elapsed_ms < 0 then
  elapsed_ms = 0
end

local carry = 0
if elapsed_ms < window_ms then
  carry = math.floor(previous_count * (window_ms - elapsed_ms) / window_ms)
end

local projected = current_count + carry + amount
if projected > limit then
  local remaining = limit - (current_count + carry)
  if remaining < 0 then
    remaining = 0
  end
  return {0, remaining, reset_ms}
end

redis.call("HSET", current_key, "start", current_start_ms, "count", current_count + amount)
redis.call("PEXPIRE", current_key, window_ms * 2)

local remaining = limit - projected
if remaining < 0 then
  remaining = 0
end

return {1, remaining, reset_ms}
