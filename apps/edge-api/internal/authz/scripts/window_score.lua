local current_key = KEYS[1]

local key_prefix = ARGV[1]
local current_bucket = tonumber(ARGV[2])
local bucket_ms = tonumber(ARGV[3])
local bucket_count = tonumber(ARGV[4])
local limit = tonumber(ARGV[5])
local score = tonumber(ARGV[6])
local now_ms = tonumber(ARGV[7])

local total = 0
for i = 0, bucket_count - 1 do
  local bucket_key = key_prefix .. ":" .. tostring(current_bucket - i)
  total = total + tonumber(redis.call("GET", bucket_key) or "0")
end

local projected = total + score
local reset_ms = bucket_ms - (now_ms % bucket_ms)
if projected > limit then
  local remaining = limit - total
  if remaining < 0 then
    remaining = 0
  end
  return {0, remaining, reset_ms}
end

redis.call("INCRBY", current_key, score)
redis.call("PEXPIRE", current_key, bucket_ms * (bucket_count + 1))

local remaining = limit - projected
if remaining < 0 then
  remaining = 0
end

return {1, remaining, reset_ms}
