-- One compact JSON state object per feature per player: data.user["<prefix>:state"].
-- Installing every feature therefore costs one user key each and one or two data ops per request.
local state = {}

local function merge(defaults, stored)
    local out = {}
    for k, v in pairs(defaults) do
        out[k] = v
    end
    if type(stored) == "table" then
        for k, v in pairs(stored) do
            out[k] = v
        end
    end
    return out
end

-- Load the player's state for `key`, filling in defaults for anything missing.
function state.load(key, defaults)
    return merge(defaults or {}, data.user:get(key))
end

-- Save the state and bump its version. Returns the new version.
function state.save(key, value)
    value.v = (value.v or 0) + 1
    data.user:set(key, value)
    return value.v
end

-- The UTC day number for a timestamp (days since 1970-01-01).
function state.day(ts)
    return math.floor((ts or std.time.now()) / 86400)
end

-- The UTC week number (weeks start on Monday; 1970-01-01 was a Thursday).
function state.week(ts)
    return math.floor((state.day(ts) + 3) / 7)
end

-- Read a table from remote config (config.get) with built-in defaults for missing keys.
function state.config(name, defaults)
    return merge(defaults, config.get(name, {}))
end

-- ---------------------------------------------------------------- merging two players
-- A guest who signs in keeps what they earned. Each feature says how its fields combine; a field it does not name
-- keeps the value of the account that survives.
local combiners = {}

function combiners.sum(a, b)
    return (a or 0) + (b or 0)
end

function combiners.max(a, b)
    return math.max(a or 0, b or 0)
end

function combiners.keep(a, b)
    if b ~= nil then
        return b
    end
    return a
end

-- Two sets, as maps of key -> true or as lists. The result is a map when either side is a map, else a list without repeats.
function combiners.union(a, b)
    a, b = a or {}, b or {}
    if a[1] ~= nil or b[1] ~= nil then
        local seen, out = {}, {}
        for _, list in ipairs({ a, b }) do
            for _, v in ipairs(list) do
                if not seen[v] then
                    seen[v] = true
                    table.insert(out, v)
                end
            end
        end
        return out
    end
    local out = {}
    for k, v in pairs(a) do
        out[k] = v
    end
    for k, v in pairs(b) do
        out[k] = v
    end
    return out
end

local function per_key(a, b, op)
    local out = {}
    for k, v in pairs(a or {}) do
        out[k] = v
    end
    for k, v in pairs(b or {}) do
        out[k] = out[k] ~= nil and op(out[k], v) or v
    end
    return out
end

function combiners.summap(a, b)
    return per_key(a, b, function(x, y)
        return (x or 0) + (y or 0)
    end)
end

function combiners.maxmap(a, b)
    return per_key(a, b, function(x, y)
        return math.max(x or 0, y or 0)
    end)
end

-- `from` and `to` are state tables; `spec` maps a field name to "sum", "max", "keep", "union", "summap" or "maxmap".
-- The result starts as a copy of `to`, so anything the spec does not name stays as the surviving account had it.
function state.combine(from, to, spec)
    local out = {}
    for k, v in pairs(to or {}) do
        out[k] = v
    end
    for field, how in pairs(spec) do
        out[field] = combiners[how](from[field], to and to[field])
    end
    return out
end

-- Moves the player's state under `key` from `from` into `to`. `combine(from_state, to_state)` returns the merged table.
-- Returns true when `from` had anything to move. `from` is left empty, so a repeat of the call changes nothing.
function state.merge_user(key, from, to, combine)
    local src = data.user(from):get(key)
    if src == nil then
        return false
    end
    local dst = data.user(to):get(key)
    local merged = combine(src, dst or {})
    merged.v = math.max(src.v or 0, dst and dst.v or 0) + 1
    data.user(to):set(key, merged)
    data.user(from):delete(key)
    return true
end

return state