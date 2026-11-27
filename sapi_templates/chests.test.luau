-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

return {
    players_cannot_award_their_own_stars = function()
        local ada = who()
        local r = t.call("chests/add_stars", { user = ada, body = { stars = 50 } })
        t.eq(r.status, 403)
    end,

    a_game_server_awards_stars_and_the_chest_opens_once = function()
        local ada = who()
        local added = t.call("chests/add_stars", { service = true, user = ada, body = { stars = 12 } })
        t.eq(added.status, 200)
        t.eq(added.body.data.stars, 12)
        local early = t.call("chests/claim", { user = ada, body = { milestone = 2 } })
        t.eq(early.status, 409)
        t.eq(early.body.error.key, "not_earned")
        local ok = t.call("chests/claim", { user = ada, body = { milestone = 1 } })
        t.eq(ok.status, 200)
        t.eq(ok.body.data.reward.coins, 100)
        t.eq(t.call("chests/claim", { user = ada, body = { milestone = 1 } }).status, 409)
        local wallet = t.call("economy/state", { user = ada, method = "GET" })
        t.eq(wallet.body.data.wallet.coins, 100)
    end,

    scripts_add_stars_internally = function()
        local ada = who()
        local r = t.call("chests/add_stars", { user = ada, method = "CALL", body = { stars = 30 } })
        t.eq(r.body.data.stars, 30)
        t.eq(t.call("chests/add_stars", { user = ada, method = "CALL", body = { stars = 0 } }).status, 400)
    end,

    unknown_milestone_is_404 = function()
        t.eq(t.call("chests/claim", { user = who(), body = { milestone = 99 } }).status, 404)
    end,
}
