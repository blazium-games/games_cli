-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

return {
    first_spin_is_free_and_pays = function()
        local ada = who()
        local r = t.call("wheel/spin", { user = ada })
        t.eq(r.status, 200)
        t.ok(r.body.data.slice >= 1 and r.body.data.slice <= 8, "slice index inside the wheel")
        t.ok(r.body.data.reward ~= nil)
        t.eq(r.body.data.paid, false)
        t.eq(r.body.data.free_spins, 0)
    end,

    extra_spin_is_charged_then_capped_by_plan = function()
        local ada = who()
        t.eq(t.call("wheel/spin", { user = ada }).status, 200)
        local broke = t.call("wheel/spin", { user = ada })
        t.eq(broke.status, 402)
        t.call("economy/grant", { user = ada, method = "CALL", body = { currency = "gems", amount = 10 } })
        local paid = t.call("wheel/spin", { user = ada })
        t.eq(paid.status, 200)
        t.eq(paid.body.data.paid, true)
        local over = t.call("wheel/spin", { user = ada })
        t.eq(over.status, 403)
        t.eq(over.body.error.key, "plan_limit")
    end,

    the_free_spin_returns_the_next_day = function()
        local ada = who()
        t.call("wheel/spin", { user = ada })
        t.clock.advance(86400)
        local s = t.call("wheel/status", { user = ada, method = "GET" })
        t.eq(s.body.data.free_spins, 1)
    end,
}
