-- Lives / energy with server-clock regeneration, continues and social gifts (spec 05).
--
--   GET  /v1/{game}/lives/status       lives now, seconds to the next one, unlimited-until
--   POST /v1/{game}/lives/spend        spend one life to start a run
--   POST /v1/{game}/lives/refill       buy a full refill with economy coins
--   POST /v1/{game}/lives/continue     pay to continue a failed run
--   POST /v1/{game}/lives/gift_send    { to }     send a heart to another player (daily cap)
--   POST /v1/{game}/lives/gift_claim              collect today's hearts
--   CALL lives/grant { n?, unlimited_sec? }       (api.call only)
--
-- Lives are never stored as a ticking counter. The state holds the stored count and the time the next life
-- started regenerating, and every read works the current count out from the server clock.
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")
local reward = require("lib.reward")

local KEY = "lives:state"

local DEFAULTS = {
    max = 5,
    regen_sec = 1800,
    refill_cost = { coins = 50 },
    continue_cost = { gems = 1 },
    gift_ttl_sec = 172800,
}

local function conf(req)
    local c = state.config("lives_config", DEFAULTS)
    c.max = plan.clamp(req, "lives", "max_lives", c.max)
    return c
end

local function load(c)
    local s = state.load(KEY, { v = 0, n = c.max, t = 0, unl = 0, gday = 0, gsent = 0 })
    return s
end

-- Apply regeneration up to `now`. Returns the current count.
local function regen(s, c, now)
    if s.n >= c.max then
        s.n = math.max(s.n, c.max)
        s.t = now
        return s.n
    end
    local earned = math.floor((now - s.t) / c.regen_sec)
    if earned > 0 then
        s.n = math.min(c.max, s.n + earned)
        s.t = s.n >= c.max and now or s.t + earned * c.regen_sec
    end
    return s.n
end

local function view(s, c, now)
    local unlimited = s.unl > now
    return {
        lives = s.n,
        max = c.max,
        unlimited = unlimited,
        unlimited_left = unlimited and (s.unl - now) or 0,
        next_in = (s.n >= c.max or unlimited) and 0 or math.max(0, s.t + c.regen_sec - now),
        gifts_sent_today = s.gsent,
    }
end

local actions = {}

actions["GET:status"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local now = std.time.now()
    local c = conf(req)
    local s = load(c)
    regen(s, c, now)
    return router.ok(view(s, c, now), s.v)
end

actions["POST:spend"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local now = std.time.now()
    local c = conf(req)
    local s = load(c)
    regen(s, c, now)
    if s.unl <= now then
        if s.n < 1 then
            return router.fail(402, "no_lives", "no lives left", { next_in = math.max(0, s.t + c.regen_sec - now) })
        end
        if s.n >= c.max then
            s.t = now -- the regeneration clock starts when the player drops below full
        end
        s.n = s.n - 1
    end
    local version = state.save(KEY, s)
    return router.ok(view(s, c, now), version)
end

actions["POST:refill"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local now = std.time.now()
    local c = conf(req)
    local s = load(c)
    regen(s, c, now)
    if s.n >= c.max then
        return router.fail(409, "already_full", "lives are already full")
    end
    local failure = reward.charge(c.refill_cost)
    if failure then
        return failure
    end
    s.n = c.max
    s.t = now
    local version = state.save(KEY, s)
    return router.ok(view(s, c, now), version)
end

actions["POST:continue"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local failure = reward.charge(conf(req).continue_cost)
    if failure then
        return failure
    end
    progress.stat("continues_used", 1, { mode = "add" })
    return router.ok({ continued = true, cost = conf(req).continue_cost })
end

actions["POST:gift_send"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", fields = { to = { type = "string", max_len = 64 } } })
    if bad then
        return bad
    end
    if body.to == req.user.id then
        return router.fail(400, "self_gift", "you cannot send a heart to yourself")
    end
    local now = std.time.now()
    local today = state.day(now)
    local c = conf(req)
    local s = load(c)
    if s.gday ~= today then
        s.gday = today
        s.gsent = 0
    end
    local cap = plan.cap(req, "lives", "gifts_per_day")
    if s.gsent >= cap then
        return router.plan_limit("gifts_per_day", cap, "you have sent all of today's hearts")
    end
    -- The inbox lives in channel data: a player cannot write another player's data.user.
    data.channel:set("gift:" .. body.to .. ":" .. today .. ":" .. req.user.id, { from = req.user.id, at = now }, { ttl = c.gift_ttl_sec })
    s.gsent = s.gsent + 1
    local version = state.save(KEY, s)
    return router.ok({ sent = true, gifts_sent_today = s.gsent }, version)
end

actions["POST:gift_claim"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local now = std.time.now()
    local c = conf(req)
    local s = load(c)
    regen(s, c, now)
    local rows = data.channel:list({ prefix = "gift:" .. req.user.id .. ":", limit = 20 })
    local claimed = 0
    for _, row in ipairs(rows) do
        data.channel:delete(row.key)
        claimed = claimed + 1
    end
    s.n = math.min(c.max, s.n + claimed)
    local version = state.save(KEY, s)
    return router.ok({ claimed = claimed, lives = s.n }, version)
end

actions["CALL:grant"] = function(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local body = req.body or {}
    local now = std.time.now()
    local c = conf(req)
    local s = load(c)
    regen(s, c, now)
    if body.n then
        s.n = math.min(c.max, s.n + math.floor(body.n))
    end
    if body.unlimited_sec then
        s.unl = math.max(s.unl, now) + math.floor(body.unlimited_sec)
    end
    local version = state.save(KEY, s)
    return router.ok(view(s, c, now), version)
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
    local c = conf(req)
    local merged = state.merge_user(KEY, from, to, function(a, b)
        local out = state.combine(a, b, { unl = "max", gday = "max", t = "max" })
        out.n = math.min(c.max, (a.n or 0) + (b.n or 0))
        if (a.gday or 0) > (b.gday or 0) then
            out.gsent = a.gsent
        elseif (a.gday or 0) == (b.gday or 0) then
            out.gsent = (a.gsent or 0) + (b.gsent or 0)
        end
        return out
    end)
    return router.ok({ merged = merged })
end

return {
    auth = "user",
    methods = { "GET", "POST" },
    uses = { "progress" },
    data = { read = { "user", "channel" }, write = { "user", "channel" } },
    rate = { per_user = 60, window = 60 },
    description = "Lives and energy with server-clock regeneration, continues and heart gifts",
    handler = router.dispatch(actions),
}
