-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function run(user, score, board_name)
    local started = t.call("leaderboards/start_run", { user = user, body = { board = board_name } })
    t.clock.advance(5)
    return t.call("leaderboards/submit", { service = true, user = user, body = { board = board_name, score = score, run_ticket = started.body.data.run_ticket, details = { moves = 12 } } })
end

return {
    players_cannot_submit_their_own_scores = function()
        local ada = who()
        local started = t.call("leaderboards/start_run", { user = ada, body = {} })
        t.clock.advance(5)
        local r = t.call("leaderboards/submit", { user = ada, body = { board = "main", score = 100, run_ticket = started.body.data.run_ticket } })
        t.eq(r.status, 403)
        t.eq(t.call("leaderboards/profile", { method = "GET", user = ada, query = { board = "main" } }).body.data.rank, nil)
    end,

    a_game_server_must_name_the_player = function()
        t.eq(t.call("leaderboards/submit", { service = true, body = { board = "main", score = 100 } }).status, 400)
    end,

    submit_needs_a_run_ticket = function()
        local ada = who()
        local r = t.call("leaderboards/submit", { service = true, user = ada, body = { board = "main", score = 10 } })
        t.eq(r.status, 409)
        t.eq(r.body.error.key, "bad_ticket")
    end,

    a_ticket_works_once = function()
        local ada = who()
        local started = t.call("leaderboards/start_run", { user = ada, body = {} })
        t.eq(started.status, 200)
        t.clock.advance(5)
        local body = { board = "main", score = 100, run_ticket = started.body.data.run_ticket }
        t.eq(t.call("leaderboards/submit", { service = true, user = ada, body = body }).status, 200)
        t.eq(t.call("leaderboards/submit", { service = true, user = ada, body = body }).status, 409)
    end,

    runs_cannot_finish_faster_than_possible = function()
        local ada = who()
        local started = t.call("leaderboards/start_run", { user = ada, body = {} })
        local r = t.call("leaderboards/submit", { service = true, user = ada, body = { board = "main", score = 5, run_ticket = started.body.data.run_ticket } })
        t.eq(r.status, 422)
        t.eq(r.body.error.key, "too_fast")
    end,

    top_lists_best_scores_publicly = function()
        local a, b = who(), who()
        run(a, 500, "main")
        run(b, 900, "main")
        local top = t.call("leaderboards/top", { method = "GET", query = { board = "main", n = "50" } })
        t.eq(top.status, 200)
        local seen_b, seen_a = 0, 0
        for i, e in ipairs(top.body.data.entries) do
            if e.user == b then
                seen_b = i
            elseif e.user == a then
                seen_a = i
            end
        end
        t.ok(seen_b > 0 and seen_a > 0 and seen_b < seen_a, "the higher score ranks first")
    end,

    best_mode_keeps_the_best_score = function()
        local ada = who()
        run(ada, 700, "main")
        run(ada, 300, "main")
        local profile = t.call("leaderboards/profile", { method = "GET", user = ada, query = { board = "main" } })
        t.eq(profile.body.data.rank.score, 700)
        t.eq(profile.body.data.details.score, 700)
    end,

    score_range_and_board_names_are_checked = function()
        local ada = who()
        local started = t.call("leaderboards/start_run", { user = ada, body = {} })
        t.clock.advance(5)
        t.eq(t.call("leaderboards/submit", { service = true, user = ada, body = { board = "main", score = -1, run_ticket = started.body.data.run_ticket } }).status, 422)
        t.eq(t.call("leaderboards/start_run", { user = ada, body = { board = "no_such" } }).status, 404)
        t.eq(t.call("leaderboards/top", { method = "GET", query = { board = "no_such" } }).status, 404)
    end,

    daily_seed_is_shared_and_rotates = function()
        local a = t.call("leaderboards/daily_seed", { method = "GET" }).body.data
        local b = t.call("leaderboards/daily_seed", { method = "GET", user = who() }).body.data
        t.eq(a.seed, b.seed)
        t.clock.advance(86400)
        t.ok(t.call("leaderboards/daily_seed", { method = "GET" }).body.data.seed ~= a.seed, "a new seed every UTC day")
    end,

    free_plan_serves_two_boards = function()
        local ada = who()
        t.eq(t.call("leaderboards/start_run", { user = ada, body = { board = "weekly" } }).status, 200)
    end,
}
