-- Achievement bounties and rank ladders (spec 07).
--
--   GET  /v1/{game}/meta/status         stats, bounties (done / claimed), rank and the next rank
--   POST /v1/{game}/meta/claim_bounty   { id }  pay a finished bounty in economy currency
--   POST /v1/{game}/meta/sync_rank      recompute the rank from rank points
--   CALL meta/record { stat, value, mode? }  (api.call only) record a stat: it is kept here and mirrored to progress.stat
--   CALL meta/add_points { points }          (api.call only) add rank points
--
-- Achievements and stats themselves live in the platform progress service; this script keeps its own copy of the
-- counters that bounties need, because a script can write progress but not read it back.
-- Configure "meta_config": { bounties = { { id = "first_win", stat = "wins", target = 1, reward = { coins = 100 } } },
-- ranks = { { name = "Bronze", points = 0 }, ... } }
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")
local reward = require("lib.reward")

local KEY = "meta:state"

local DEFAULTS = {
    bounties = {
        { id = "first_win", stat = "wins", target = 1, reward = { coins = 100 } },
        { id = "ten_wins", stat = "wins", target = 10, reward = { coins = 300 } },
        { id = "hundred_runs", stat = "runs", target = 100, reward = { gems = 5 } },
    },
    ranks = {
        { name = "Bronze", points = 0 },
        { name = "Silver", points = 100 },
        { name = "Gold", points = 300 },
        { name = "Platinum", points = 700 },
        { name = "Diamond", points = 1500 },
    },
}

local function conf(req)
    local c = state.config("meta_config", DEFAULTS)
    c.bounties = plan.take(req, "meta", "bounties", c.bounties)
    c.ranks = plan.take(req, "meta", "ranks", c.ranks)
    return c
end

local function load()
    return state.load(KEY, { v = 0, stats = {}, claimed = {}, pts = 0, rank = 1 })
end

local function rank_of(c, points)
    local index = 1
    for i, r in ipairs(c.ranks) do
        if points >= r.points then
            index = i
        end
    end
    return index
end

local function view(c, s)
    local bounties = {}
    for i, b in ipairs(c.bounties) do
        local have = math.min(s.stats[b.stat] or 0, b.target)
        bounties[i] = { id = b.id, stat = b.stat, target = b.target, progress = have, done = have >= b.target, claimed = s.claimed[b.id] == 1, reward = b.reward }
    end
    local index = rank_of(c, s.pts)
    local nextr = c.ranks[index + 1]
    return {
        stats = s.stats,
        bounties = bounties,
        points = s.pts,
        rank = c.ranks[index].name,
        rank_index = index,
        next_rank = nextr and nextr.name or nil,
        next_rank_points = nextr and nextr.points or nil,
    }
end

local actions = {}

actions["GET:status"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local s = load()
    return router.ok(view(conf(req), s), s.v)
end

actions["POST:claim_bounty"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", fields = { id = { type = "string", max_len = 48 } } })
    if bad then
        return bad
    end
    local c = conf(req)
    local s = load()
    for _, b in ipairs(c.bounties) do
        if b.id == body.id then
            if (s.stats[b.stat] or 0) < b.target then
                return router.fail(409, "not_done", "this bounty is not finished")
            end
            if s.claimed[b.id] == 1 then
                return router.fail(409, "already_claimed", "this bounty was already claimed")
            end
            local failure, missing = reward.give(b.reward)
            if failure then
                return failure
            end
            s.claimed[b.id] = 1
            progress.unlock("bounty_" .. b.id)
            local version = state.save(KEY, s)
            local out = view(c, s)
            out.reward = b.reward
            out.missing = missing
            return router.ok(out, version)
        end
    end
    return router.fail(404, "unknown_bounty", "no such bounty")
end

actions["POST:sync_rank"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local c = conf(req)
    local s = load()
    local index = rank_of(c, s.pts)
    local promoted = index > s.rank
    s.rank = index
    if promoted then
        progress.stat("rank", index, { mode = "max" })
    end
    local version = state.save(KEY, s)
    local out = view(c, s)
    out.promoted = promoted
    return router.ok(out, version)
end

actions["CALL:record"] = function(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local body = req.body or {}
    if type(body.stat) ~= "string" or type(body.value) ~= "number" then
        return router.bad_request("record needs a stat name and a number")
    end
    local mode = body.mode or "add"
    local s = load()
    local have = s.stats[body.stat] or 0
    if mode == "add" then
        have = have + body.value
    elseif mode == "max" then
        have = math.max(have, body.value)
    elseif mode == "min" then
        have = have == 0 and body.value or math.min(have, body.value)
    else
        have = body.value
    end
    s.stats[body.stat] = have
    progress.stat(body.stat, body.value, { mode = mode })
    local version = state.save(KEY, s)
    return router.ok({ stat = body.stat, value = have }, version)
end

actions["CALL:add_points"] = function(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local points = math.floor((req.body or {}).points or 0)
    if points == 0 then
        return router.bad_request("points must not be zero")
    end
    local s = load()
    s.pts = math.max(0, s.pts + points)
    local version = state.save(KEY, s)
    return router.ok(view(conf(req), s), version)
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
        return state.combine(a, b, { stats = "summap", claimed = "union", pts = "sum", rank = "max" })
    end)
    return router.ok({ merged = merged })
end

return {
    auth = "user",
    methods = { "GET", "POST" },
    uses = { "progress" },
    data = { read = { "user" }, write = { "user" } },
    rate = { per_user = 30, window = 60 },
    description = "Achievement bounties and rank ladders",
    handler = router.dispatch(actions),
}
