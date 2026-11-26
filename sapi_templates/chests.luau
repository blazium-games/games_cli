-- Star and milestone chests (spec 02).
--
--   GET  /v1/{game}/chests/status      stars, milestones, which are claimable
--   POST /v1/{game}/chests/add_stars   { stars }  game servers only by default (a player cannot award themselves stars)
--   POST /v1/{game}/chests/claim       { milestone }  open an earned chest
--   CALL chests/add_stars { stars }    (api.call only, for example from quests or leaderboards)
--
-- Set chests_config.client_add_stars = true (with client_add_max) to let the game client report stars itself.
-- Configure milestones in "chests_config": { milestones = { { stars = 10, reward = { coins = 100 } }, ... } }
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")
local reward = require("lib.reward")

local KEY = "chests:state"

local DEFAULTS = {
    milestones = {
        { stars = 10, reward = { coins = 100 } },
        { stars = 25, reward = { coins = 250 } },
        { stars = 50, reward = { gems = 2 } },
        { stars = 100, reward = { coins = 600, gems = 3 } },
        { stars = 200, reward = { gems = 10 } },
    },
    client_add_stars = false,
    client_add_max = 3,
}

local function conf(req)
    local c = state.config("chests_config", DEFAULTS)
    c.milestones = plan.take(req, "chests", "milestones", c.milestones)
    return c
end

local function load()
    return state.load(KEY, { v = 0, stars = 0, claimed = {} })
end

local function view(c, s)
    local rows = {}
    for i, m in ipairs(c.milestones) do
        rows[i] = { index = i, stars = m.stars, reward = m.reward, claimed = s.claimed[tostring(i)] == 1, earned = s.stars >= m.stars }
    end
    return { stars = s.stars, milestones = rows }
end

local function add(s, stars)
    s.stars = s.stars + stars
    progress.stat("chest_stars", s.stars, { mode = "max" })
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

actions["POST:add_stars"] = function(req)
    local c = conf(req)
    local limit = 0
    if req.caller == "service" or req.caller == "platform" then
        limit = 1000
    elseif c.client_add_stars then
        local denied = guard.user(req)
        if denied then
            return denied
        end
        limit = c.client_add_max
    else
        return router.fail(403, "forbidden", "stars are awarded by the game server")
    end
    local body, bad = router.body(req, { type = "object", fields = { stars = { type = "integer", min = 1, max = limit } } })
    if bad then
        return bad
    end
    if req.user == nil then
        return router.bad_request("a game server must name the player (X-Sapi-User)")
    end
    local s = load()
    add(s, body.stars)
    local version = state.save(KEY, s)
    return router.ok(view(c, s), version)
end

actions["CALL:add_stars"] = function(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local stars = math.floor((req.body or {}).stars or 0)
    if stars < 1 then
        return router.bad_request("stars must be at least 1")
    end
    local s = load()
    add(s, stars)
    local version = state.save(KEY, s)
    return router.ok(view(conf(req), s), version)
end

actions["POST:claim"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", fields = { milestone = { type = "integer", min = 1, max = 100 } } })
    if bad then
        return bad
    end
    local c = conf(req)
    local m = c.milestones[body.milestone]
    if m == nil then
        return router.fail(404, "unknown_milestone", "no such milestone")
    end
    local s = load()
    if s.stars < m.stars then
        return router.fail(409, "not_earned", "not enough stars", { stars = s.stars, needed = m.stars })
    end
    local key = tostring(body.milestone)
    if s.claimed[key] == 1 then
        return router.fail(409, "already_claimed", "this chest was already opened")
    end
    local failure, missing = reward.give(m.reward)
    if failure then
        return failure
    end
    s.claimed[key] = 1
    local version = state.save(KEY, s)
    local out = view(c, s)
    out.reward = m.reward
    out.missing = missing
    return router.ok(out, version)
end

return {
    auth = "user",
    methods = { "GET", "POST" },
    uses = { "progress" },
    data = { read = { "user" }, write = { "user" } },
    rate = { per_user = 30, window = 60 },
    description = "Star and milestone chests",
    handler = router.dispatch(actions),
}
