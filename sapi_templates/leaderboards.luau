-- Leaderboards, seeded daily runs and anti-cheat run tickets (spec 03).
--
--   GET  /v1/{game}/leaderboards/top?board=main&n=10        public
--   GET  /v1/{game}/leaderboards/around?board=main&n=5      the caller and the players around them
--   GET  /v1/{game}/leaderboards/profile?board=main&user=   a player's rank, score and stored run details
--   GET  /v1/{game}/leaderboards/daily_seed                 today's shared seed (same for every player)
--   POST /v1/{game}/leaderboards/start_run                  { board? }  returns a single-use run_ticket
--   POST /v1/{game}/leaderboards/submit                     { board, score, run_ticket, details? }
--
-- Boards are named in config "leaderboards_config": { boards = { main = { mode = "best", max_score = 1000000000,
-- min_run_sec = 3, require_ticket = true } } }. A plan allows a fixed number of boards (Free: 2).
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")

local KEY = "lb:state"

local DEFAULTS = {
    boards = {
        main = { mode = "best", max_score = 1000000000, min_run_sec = 3, require_ticket = true },
        weekly = { mode = "best", max_score = 1000000000, min_run_sec = 3, require_ticket = true },
    },
    ticket_ttl_sec = 3600,
}

local function conf()
    return state.config("leaderboards_config", DEFAULTS)
end

-- The boards this plan serves, in name order.
local function board_names(req, c)
    local names = {}
    for name in pairs(c.boards) do
        table.insert(names, name)
    end
    table.sort(names)
    return plan.take(req, "leaderboards", "boards", names)
end

local function board_conf(req, c, name)
    if type(name) ~= "string" then
        return nil
    end
    for _, allowed in ipairs(board_names(req, c)) do
        if allowed == name then
            return c.boards[name]
        end
    end
    return nil
end

local function number(req, key, default, max)
    local n = tonumber(req.query[key])
    if n == nil then
        return default
    end
    return math.max(1, math.min(max, math.floor(n)))
end

-- Today's seed: the same for every player, different every UTC day.
local function seed_for(req, day)
    return tonumber(string.sub(std.crypto.sha256("seed:" .. req.game .. ":" .. day), 1, 8), 16)
end

-- board:rank(user) returns (rank, score); wrap it so replies carry one object.
local function standing(b, who)
    local rank, score = b:rank(who)
    if rank == nil then
        return nil
    end
    return { rank = rank, score = score }
end

local actions = {}

actions["GET:top"] = function(req)
    local c = conf()
    local name = req.query.board or "main"
    if board_conf(req, c, name) == nil then
        return router.fail(404, "unknown_board", "no such board")
    end
    local entries = board(name):top(number(req, "n", 10, 100), { offset = number(req, "offset", 1, 10000) - 1 })
    return router.ok({ board = name, entries = entries, count = board(name):count() })
end

actions["GET:around"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local c = conf()
    local name = req.query.board or "main"
    if board_conf(req, c, name) == nil then
        return router.fail(404, "unknown_board", "no such board")
    end
    local b = board(name)
    return router.ok({ board = name, me = standing(b), entries = b:around(nil, number(req, "n", 5, 25)) })
end

actions["GET:profile"] = function(req)
    local c = conf()
    local name = req.query.board or "main"
    if board_conf(req, c, name) == nil then
        return router.fail(404, "unknown_board", "no such board")
    end
    local who = req.query.user or (req.user and req.user.id)
    if who == nil then
        return router.bad_request("name a user")
    end
    local meta = data.channel:get("lb_meta:" .. name .. ":" .. who)
    return router.ok({ board = name, user = who, rank = standing(board(name), who), details = meta })
end

actions["GET:daily_seed"] = function(req)
    local now = std.time.now()
    local day = state.day(now)
    return router.ok({ day = day, seed = seed_for(req, day), resets_in = (day + 1) * 86400 - now })
end

actions["POST:start_run"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", extra = true, fields = { board = { type = "string", max_len = 32, optional = true } } })
    if bad then
        return bad
    end
    local c = conf()
    local name = body.board or "main"
    if board_conf(req, c, name) == nil then
        return router.fail(404, "unknown_board", "no such board")
    end
    local s = state.load(KEY, { v = 0, runs = 0 })
    s.tid = std.crypto.random_hex(8)
    s.tb = name
    s.ts = std.time.now()
    s.runs = s.runs + 1
    local version = state.save(KEY, s)
    return router.ok({ run_ticket = s.tid, board = name, seed = seed_for(req, state.day()) }, version)
end

actions["POST:submit"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, {
        type = "object",
        fields = {
            board = { type = "string", max_len = 32 },
            score = { type = "number" },
            run_ticket = { type = "string", max_len = 64, optional = true },
            details = { type = "object", extra = true, optional = true },
        },
    })
    if bad then
        return bad
    end
    local c = conf()
    local bc = board_conf(req, c, body.board)
    if bc == nil then
        return router.fail(404, "unknown_board", "no such board")
    end
    if body.score < 0 or body.score > bc.max_score then
        return router.fail(422, "score_out_of_range", "the score is outside what this board accepts")
    end
    local s = state.load(KEY, { v = 0, runs = 0 })
    local now = std.time.now()
    if bc.require_ticket then
        if s.tid == nil or body.run_ticket ~= s.tid or s.tb ~= body.board then
            return router.fail(409, "bad_ticket", "start a run before you submit")
        end
        if now - s.ts > c.ticket_ttl_sec then
            s.tid = nil
            state.save(KEY, s)
            return router.fail(409, "stale", "that run ticket has expired")
        end
        if now - s.ts < bc.min_run_sec then
            return router.fail(422, "too_fast", "that run finished faster than is possible")
        end
        s.tid = nil -- single use
    end
    board(body.board):submit(body.score, { mode = bc.mode })
    -- A compact sidecar of run details for profiles, held to the plan's size.
    if body.details ~= nil then
        local encoded = std.json.encode(body.details)
        if #encoded <= plan.cap(req, "leaderboards", "details_bytes") then
            local key = "lb_meta:" .. body.board .. ":" .. req.user.id
            local previous = data.channel:get(key)
            if previous == nil or (bc.mode == "lowest" and body.score < previous.score) or (bc.mode ~= "lowest" and body.score > previous.score) then
                data.channel:set(key, { score = body.score, details = body.details, at = now })
            end
        end
    end
    progress.stat("best_" .. body.board, body.score, { mode = bc.mode == "lowest" and "min" or "max" })
    local version = state.save(KEY, s)
    return router.ok({ board = body.board, rank = standing(board(body.board)) }, version)
end

return {
    auth = "anonymous",
    methods = { "GET", "POST" },
    uses = { "boards", "progress" },
    data = { read = { "channel", "user" }, write = { "channel", "user" } },
    rate = { per_user = 60, window = 60, per_ip = 120 },
    description = "Leaderboards, seeded daily runs and run tickets",
    handler = router.dispatch(actions),
}
