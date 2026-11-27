-- Weighted lucky-spin wheel (spec 02).
--
--   GET  /v1/{game}/wheel/status   free spin available?, slices, extra spin price
--   POST /v1/{game}/wheel/spin     one free spin per UTC day, extra spins are charged through the economy
--
-- The slice is rolled on the server with the secure RNG. Configure with "wheel_config":
-- { slices = { { weight = 40, reward = { coins = 50 } }, ... }, free_per_day = 1, extra_cost = { gems = 5 } }
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")
local reward = require("lib.reward")

local KEY = "wheel:state"

local DEFAULTS = {
    slices = {
        { weight = 30, reward = { coins = 25 } },
        { weight = 25, reward = { coins = 50 } },
        { weight = 15, reward = { coins = 100 } },
        { weight = 10, reward = { gems = 1 } },
        { weight = 8, reward = { lives = 1 } },
        { weight = 6, reward = { coins = 250 } },
        { weight = 4, reward = { gems = 3 } },
        { weight = 2, reward = { coins = 1000 } },
    },
    free_per_day = 1,
    extra_cost = { gems = 5 },
}

local function conf(req)
    local c = state.config("wheel_config", DEFAULTS)
    c.slices = plan.take(req, "wheel", "slices", c.slices)
    return c
end

local function load(today)
    local s = state.load(KEY, { v = 0, day = 0, used = 0, extra = 0 })
    if s.day ~= today then
        s.day = today
        s.used = 0
        s.extra = 0
    end
    return s
end

local function view(req, c, s)
    local free_left = math.max(0, c.free_per_day - s.used)
    return {
        free_spins = free_left,
        extra_cost = c.extra_cost,
        extra_left = math.max(0, plan.cap(req, "wheel", "extra_spins") - s.extra),
        slices = c.slices,
    }
end

local actions = {}

actions["GET:status"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local s = load(state.day())
    return router.ok(view(req, conf(req), s), s.v)
end

actions["POST:spin"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local c = conf(req)
    local s = load(state.day())
    local paid = false
    if s.used >= c.free_per_day then
        local cap = plan.cap(req, "wheel", "extra_spins")
        if s.extra >= cap then
            return router.plan_limit("extra_spins", cap, "no spins left today")
        end
        local failure = reward.charge(c.extra_cost)
        if failure then
            return failure
        end
        s.extra = s.extra + 1
        paid = true
    else
        s.used = s.used + 1
    end
    local total = 0
    for _, slice in ipairs(c.slices) do
        total = total + slice.weight
    end
    local roll = std.random.int(1, total)
    local index = #c.slices
    for i, slice in ipairs(c.slices) do
        roll = roll - slice.weight
        if roll <= 0 then
            index = i
            break
        end
    end
    local won = c.slices[index].reward
    local failure, missing = reward.give(won)
    if failure then
        return failure
    end
    progress.stat("wheel_spins", 1, { mode = "add" })
    local version = state.save(KEY, s)
    local out = view(req, c, s)
    out.slice = index
    out.reward = won
    out.paid = paid
    out.missing = missing
    return router.ok(out, version)
end

actions["CALL:merge"] = function(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local from, to, bad = guard.merge(req)
    if bad then
        return bad
    end
    local merged = state.merge_user(KEY, from, to, function(a, b)
        local out = state.combine(a, b, { day = "max", extra = "sum" })
        -- Spins used count for the day they were used on, so the newer day wins and a tie takes the larger.
        if (a.day or 0) > (b.day or 0) then
            out.used = a.used
        elseif (a.day or 0) == (b.day or 0) then
            out.used = math.max(a.used or 0, b.used or 0)
        end
        return out
    end)
    return router.ok({ merged = merged })
end

return {
    auth = "user",
    methods = { "GET", "POST" },
    uses = { "progress" },
    data = { read = { "user" }, write = { "user" } },
    rate = { per_user = 20, window = 60 },
    description = "Weighted lucky-spin wheel with a daily free spin",
    handler = router.dispatch(actions),
}
