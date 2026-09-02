-- Long usage window: sum every bucket in the window, admit or refuse, and
-- report when the window actually opens again.
--
-- Returns {allowed, remaining, retry_after_ms, full_reset_ms, used}.
--
-- retry_after_ms and full_reset_ms are different questions and issue #1725 was
-- filed partly because the old script answered neither. It returned
-- "milliseconds until the current bucket rolls", which for the weekly window
-- was up to a full day out with the allowance still spent, and the refusal
-- carried no timestamp at all.
--
--   retry_after_ms: walk the buckets oldest first until enough score has aged
--   out for THIS request to fit. That is when the caller may retry.
--
--   full_reset_ms: when the window is empty again, which is when the current
--   bucket ages out of the last slot. That is what a consumption bar counts
--   down to. For the anchored weekly window (one bucket, seven days wide) it
--   is exactly the end of the account's week.
--
-- anchor_ms offsets the bucket grid. Zero for the sliding session window, whose
-- boundaries are never customer visible; the account's own anchor for the
-- weekly window, so its reset lands on the instant the customer was told.

local current_key = KEYS[1]

local key_prefix = ARGV[1]
local current_bucket = tonumber(ARGV[2])
local bucket_ms = tonumber(ARGV[3])
local bucket_count = tonumber(ARGV[4])
local limit = tonumber(ARGV[5])
local score = tonumber(ARGV[6])
local now_ms = tonumber(ARGV[7])
local anchor_ms = tonumber(ARGV[8])

-- Bucket i is i buckets older than the current one.
local buckets = {}
local total = 0
for i = 0, bucket_count - 1 do
  local value = tonumber(redis.call("GET", key_prefix .. ":" .. tostring(current_bucket - i)) or "0")
  buckets[i] = value
  total = total + value
end

-- A bucket leaves the window bucket_count slots after it starts.
local function ages_out_ms(age)
  return anchor_ms + (current_bucket - age + bucket_count) * bucket_ms - now_ms
end

local full_reset_ms = ages_out_ms(0)
local projected = total + score

if projected > limit then
  local remaining = limit - total
  if remaining < 0 then
    remaining = 0
  end

  -- How much has to age out before this request fits.
  local needed = projected - limit
  local freed = 0
  -- Default: a single request larger than the whole limit never fits by
  -- waiting, so report the full drain rather than a retry that will fail.
  local retry_after_ms = full_reset_ms
  for age = bucket_count - 1, 0, -1 do
    freed = freed + buckets[age]
    if freed >= needed then
      retry_after_ms = ages_out_ms(age)
      break
    end
  end

  return {0, remaining, retry_after_ms, full_reset_ms, total}
end

redis.call("INCRBY", current_key, score)
redis.call("PEXPIRE", current_key, bucket_ms * (bucket_count + 1))

local remaining = limit - projected
if remaining < 0 then
  remaining = 0
end

return {1, remaining, 0, full_reset_ms, projected}
