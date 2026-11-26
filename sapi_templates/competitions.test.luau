-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function report(user, id, result)
    return t.call("competitions/report_match", { user = user, body = { match_id = id, result = result } })
end

return {
    join_then_play_for_points = function()
        local ada = who()
        t.eq(t.call("competitions/join", { user = ada }).status, 200)
        t.eq(t.call("competitions/join", { user = ada }).status, 409)
        local win = report(ada, "m1", "win")
        t.eq(win.status, 200)
        t.eq(win.body.data.gained, 3)
        t.eq(report(ada, "m2", "draw").body.data.points, 4)
        t.eq(report(ada, "m3", "loss").body.data.points, 4)
        t.eq(report(ada, "m1", "win").status, 409)
    end,

    you_must_join_before_reporting = function()
        t.eq(report(who(), "m1", "win").status, 409)
    end,

    standings_rank_the_bracket = function()
        local a, b = who(), who()
        t.call("competitions/join", { user = a })
        t.call("competitions/join", { user = b })
        report(a, "x", "win")
        report(b, "y", "loss")
        local s = t.call("competitions/status", { user = a, method = "GET" }).body.data
        t.eq(s.joined, true)
        t.ok(#s.standings >= 1)
        t.eq(s.standings[1].rank, 1)
    end,

    daily_match_limit_is_enforced = function()
        local ada = who()
        t.call("competitions/join", { user = ada })
        for i = 1, 20 do
            t.eq(report(ada, "m" .. i, "win").status, 200)
        end
        local over = report(ada, "m21", "win")
        t.eq(over.status, 403)
        t.eq(over.body.error.key, "plan_limit")
    end,

    free_brackets_hold_eight_players = function()
        local seen = {}
        for _ = 1, 9 do
            local u = who()
            t.call("competitions/join", { user = u })
            local s = t.call("competitions/status", { user = u, method = "GET" }).body.data
            seen[s.bracket] = (seen[s.bracket] or 0) + 1
        end
        for _, count in pairs(seen) do
            t.ok(count <= 8, "no bracket holds more than the plan's size")
        end
    end,

    season_reward_waits_for_the_season_to_end = function()
        local ada = who()
        t.call("competitions/join", { user = ada })
        report(ada, "w", "win")
        local season = t.call("competitions/status", { user = ada, method = "GET" }).body.data.season
        t.eq(t.call("competitions/claim_season", { user = ada, body = { season = season } }).status, 409)
        t.clock.advance(8 * 86400)
        local claimed = t.call("competitions/claim_season", { user = ada, body = { season = season } })
        t.eq(claimed.status, 200)
        t.ok(claimed.body.data.rank >= 1 and claimed.body.data.rank <= 8, "ranked inside the bracket")
        t.ok(claimed.body.data.reward ~= nil, "every place in a Free bracket of 8 earns a tier")
        t.eq(t.call("competitions/claim_season", { user = ada, body = { season = season } }).status, 409)
    end,
}
