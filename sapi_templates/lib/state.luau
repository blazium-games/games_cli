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

return state
