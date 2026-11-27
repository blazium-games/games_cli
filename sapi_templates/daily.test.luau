-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function claim(user)
    return t.call("daily/claim", { user = user })
end

local function coins(user)
    return t.call("economy/state", { user = user, method = "GET" }).body.data.wallet.coins
end

return {
    first_claim_pays_day_one = function()
        local ada, bob = who(), who()
        local r = claim(ada)
        t.eq(r.status, 200)
        t.eq(r.body.data.streak, 1)
        t.eq(r.body.data.reward.coins, 50)
        t.eq(coins(ada), 50)
    end,

    one_claim_per_utc_day = function()
        local ada, bob = who(), who()
        claim(ada)
        local again = claim(ada)
        t.eq(again.status, 409)
        t.eq(again.body.error.key, "already_claimed")
        t.ok(again.body.error.next_claim_in > 0, "tells the player when the next claim opens")
    end,

    streak_grows_on_consecutive_days = function()
        local ada, bob = who(), who()
        claim(ada)
        t.clock.advance(86400)
        local two = claim(ada)
        t.eq(two.body.data.streak, 2)
        t.eq(two.body.data.reward.coins, 75)
        t.clock.advance(86400)
        t.eq(claim(ada).body.data.streak, 3)
        t.eq(coins(ada), 50 + 75 + 100)
    end,

    a_missed_day_resets_the_streak = function()
        local ada, bob = who(), who()
        claim(ada)
        t.clock.advance(86400)
        claim(ada)
        t.clock.advance(3 * 86400)
        local r = claim(ada)
        t.eq(r.body.data.streak, 1)
        t.eq(r.body.data.best, 2)
    end,

    status_reports_claimable = function()
        local ada, bob = who(), who()
        local before = t.call("daily/status", { user = ada, method = "GET" })
        t.eq(before.body.data.claimable, true)
        claim(ada)
        local after = t.call("daily/status", { user = ada, method = "GET" })
        t.eq(after.body.data.claimable, false)
        t.eq(after.body.data.streak, 1)
    end,

    dispenser_has_a_cooldown = function()
        local ada, bob = who(), who()
        local first = t.call("daily/dispenser", { user = ada, body = { id = "free_chest" } })
        t.eq(first.status, 200)
        t.eq(coins(ada), 25)
        local again = t.call("daily/dispenser", { user = ada, body = { id = "free_chest" } })
        t.eq(again.status, 409)
        t.ok(again.body.error.ready_in > 0)
        t.clock.advance(14400)
        t.eq(t.call("daily/dispenser", { user = ada, body = { id = "free_chest" } }).status, 200)
        t.eq(t.call("daily/dispenser", { user = ada, body = { id = "nope" } }).status, 404)
    end,

    needs_a_player = function()
        local ada, bob = who(), who()
        t.eq(t.call("daily/claim", {}).status, 401)
    end,
}
