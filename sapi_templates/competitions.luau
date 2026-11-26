-- Asynchronous brackets and weekly seasons (spec 03).
--
--   GET  /v1/{game}/competitions/status        season, my bracket standings, time left
--   POST /v1/{game}/competitions/join          enter this week's season (placed in an open bracket)
--   POST /v1/{game}/competitions/report_match  { match_id, result = "win" | "draw" | "loss" }
--   POST /v1/{game}/competitions/claim_season  { season }  collect the reward for an ended season
--
-- Players are placed in brackets of bracket_size (Free 8, Grow 32, Scale 128). Standings are points inside the
-- bracket, so there is no global write hot spot. Configure "competitions_config":
-- { win = 3, draw = 1, max_matches_per_day = 20, rewards = { { rank = 1, reward = { gems = 10 } }, ... } }
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")
local reward = require("lib.reward")

local KEY = "comp:state"

local DEFAULTS = {
    win = 3,
    draw = 1,
    max_matches_per_day = 20,
    rewards = {
        { rank = 1, reward = { gems = 10 } },
        { rank = 3, reward = { gems = 5 } },
        { rank = 8, reward = { coins = 500 } },
    },
}

local function conf()
    return state.config("competitions_config", DEFAULTS)
end

local function load()
    return state.load(KEY, { v = 0, season = 0, g = 0, pts = 0, played = 0, day = 0, today = 0, seen = {}, claimed = {} })
end

local function standings_key(season, g)
    return "comp:" .. season .. ":g" .. g
end

local function ranked(table_of_points)
    local rows = {}
    for uid, pts in pairs(table_of_points) do
        table.insert(rows, { user = uid, points = pts })
    end
    table.sort(rows, function(a, b)
        if a.points ~= b.points then
            return a.points > b.points
        end
        return a.user < b.user
    end)
    for i, row in ipairs(rows) do
        row.rank = i
    end
    return rows
end

local function view(s, season, now)
    local out = { season = season, joined = s.season == season, ends_in = (state.week(now) + 1) * 7 * 86400 - 3 * 86400 - now }
    if s.season == season and s.g > 0 then
        local group = data.channel:get(standings_key(season, s.g)) or {}
        out.bracket = s.g
        out.points = s.pts
        out.standings = ranked(group)
    end
    return out
end

local actions = {}

actions["GET:status"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local now = std.time.now()
    local s = load()
    return router.ok(view(s, state.week(now), now), s.v)
end

actions["POST:join"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local now = std.time.now()
    local season = state.week(now)
    local s = load()
    if s.season == season then
        return router.fail(409, "already_joined", "you are already in this season")
    end
    local size = plan.cap(req, "competitions", "bracket_size")
    local open = data.channel:get("comp:" .. season .. ":open") or { g = 1, n = 0 }
    if open.n >= size then
        open = { g = open.g + 1, n = 0 }
    end
    local key = standings_key(season, open.g)
    local group = data.channel:get(key) or {}
    group[req.user.id] = 0
    open.n = open.n + 1
    data.channel:set(key, group, { ttl = 28 * 86400 })
    data.channel:set("comp:" .. season .. ":open", open, { ttl = 21 * 86400 })
    if s.season > 0 then
        -- Remember last season's bracket so its reward can still be claimed.
        s.prev = { season = s.season, g = s.g }
        local keep = s.claimed[tostring(s.season)]
        s.claimed = keep and { [tostring(s.season)] = keep } or {}
    end
    s.season, s.g, s.pts, s.seen = season, open.g, 0, {}
    local version = state.save(KEY, s)
    return router.ok(view(s, season, now), version)
end

actions["POST:report_match"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, {
        type = "object",
        fields = { match_id = { type = "string", max_len = 48 }, result = { type = "string", enum = { "win", "draw", "loss" } } },
    })
    if bad then
        return bad
    end
    local c = conf()
    local now = std.time.now()
    local season = state.week(now)
    local s = load()
    if s.season ~= season then
        return router.fail(409, "not_joined", "join this season first")
    end
    local today = state.day(now)
    if s.day ~= today then
        s.day, s.today = today, 0
    end
    if s.today >= c.max_matches_per_day then
        return router.plan_limit("max_matches_per_day", c.max_matches_per_day, "no more matches count today")
    end
    if s.seen[body.match_id] then
        return router.fail(409, "duplicate_match", "that match was already reported")
    end
    -- Keep the replay list short: only the newest 20 match ids are remembered.
    local ids = {}
    for id in pairs(s.seen) do
        table.insert(ids, id)
    end
    if #ids >= 20 then
        s.seen = {}
    end
    s.seen[body.match_id] = 1
    local gained = body.result == "win" and c.win or (body.result == "draw" and c.draw or 0)
    s.pts = s.pts + gained
    s.today = s.today + 1
    local key = standings_key(season, s.g)
    local group = data.channel:get(key) or {}
    group[req.user.id] = s.pts
    data.channel:set(key, group, { ttl = 28 * 86400 })
    progress.stat("competition_points", s.pts, { mode = "set" })
    local version = state.save(KEY, s)
    local out = view(s, season, now)
    out.gained = gained
    return router.ok(out, version)
end

actions["POST:claim_season"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", fields = { season = { type = "integer", min = 1 } } })
    if bad then
        return bad
    end
    local c = conf()
    local now = std.time.now()
    local s = load()
    if body.season >= state.week(now) then
        return router.fail(409, "season_running", "that season is still running")
    end
    local g = nil
    if s.season == body.season then
        g = s.g
    elseif s.prev and s.prev.season == body.season then
        g = s.prev.g
    end
    if g == nil then
        return router.fail(404, "not_in_season", "you did not play that season")
    end
    if s.claimed[tostring(body.season)] == 1 then
        return router.fail(409, "already_claimed", "that season was already claimed")
    end
    local group = data.channel:get(standings_key(body.season, g)) or {}
    local my_rank = 0
    for _, row in ipairs(ranked(group)) do
        if row.user == req.user.id then
            my_rank = row.rank
        end
    end
    local won = nil
    for _, tier in ipairs(c.rewards) do
        if my_rank > 0 and my_rank <= tier.rank then
            won = tier.reward
            break
        end
    end
    if won ~= nil then
        local failure = reward.give(won)
        if failure then
            return failure
        end
    end
    s.claimed[tostring(body.season)] = 1
    local version = state.save(KEY, s)
    return router.ok({ season = body.season, rank = my_rank, reward = won }, version)
end

return {
    auth = "user",
    methods = { "GET", "POST" },
    uses = { "progress" },
    data = { read = { "channel", "user" }, write = { "channel", "user" } },
    rate = { per_user = 30, window = 60 },
    description = "Asynchronous brackets and weekly seasons",
    handler = router.dispatch(actions),
}
