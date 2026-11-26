-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function record(user, stat, value, mode)
    return t.call("meta/record", { user = user, method = "CALL", body = { stat = stat, value = value, mode = mode } })
end

return {
    bounty_needs_the_stat_then_pays_once = function()
        local ada = who()
        t.eq(t.call("meta/claim_bounty", { user = ada, body = { id = "first_win" } }).status, 409)
        record(ada, "wins", 1)
        local r = t.call("meta/claim_bounty", { user = ada, body = { id = "first_win" } })
        t.eq(r.status, 200)
        t.eq(r.body.data.reward.coins, 100)
        t.eq(t.call("meta/claim_bounty", { user = ada, body = { id = "first_win" } }).status, 409)
        t.eq(t.call("economy/state", { user = ada, method = "GET" }).body.data.wallet.coins, 100)
        t.eq(t.call("meta/claim_bounty", { user = ada, body = { id = "nope" } }).status, 404)
    end,

    stat_modes = function()
        local ada = who()
        record(ada, "best_time", 50, "min")
        record(ada, "best_time", 70, "min")
        t.eq(record(ada, "best_time", 40, "min").body.data.value, 40)
        t.eq(record(ada, "hi", 9, "max").body.data.value, 9)
        t.eq(record(ada, "hi", 3, "max").body.data.value, 9)
        t.eq(record(ada, "runs", 2).body.data.value, 2)
        t.eq(record(ada, "runs", 3).body.data.value, 5)
        t.eq(t.call("meta/record", { user = ada, method = "CALL", body = { stat = 5 } }).status, 400)
    end,

    rank_follows_points = function()
        local ada = who()
        t.eq(t.call("meta/status", { user = ada, method = "GET" }).body.data.rank, "Bronze")
        t.call("meta/add_points", { user = ada, method = "CALL", body = { points = 120 } })
        local r = t.call("meta/sync_rank", { user = ada })
        t.eq(r.body.data.rank, "Silver")
        t.eq(r.body.data.promoted, true)
        t.eq(r.body.data.next_rank, "Gold")
        t.eq(t.call("meta/sync_rank", { user = ada }).body.data.promoted, false)
    end,

    internal_actions_are_not_public = function()
        local ada = who()
        t.eq(t.call("meta/record", { user = ada, body = { stat = "wins", value = 1 } }).status, 404)
        t.eq(t.call("meta/add_points", { user = ada, body = { points = 5 } }).status, 404)
    end,
}
