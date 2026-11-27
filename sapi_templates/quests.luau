-- Deterministic daily and weekly quests (spec 02).
--
--   GET  /v1/{game}/quests/status     today's and this week's quests with progress
--   POST /v1/{game}/quests/progress   { event, amount? }  report a game event (game servers only, naming the player)
--   POST /v1/{game}/quests/claim      { id }              collect a finished quest
--   CALL quests/event { event, amount }                   (api.call only, uncapped)
--
-- The rotation needs no cron job and no global writes: the day (or week) number seeds a shuffle of the pool in
-- config "quests_pool", so every player sees the same quests and the set changes at 00:00 UTC.
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")
local reward = require("lib.reward")

local KEY = "quests:state"

local POOL = {
    { id = "play_3", kind = "daily", event = "run_finished", target = 3, reward = { coins = 50 } },
    { id = "win_1", kind = "daily", event = "run_won", target = 1, reward = { coins = 75 } },
    { id = "score_5000", kind = "daily", event = "score", target = 5000, reward = { coins = 100 } },
    { id = "collect_20", kind = "daily", event = "coin_collected", target = 20, reward = { coins = 60 } },
    { id = "daily_login", kind = "daily", event = "login", target = 1, reward = { coins = 25 } },
    { id = "play_20", kind = "weekly", event = "run_finished", target = 20, reward = { gems = 2 } },
    { id = "win_10", kind = "weekly", event = "run_won", target = 10, reward = { gems = 3 } },
    { id = "collect_300", kind = "weekly", event = "coin_collected", target = 300, reward = { coins = 500 } },
    { id = "streak_5", kind = "weekly", event = "login", target = 5, reward = { gems = 5 } },
}

local function pool()
    local configured = config.get("quests_pool", nil)
    if type(configured) == "table" and #configured > 0 then
        return configured
    end
    return POOL
end

local function pick(list, kind, count, seed)
    local candidates = {}
    for _, q in ipairs(list) do
        if q.kind == kind then
            table.insert(candidates, q)
        end
    end
    local rng = std.random.new(seed)
    local shuffled = rng:shuffle(candidates)
    local out = {}
    for i = 1, math.min(count, #shuffled) do
        out[i] = shuffled[i]
    end
    return out
end

local function week_of(day)
    return math.floor((day + 3) / 7) -- weeks start on Monday (1970-01-01 was a Thursday)
end

local function active(req, now)
    local day = state.day(now)
    local week = week_of(day)
    local list = pool()
    return {
        daily = pick(list, "daily", plan.cap(req, "quests", "daily_slots"), day * 2 + 1),
        weekly = pick(list, "weekly", plan.cap(req, "quests", "weekly_slots"), week * 2 + 7777),
    }, day, week
end

local function load(day, week)
    local s = state.load(KEY, { v = 0, d = 0, w = 0, dp = {}, dc = {}, wp = {}, wc = {} })
    if s.d ~= day then
        s.d, s.dp, s.dc = day, {}, {}
    end
    if s.w ~= week then
        s.w, s.wp, s.wc = week, {}, {}
    end
    return s
end

local function rows(list, progress_map, claimed_map)
    local out = {}
    for i, q in ipairs(list) do
        local p = math.min(progress_map[q.id] or 0, q.target)
        out[i] = { id = q.id, event = q.event, target = q.target, progress = p, done = p >= q.target, claimed = claimed_map[q.id] == 1, reward = q.reward }
    end
    return out
end

local function view(quests, s)
    return { daily = rows(quests.daily, s.dp, s.dc), weekly = rows(quests.weekly, s.wp, s.wc) }
end

local function apply(quests, s, event, amount)
    local touched = 0
    for _, q in ipairs(quests.daily) do
        if q.event == event then
            s.dp[q.id] = (s.dp[q.id] or 0) + amount
            touched = touched + 1
        end
    end
    for _, q in ipairs(quests.weekly) do
        if q.event == event then
            s.wp[q.id] = (s.wp[q.id] or 0) + amount
            touched = touched + 1
        end
    end
    return touched
end

local actions = {}

actions["GET:status"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local quests, day, week = active(req, std.time.now())
    local s = load(day, week)
    return router.ok(view(quests, s), s.v)
end

actions["POST:progress"] = function(req)
    -- A player cannot vouch for their own events: repeated reports would finish any quest without playing.
    local denied = guard.server(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, {
        type = "object",
        fields = { event = { type = "string", max_len = 32 }, amount = { type = "integer", min = 1, max = 10000, optional = true } },
    })
    if bad then
        return bad
    end
    if req.user == nil then
        return router.bad_request("a game server must name the player (X-Sapi-User)")
    end
    local quests, day, week = active(req, std.time.now())
    local s = load(day, week)
    apply(quests, s, body.event, body.amount or 1)
    local version = state.save(KEY, s)
    return router.ok(view(quests, s), version)
end

actions["CALL:event"] = function(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local body = req.body or {}
    local quests, day, week = active(req, std.time.now())
    local s = load(day, week)
    apply(quests, s, tostring(body.event), math.floor(body.amount or 1))
    local version = state.save(KEY, s)
    return router.ok(view(quests, s), version)
end

actions["POST:claim"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", fields = { id = { type = "string", max_len = 48 } } })
    if bad then
        return bad
    end
    local quests, day, week = active(req, std.time.now())
    local s = load(day, week)
    for _, bucket in ipairs({ { quests.daily, s.dp, s.dc }, { quests.weekly, s.wp, s.wc } }) do
        for _, q in ipairs(bucket[1]) do
            if q.id == body.id then
                if (bucket[2][q.id] or 0) < q.target then
                    return router.fail(409, "not_done", "this quest is not finished")
                end
                if bucket[3][q.id] == 1 then
                    return router.fail(409, "already_claimed", "this quest was already claimed")
                end
                local failure, missing = reward.give(q.reward)
                if failure then
                    return failure
                end
                bucket[3][q.id] = 1
                progress.stat("quests_done", 1, { mode = "add" })
                local version = state.save(KEY, s)
                local out = view(quests, s)
                out.reward = q.reward
                out.missing = missing
                return router.ok(out, version)
            end
        end
    end
    return router.fail(404, "unknown_quest", "that quest is not active today")
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
        local out = state.combine(a, b, { d = "max", w = "max" })
        -- Progress belongs to a day or a week. The newer period wins, and the same period adds progress up.
        local function period(field, progress, claimed)
            if (a[field] or 0) > (b[field] or 0) then
                out[progress], out[claimed] = a[progress], a[claimed]
            elseif (a[field] or 0) == (b[field] or 0) then
                out[progress] = state.combine({ x = a[progress] }, { x = b[progress] }, { x = "maxmap" }).x
                out[claimed] = state.combine({ x = a[claimed] }, { x = b[claimed] }, { x = "union" }).x
            end
        end
        period("d", "dp", "dc")
        period("w", "wp", "wc")
        return out
    end)
    return router.ok({ merged = merged })
end

return {
    auth = "user",
    methods = { "GET", "POST" },
    uses = { "progress" },
    data = { read = { "user" }, write = { "user" } },
    rate = { per_user = 60, window = 60 },
    description = "Deterministic daily and weekly quests",
    handler = router.dispatch(actions),
}
