-- Daily rewards, streaks and cooldown dispensers (spec 02).
--
--   GET  /v1/{game}/daily/status       streak, whether today is claimable, next reward, dispensers
--   POST /v1/{game}/daily/claim        claim today's reward (once per UTC day)
--   POST /v1/{game}/daily/dispenser    { id }   claim a cooldown dispenser (a free chest every 4 hours, ...)
--
-- Rewards are paid through economy/grant (and lives/grant) so they commit with the claim. Tune the ladder with
-- config "daily_config": { ladder = { {coins=50}, {coins=75}, ... }, miss_resets = true, dispensers = { ... } }.
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")
local reward = require("lib.reward")

local KEY = "daily:state"

local DEFAULTS = {
    ladder = { { coins = 50 }, { coins = 75 }, { coins = 100 }, { coins = 125, gems = 1 }, { coins = 150 }, { coins = 200 }, { coins = 250, gems = 5 } },
    miss_resets = true,
    dispensers = {
        free_chest = { cooldown_sec = 14400, reward = { coins = 25 } },
        gem_drip = { cooldown_sec = 86400, reward = { gems = 1 } },
    },
}

local function conf(req)
    local c = state.config("daily_config", DEFAULTS)
    c.ladder = plan.take(req, "daily", "ladder_days", c.ladder)
    return c
end

local function load()
    return state.load(KEY, { v = 0, last = -1, streak = 0, best = 0, disp = {} })
end

-- The streak the player would have if they claimed today.
local function next_streak(s, today, c)
    if s.last == today - 1 then
        return s.streak + 1
    elseif s.last == today then
        return s.streak
    elseif c.miss_resets then
        return 1
    end
    return s.streak + 1
end

local function ladder_reward(c, streak)
    local index = ((streak - 1) % #c.ladder) + 1
    return c.ladder[index], index
end

local function view(req, s, c, today, now)
    local claimable = s.last ~= today
    local streak = next_streak(s, today, c)
    local r, index = ladder_reward(c, streak)
    -- The plan caps how many dispensers answer; ids are taken in name order so the set is stable.
    local ids = {}
    for id in pairs(c.dispensers) do
        table.insert(ids, id)
    end
    table.sort(ids)
    local dispensers = {}
    for i = 1, math.min(#ids, plan.cap(req, "daily", "dispensers")) do
        local id = ids[i]
        local ready_at = (s.disp[id] or 0) + c.dispensers[id].cooldown_sec
        dispensers[id] = { ready = ready_at <= now, ready_in = math.max(0, ready_at - now) }
    end
    local current = s.streak
    if s.last < today - 1 and c.miss_resets then
        current = 0
    end
    return {
        claimable = claimable,
        streak = current,
        best = s.best,
        next_streak = streak,
        next_reward = r,
        ladder_index = index,
        next_claim_in = claimable and 0 or ((today + 1) * 86400 - now),
        dispensers = dispensers,
    }
end

local actions = {}

actions["GET:status"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local now = std.time.now()
    local s = load()
    return router.ok(view(req, s, conf(req), state.day(now), now), s.v)
end

actions["POST:claim"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local now = std.time.now()
    local today = state.day(now)
    local c = conf(req)
    local s = load()
    if s.last == today then
        return router.fail(409, "already_claimed", "today's reward was already claimed", { next_claim_in = (today + 1) * 86400 - now })
    end
    local streak = next_streak(s, today, c)
    local r = ladder_reward(c, streak)
    local failure, missing = reward.give(r)
    if failure then
        return failure
    end
    s.last = today
    s.streak = streak
    s.best = math.max(s.best, streak)
    progress.stat("daily_streak", streak, { mode = "set" })
    progress.stat("daily_claims", 1, { mode = "add" })
    local version = state.save(KEY, s)
    local out = view(req, s, c, today, now)
    out.reward = r
    out.missing = missing
    return router.ok(out, version)
end

actions["POST:dispenser"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", fields = { id = { type = "string", max_len = 32 } } })
    if bad then
        return bad
    end
    local now = std.time.now()
    local c = conf(req)
    local d = c.dispensers[body.id]
    local ids = {}
    for id in pairs(c.dispensers) do
        table.insert(ids, id)
    end
    table.sort(ids)
    local allowed = false
    for i = 1, math.min(#ids, plan.cap(req, "daily", "dispensers")) do
        allowed = allowed or ids[i] == body.id
    end
    if d == nil or not allowed then
        return router.fail(404, "unknown_dispenser", "no such dispenser")
    end
    local s = load()
    local ready_at = (s.disp[body.id] or 0) + d.cooldown_sec
    if ready_at > now then
        return router.fail(409, "cooling_down", "not ready yet", { ready_in = ready_at - now })
    end
    local failure = reward.give(d.reward)
    if failure then
        return failure
    end
    s.disp[body.id] = now
    local version = state.save(KEY, s)
    return router.ok({ reward = d.reward, ready_in = d.cooldown_sec }, version)
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
        local out = state.combine(a, b, { last = "max", streak = "max", best = "max" })
        -- Each dispenser keeps the later of the two last-claim times, so a merge never makes one ready sooner.
        out.disp = state.combine({ x = a.disp }, { x = b.disp }, { x = "maxmap" }).x
        return out
    end)
    return router.ok({ merged = merged })
end

return {
    auth = "user",
    methods = { "GET", "POST" },
    uses = { "progress" },
    data = { read = { "user" }, write = { "user" } },
    rate = { per_user = 30, window = 60 },
    description = "Daily rewards, streaks and cooldown dispensers",
    handler = router.dispatch(actions),
}
