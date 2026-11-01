package infra

import "github.com/redis/go-redis/v9"

// All state keys and dynamically addressed per-scope keys share one Redis hash
// tag. TIME is the only clock used for distributed windows and leases. A failed
// eligibility check never changes request/token counters. Waiters stay local;
// Redis stores only supplier settings, counters and admitted lease identities.
var capacityScript = redis.NewScript(`
local op = ARGV[1]
local id = ARGV[2]
local lease = tonumber(ARGV[3])
local prefix = ARGV[4]
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)

local function release(member)
  local raw = redis.call('HGET', KEYS[2], member)
  if raw then
    local active = cjson.decode(raw)
    for _, scope in ipairs({active.p, active.m}) do
      if redis.call('HINCRBY', KEYS[3], scope, -1) <= 0 then
        redis.call('HDEL', KEYS[3], scope)
      end
    end
  end
  redis.call('ZREM', KEYS[1], member)
  redis.call('HDEL', KEYS[2], member)
end

if op == 'release' or op == 'abandon' then
  redis.call('SET', prefix .. ':canceled:' .. id, 1, 'PX', math.max(120000, 4 * lease))
  if op == 'abandon' then
    local raw = redis.call('HGET', KEYS[2], id)
    if raw then
      local active = cjson.decode(raw)
      for _, scope in ipairs({active.p, active.m}) do
        local key = prefix .. ':rate:' .. scope
        if tonumber(redis.call('HGET', key, 'minute')) == active.minute then
          redis.call('HINCRBY', key, 'rpm', -1)
        end
        if tonumber(redis.call('HGET', key, 'second')) == active.second then
          redis.call('HINCRBY', key, 'rps', -1)
          redis.call('HINCRBY', key, 'tps', -active.t)
        end
      end
    end
  end
  release(id)
  return 1
end
if redis.call('EXISTS', prefix .. ':canceled:' .. id) == 1 then return -2 end

-- An unrenewed owner must cancel locally by its lease deadline. Reclamation
-- waits one additional full lease. Explicit Release follows upstream closure.
for _, member in ipairs(redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', now)) do
  release(member)
end

if op == 'renew' then
  local raw = redis.call('HGET', KEYS[2], id)
  if not raw then return 0 end
  local active = cjson.decode(raw)
  if active.e <= now then return 0 end
  active.e = now + lease
  redis.call('HSET', KEYS[2], id, cjson.encode(active))
  redis.call('ZADD', KEYS[1], now + 2 * lease, id)
  return 1
end

local function updateConfig(scope, revision, limits)
  local key = prefix .. ':config:' .. scope
  local raw = redis.call('GET', key)
  if not raw or revision >= cjson.decode(raw).v then
    -- Never let an old registry snapshot replace a newer revision after idle.
    redis.call('SET', key, cjson.encode({v=revision, l=limits}))
  end
end
local function limits(scope)
  return cjson.decode(redis.call('GET', prefix .. ':config:' .. scope)).l
end

-- Repeating one identity acknowledges it without charging quota twice.
local admitted = redis.call('HGET', KEYS[2], id)
if admitted then
  if cjson.decode(admitted).e <= now then return -2 end
  return 1
end
local request = cjson.decode(ARGV[5])
updateConfig(request.p, request.pv, request.pl)
updateConfig(request.m, request.mv, request.ml)
local pool, model = limits(request.p), limits(request.m)
local minute = math.floor(now / 60000)
local second = math.floor(now / 1000)

local function counters(scope)
  local key = prefix .. ':rate:' .. scope
  local values = redis.call('HMGET', key, 'minute', 'rpm', 'second', 'rps', 'tps')
  local rpm, rps, tps = 0, 0, 0
  if tonumber(values[1]) == minute then rpm = tonumber(values[2]) or 0 end
  if tonumber(values[3]) == second then
    rps = tonumber(values[4]) or 0
    tps = tonumber(values[5]) or 0
  end
  return rpm, rps, tps
end
for _, scope in ipairs({{request.p, pool}, {request.m, model}}) do
  local config = scope[2]
  if config[3] > 0 and (request.t <= 0 or request.t > config[3]) then return -4 end
end
for _, scope in ipairs({{request.p, pool}, {request.m, model}}) do
  local config = scope[2]
  if config[4] > 0 and tonumber(redis.call('HGET', KEYS[3], scope[1]) or '0') >= config[4] then return 0 end
  local rpm, rps, tps = counters(scope[1])
  if config[1] > 0 and rpm + 1 > config[1] then return 0 end
  if config[2] > 0 and rps + 1 > config[2] then return 0 end
  if config[3] > 0 and tps + request.t > config[3] then return 0 end
end

-- Commit all supplier dimensions only after every dimension has passed.
for _, scope in ipairs({request.p, request.m}) do
  local rpm, rps, tps = counters(scope)
  local key = prefix .. ':rate:' .. scope
  redis.call('HSET', key, 'minute', minute, 'rpm', rpm + 1,
    'second', second, 'rps', rps + 1, 'tps', tps + request.t)
  redis.call('PEXPIRE', key, 120000)
  redis.call('HINCRBY', KEYS[3], scope, 1)
end
request.minute = minute
request.second = second
request.e = now + lease
redis.call('HSET', KEYS[2], id, cjson.encode(request))
redis.call('ZADD', KEYS[1], now + 2 * lease, id)
return 1
`)
