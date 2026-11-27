-- Plan-aware presets. Free runs the whole core loop of every feature; paid plans raise capacity and depth.
-- req.plan is "free", "premium" (Grow) or "business" (Scale). Developers may override any number from
-- config (for example daily_config.ladder_days) as long as it stays inside the plan's cap.
local plan = {}

local PRESETS = {
    daily = { ladder_days = { free = 7, premium = 14, business = 30 }, dispensers = { free = 2, premium = 6, business = 12 } },
    wheel = { slices = { free = 8, premium = 12, business = 16 }, extra_spins = { free = 1, premium = 3, business = 5 } },
    chests = { tracks = { free = 1, premium = 3, business = 8 }, milestones = { free = 5, premium = 10, business = 20 } },
    quests = { daily_slots = { free = 3, premium = 4, business = 6 }, weekly_slots = { free = 2, premium = 3, business = 5 } },
    leaderboards = { boards = { free = 2, premium = 20, business = 100 }, details_bytes = { free = 128, premium = 512, business = 1024 } },
    competitions = { concurrent = { free = 1, premium = 3, business = 10 }, bracket_size = { free = 8, premium = 32, business = 128 } },
    economy = { currencies = { free = 2, premium = 6, business = 12 }, catalog_items = { free = 25, premium = 100, business = 250 } },
    lives = { gifts_per_day = { free = 5, premium = 10, business = 20 }, max_lives = { free = 5, premium = 10, business = 20 } },
    meta = { bounties = { free = 20, premium = 50, business = 150 }, ranks = { free = 10, premium = 25, business = 50 } },
}

-- plan.cap(req, "daily", "ladder_days") -> the plan's cap for that number.
function plan.cap(req, feature, name)
    local row = PRESETS[feature] and PRESETS[feature][name]
    if row == nil then
        return 0
    end
    return row[req.plan] or row.free
end

-- plan.clamp(req, "wheel", "slices", configured) -> the configured number held to the plan's cap.
function plan.clamp(req, feature, name, configured)
    local cap = plan.cap(req, feature, name)
    if type(configured) ~= "number" then
        return cap
    end
    return math.min(configured, cap)
end

-- Keep at most the plan's cap of entries from a list.
function plan.take(req, feature, name, list)
    local cap = plan.cap(req, feature, name)
    if #list <= cap then
        return list
    end
    local out = {}
    for i = 1, cap do
        out[i] = list[i]
    end
    return out
end

return plan
