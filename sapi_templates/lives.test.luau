-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function status(user)
    return t.call("lives/status", { user = user, method = "GET" }).body.data
end

local function spend(user)
    return t.call("lives/spend", { user = user })
end

return {
    starts_full = function()
        local ada, bob = who(), who()
        local s = status(ada)
        t.eq(s.lives, 5)
        t.eq(s.max, 5)
    end,

    spending_runs_down_and_the_server_clock_refills = function()
        local ada, bob = who(), who()
        for _ = 1, 5 do
            t.eq(spend(ada).status, 200)
        end
        t.eq(status(ada).lives, 0)
        local out = spend(ada)
        t.eq(out.status, 402)
        t.eq(out.body.error.key, "no_lives")
        t.clock.advance(1800)
        t.eq(status(ada).lives, 1)
        t.clock.advance(1800 * 10)
        t.eq(status(ada).lives, 5)
    end,

    refill_charges_the_economy = function()
        local ada, bob = who(), who()
        spend(ada)
        t.eq(t.call("lives/refill", { user = ada }).status, 402)
        t.call("economy/grant", { user = ada, method = "CALL", body = { currency = "coins", amount = 50 } })
        local r = t.call("lives/refill", { user = ada })
        t.eq(r.status, 200)
        t.eq(r.body.data.lives, 5)
        t.eq(t.call("lives/refill", { user = ada }).status, 409)
    end,

    unlimited_pass_does_not_spend = function()
        local ada, bob = who(), who()
        t.call("lives/grant", { user = ada, method = "CALL", body = { unlimited_sec = 600 } })
        for _ = 1, 8 do
            t.eq(spend(ada).status, 200)
        end
        local s = status(ada)
        t.eq(s.unlimited, true)
        t.eq(s.lives, 5)
    end,

    continue_costs_gems = function()
        local ada, bob = who(), who()
        t.eq(t.call("lives/continue", { user = ada }).status, 402)
        t.call("economy/grant", { user = ada, method = "CALL", body = { currency = "gems", amount = 1 } })
        t.eq(t.call("lives/continue", { user = ada }).status, 200)
    end,

    gifts_reach_the_recipient_and_are_capped = function()
        local ada, bob = who(), who()
        spend(bob)
        spend(bob)
        t.eq(t.call("lives/gift_send", { user = ada, body = { to = bob } }).status, 200)
        t.eq(t.call("lives/gift_send", { user = ada, body = { to = ada } }).status, 400)
        local claimed = t.call("lives/gift_claim", { user = bob })
        t.eq(claimed.body.data.claimed, 1)
        t.eq(claimed.body.data.lives, 4)
        t.eq(t.call("lives/gift_claim", { user = bob }).body.data.claimed, 0)
        for i = 2, 5 do
            t.eq(t.call("lives/gift_send", { user = ada, body = { to = "friend" .. i } }).status, 200)
        end
        local over = t.call("lives/gift_send", { user = ada, body = { to = "one_more" } })
        t.eq(over.status, 403)
        t.eq(over.body.error.key, "plan_limit")
    end,

    grant_is_internal = function()
        local ada, bob = who(), who()
        t.eq(t.call("lives/grant", { user = ada, body = { n = 1 } }).status, 404)
    end,
}
