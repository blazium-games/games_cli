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
}
