-- Settlement must be accepted only from a game server that names the player, the same way
-- quests/progress and competitions/report_match already work. A signed-in player's own
-- settle call must not create coins or gems, and must not leave a spendable balance behind
-- if it is refused. Per-call and per-day caps stay in force for a real server settlement.
--
-- These tests fail while a player can mint currency by calling economy/settle.

local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function state_of(user)
    return t.call("economy/state", { user = user, method = "GET" }).body.data
end

return {
    -- The reported attack: POST settle as the player, then buy a starter-catalog cosmetic.
    player_settle_does_not_mint_spendable_coins = function()
        local ada = who()
        local settled = t.call("economy/settle", { user = ada, body = { coins = 500, gems = 500 } })
        t.eq(settled.status, 403)
        t.eq(settled.body.error.key, "forbidden")
        t.eq(state_of(ada).wallet.coins, nil)
        t.eq(state_of(ada).wallet.gems, nil)

        local cosmetic = t.call("economy/purchase", { user = ada, body = { item_id = "skin_red" } })
        t.eq(cosmetic.status, 402)
        t.eq(cosmetic.body.error.key, "insufficient_funds")
        t.eq(state_of(ada).owned.skin_red, nil)

        local booster = t.call("economy/purchase", { user = ada, body = { item_id = "bomb_pack" } })
        t.eq(booster.status, 402)
        t.eq(state_of(ada).boosters.bomb_pack, nil)

        local upgrade = t.call("economy/upgrade", { user = ada, body = { id = "speed" } })
        t.eq(upgrade.status, 402)
        t.eq(state_of(ada).upgrades.speed, nil)

        local equip = t.call("economy/equip", { user = ada, body = { slot = "skin", item_id = "skin_red" } })
        t.eq(equip.status, 409)
        t.eq(state_of(ada).equipped.skin, nil)
    end,

    -- Repeating the call must not accumulate the daily cap into the player's wallet.
    repeated_player_settle_does_not_reach_the_daily_cap = function()
        local ada = who()
        local first = t.call("economy/settle", { user = ada, body = { coins = 99999 } })
        t.eq(first.status, 403)
        t.eq(state_of(ada).wallet.coins, nil)
        for _ = 1, 12 do
            local more = t.call("economy/settle", { user = ada, body = { coins = 500 } })
            t.eq(more.status, 403)
        end
        t.eq(state_of(ada).wallet.coins, nil)
        t.eq(state_of(ada).wallet.gems, nil)
        t.clock.advance(86400)
        local next_day = t.call("economy/settle", { user = ada, body = { coins = 500 } })
        t.eq(next_day.status, 403)
        t.eq(state_of(ada).wallet.coins, nil)
    end,

    -- Coins from a player call must not land on that player or on anyone else.
    player_settle_does_not_credit_any_wallet = function()
        local ada, bob = who(), who()
        local settled = t.call("economy/settle", { user = ada, body = { coins = 500 } })
        t.eq(settled.status, 403)
        t.eq(state_of(ada).wallet.coins, nil)
        t.eq(state_of(bob).wallet.coins, nil)
    end,

    -- A refused settle must not be spendable even when the refusal is checked again via state.
    refused_settle_has_no_persisted_balance = function()
        local ada = who()
        t.eq(t.call("economy/settle", { user = ada, body = { coins = 500 } }).status, 403)
        local again = t.call("economy/settle", { user = ada, body = { coins = 500 } })
        t.eq(again.status, 403)
        t.eq(state_of(ada).wallet.coins, nil)
        t.eq(t.call("economy/purchase", { user = ada, body = { item_id = "skin_blue" } }).status, 402)
    end,

    anonymous_settle_is_rejected = function()
        local ada = who()
        local settled = t.call("economy/settle", { body = { coins = 500 } })
        t.eq(settled.status, 401)
        t.eq(state_of(ada).wallet.coins, nil)
    end,

    -- A game server names the player. Without that name nothing is credited.
    server_settle_without_a_player_is_rejected = function()
        local ada = who()
        local settled = t.call("economy/settle", { service = true, body = { coins = 500 } })
        t.eq(settled.status, 400)
        t.eq(state_of(ada).wallet.coins, nil)
    end,

    -- The named player's wallet is the one that changes. Another player's wallet does not.
    server_settle_credits_only_the_named_player = function()
        local ada, bob = who(), who()
        local settled = t.call("economy/settle", { service = true, user = ada, body = { coins = 100 } })
        t.eq(settled.status, 200)
        t.eq(settled.body.data.granted.coins, 100)
        t.eq(settled.body.data.wallet.coins, 100)
        t.eq(state_of(ada).wallet.coins, 100)
        t.eq(state_of(bob).wallet.coins, nil)
        t.eq(state_of(ada).wallet.gems, nil)
    end,

    -- Caps remain a backstop once a server is the only caller allowed to settle.
    server_settle_is_capped_per_call_and_per_day = function()
        local ada = who()
        local settled = t.call("economy/settle", { service = true, user = ada, body = { coins = 99999, gems = 99999 } })
        t.eq(settled.status, 200)
        t.eq(settled.body.data.granted.coins, 500)
        t.eq(settled.body.data.granted.gems, nil)
        local total = 500
        for _ = 1, 12 do
            local more = t.call("economy/settle", { service = true, user = ada, body = { coins = 500 } })
            t.eq(more.status, 200)
            total = total + (more.body.data.granted.coins or 0)
        end
        t.eq(total, 5000)
        t.eq(state_of(ada).wallet.coins, 5000)
        t.eq(state_of(ada).wallet.gems, nil)
        t.clock.advance(86400)
        local next_day = t.call("economy/settle", { service = true, user = ada, body = { coins = 500 } })
        t.eq(next_day.status, 200)
        t.eq(next_day.body.data.granted.coins, 500)
    end,

    -- Coins a server actually grants still buy the starter catalog. Forged player coins do not.
    server_granted_coins_can_buy_the_starter_catalog = function()
        local ada = who()
        t.eq(t.call("economy/settle", { user = ada, body = { coins = 5000 } }).status, 403)
        t.eq(t.call("economy/purchase", { user = ada, body = { item_id = "skin_red" } }).status, 402)
        local settled = t.call("economy/settle", { service = true, user = ada, body = { coins = 500 } })
        t.eq(settled.status, 200)
        t.eq(settled.body.data.wallet.coins, 500)
        local bought = t.call("economy/purchase", { user = ada, body = { item_id = "skin_red" } })
        t.eq(bought.status, 200)
        t.eq(bought.body.data.owned.skin_red, 1)
        t.eq(bought.body.data.wallet.coins, 400)
    end,
}
