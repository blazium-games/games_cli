-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function grant(user, body)
    return t.call("economy/grant", { user = user, method = "CALL", body = body })
end

local function state_of(user)
    return t.call("economy/state", { user = user, method = "GET" }).body.data
end

return {
    grant_and_state = function()
        local ada, bob = who(), who()
        local r = grant(ada, { currency = "coins", amount = 150 })
        t.eq(r.status, 200)
        t.eq(r.body.data.wallet.coins, 150)
        t.eq(state_of(ada).wallet.coins, 150)
    end,

    purchase_cosmetic_charges_and_owns = function()
        local ada, bob = who(), who()
        grant(ada, { currency = "coins", amount = 150 })
        local r = t.call("economy/purchase", { user = ada, body = { item_id = "skin_red" } })
        t.eq(r.status, 200)
        t.eq(r.body.data.wallet.coins, 50)
        t.eq(r.body.data.owned.skin_red, 1)
        local again = t.call("economy/purchase", { user = ada, body = { item_id = "skin_red" } })
        t.eq(again.status, 409)
    end,

    purchase_without_funds_is_402 = function()
        local ada, bob = who(), who()
        local r = t.call("economy/purchase", { user = ada, body = { item_id = "skin_blue" } })
        t.eq(r.status, 402)
        t.eq(r.body.error.key, "insufficient_funds")
        t.eq(r.body.error.currency, "coins")
    end,

    booster_pack_and_consume = function()
        local ada, bob = who(), who()
        grant(ada, { currency = "coins", amount = 30 })
        local r = t.call("economy/purchase", { user = ada, body = { item_id = "bomb_pack" } })
        t.eq(r.body.data.boosters.bomb_pack, 3)
        local used = t.call("economy/consume_booster", { user = ada, body = { id = "bomb_pack" } })
        t.eq(used.body.data.boosters.bomb_pack, 2)
        t.eq(t.call("economy/consume_booster", { user = bob, body = { id = "bomb_pack" } }).status, 409)
    end,

    equip_needs_ownership = function()
        local ada, bob = who(), who()
        t.eq(t.call("economy/equip", { user = ada, body = { slot = "skin", item_id = "skin_red" } }).status, 409)
        grant(ada, { currency = "coins", amount = 100 })
        t.call("economy/purchase", { user = ada, body = { item_id = "skin_red" } })
        local r = t.call("economy/equip", { user = ada, body = { slot = "skin", item_id = "skin_red" } })
        t.eq(r.status, 200)
        t.eq(r.body.data.equipped.skin, "skin_red")
    end,

    upgrade_costs_grow = function()
        local ada, bob = who(), who()
        grant(ada, { currency = "coins", amount = 200 })
        local one = t.call("economy/upgrade", { user = ada, body = { id = "speed" } })
        t.eq(one.body.data.upgrades.speed, 1)
        t.eq(one.body.data.wallet.coins, 150) -- 50
        local two = t.call("economy/upgrade", { user = ada, body = { id = "speed" } })
        t.eq(two.body.data.wallet.coins, 75) -- floor(50 * 1.5)
    end,

    gacha_spends_gems_and_pays_a_reward = function()
        local ada, bob = who(), who()
        t.eq(t.call("economy/gacha", { user = ada }).status, 402)
        grant(ada, { currency = "gems", amount = 10 })
        local r = t.call("economy/gacha", { user = ada })
        t.eq(r.status, 200)
        t.ok(r.body.data.reward ~= nil, "a gacha pull pays a reward")
    end,

    settle_is_capped_per_call_and_per_day = function()
        local ada, bob = who(), who()
        local r = t.call("economy/settle", { user = ada, body = { coins = 99999 } })
        t.eq(r.body.data.granted.coins, 500)
        local total = 500
        for _ = 1, 12 do
            local more = t.call("economy/settle", { user = ada, body = { coins = 500 } })
            total = total + (more.body.data.granted.coins or 0)
        end
        t.eq(total, 5000)
        t.clock.advance(86400)
        t.eq(t.call("economy/settle", { user = ada, body = { coins = 500 } }).body.data.granted.coins, 500)
    end,

    internal_actions_are_not_public = function()
        local ada, bob = who(), who()
        t.eq(t.call("economy/grant", { user = ada, body = { currency = "coins", amount = 5 } }).status, 404)
        t.eq(t.call("economy/debit", { user = ada, body = { currency = "coins", amount = 5 } }).status, 404)
        t.eq(t.call("economy/nope", { user = ada, method = "GET" }).status, 404)
    end,

    debit_refuses_overdraft = function()
        local ada, bob = who(), who()
        grant(ada, { currency = "coins", amount = 10 })
        local r = t.call("economy/debit", { user = ada, method = "CALL", body = { currency = "coins", amount = 11 } })
        t.eq(r.status, 402)
        t.eq(state_of(ada).wallet.coins, 10)
    end,

    grant_a_reward_table = function()
        local ada, bob = who(), who()
        local r = grant(ada, { reward = { coins = 20, gems = 2, items = { bomb_pack = 1 } } })
        t.eq(r.body.data.wallet.coins, 20)
        t.eq(r.body.data.wallet.gems, 2)
        t.eq(state_of(ada).boosters.bomb_pack, 3)
    end,

    users_do_not_share_wallets = function()
        local ada, bob = who(), who()
        grant(ada, { currency = "coins", amount = 99 })
        t.eq(state_of(bob).wallet.coins, nil)
    end,
}
