return {
    bootstrap_collects_every_installed_feature = function()
        local ada = "u" .. std.crypto.random_hex(6)
        local r = t.call("hub/bootstrap", { user = ada, method = "GET" })
        t.eq(r.status, 200)
        local f = r.body.data.features
        t.ok(f.economy ~= nil and f.daily ~= nil and f.lives ~= nil and f.wheel ~= nil, "core features answer")
        t.ok(f.chests ~= nil and f.quests ~= nil and f.meta ~= nil and f.competitions ~= nil, "progression features answer")
        t.ok(f.leaderboards.seed ~= nil, "the daily seed rides along")
        t.eq(f.lives.lives, 5)
        t.eq(r.body.data.plan, "free")
        t.ok(#r.body.data.enabled >= 8, "the enabled list names every feature that answered")
    end,

    bootstrap_needs_a_player = function()
        t.eq(t.call("hub/bootstrap", { method = "GET" }).status, 401)
    end,

    other_verbs_are_refused = function()
        t.eq(t.call("hub/bootstrap", { user = "x" }).status, 404)
    end,

    merge_moves_one_player_into_another = function()
        local from, to = "u" .. std.crypto.random_hex(6), "u" .. std.crypto.random_hex(6)
        t.call("economy/grant", { user = from, method = "CALL", body = { currency = "coins", amount = 70 } })
        t.call("economy/grant", { user = to, method = "CALL", body = { currency = "coins", amount = 30 } })
        t.call("lives/grant", { user = from, method = "CALL", body = { unlimited_sec = 600 } })
        t.eq(t.call("daily/claim", { user = from }).status, 200)
        t.eq(t.call("lives/spend", { user = to }).status, 200)

        local r = t.call("hub/merge", { service = true, body = { from = from, to = to } })
        t.eq(r.status, 200)
        local merged = {}
        for _, id in ipairs(r.body.data.merged) do
            merged[id] = true
        end
        t.ok(merged.economy and merged.daily and merged.lives, "the features with something stored were merged")
        t.ok(not merged.wheel, "a feature with nothing stored for the guest is skipped")

        t.eq(t.call("economy/state", { user = to, method = "GET" }).body.data.wallet.coins, 150)
        t.eq(t.call("economy/state", { user = from, method = "GET" }).body.data.wallet.coins, nil)
        t.eq(t.call("daily/status", { user = to, method = "GET" }).body.data.streak, 1)
        t.eq(t.call("lives/status", { user = to, method = "GET" }).body.data.unlimited, true)

        -- 70 + 30 plus the 50 the guest's daily claim paid; a repeat finds nothing left to move.
        local again = t.call("hub/merge", { service = true, body = { from = from, to = to } })
        t.eq(#again.body.data.merged, 0)
        t.eq(t.call("economy/state", { user = to, method = "GET" }).body.data.wallet.coins, 150)
    end,

    merge_is_not_for_players = function()
        local ada, bob = "u" .. std.crypto.random_hex(6), "u" .. std.crypto.random_hex(6)
        local r = t.call("hub/merge", { user = ada, body = { from = bob, to = ada } })
        t.eq(r.status, 403)
    end,

    merge_needs_two_different_players = function()
        local ada = "u" .. std.crypto.random_hex(6)
        t.eq(t.call("hub/merge", { service = true, body = { from = ada, to = ada } }).status, 400)
        t.eq(t.call("hub/merge", { service = true, body = { from = ada } }).status, 400)
        t.eq(t.call("hub/merge", { service = true, body = { from = 5, to = ada } }).status, 400)
    end,
}