-- Every test uses fresh player ids: the test game's data is shared between the tests of one run.
local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function ids(rows)
    local out = {}
    for i, q in ipairs(rows) do
        out[i] = q.id
    end
    return out
end

return {
    rotation_is_the_same_for_every_player_on_a_day = function()
        local a = t.call("quests/status", { user = who(), method = "GET" }).body.data
        local b = t.call("quests/status", { user = who(), method = "GET" }).body.data
        t.eq(#a.daily, 3)
        t.eq(#a.weekly, 2)
        t.eq(ids(a.daily), ids(b.daily))
        t.eq(ids(a.weekly), ids(b.weekly))
    end,

    rotation_changes_with_the_day = function()
        local who1 = who()
        local seen = {}
        for day = 1, 6 do
            local s = t.call("quests/status", { user = who1, method = "GET" }).body.data
            seen[table.concat(ids(s.daily), ",")] = true
            t.clock.advance(86400)
        end
        local distinct = 0
        for _ in pairs(seen) do
            distinct = distinct + 1
        end
        t.ok(distinct > 1, "the daily set rotates")
    end,

    progress_and_claim_pay_through_the_economy = function()
        local ada = who()
        local s = t.call("quests/status", { user = ada, method = "GET" }).body.data
        local quest = s.daily[1]
        t.call("quests/progress", { user = ada, body = { event = quest.event, amount = 1 } })
        -- the client may only move a quest a tenth of its target per call, so finish it with an internal event
        t.call("quests/event", { user = ada, method = "CALL", body = { event = quest.event, amount = quest.target } })
        local claimed = t.call("quests/claim", { user = ada, body = { id = quest.id } })
        t.eq(claimed.status, 200)
        t.eq(t.call("quests/claim", { user = ada, body = { id = quest.id } }).status, 409)
        local wallet = t.call("economy/state", { user = ada, method = "GET" }).body.data.wallet
        t.ok((wallet.coins or 0) + (wallet.gems or 0) > 0, "the reward was paid")
    end,

    claim_needs_a_finished_quest = function()
        local ada = who()
        local s = t.call("quests/status", { user = ada, method = "GET" }).body.data
        t.eq(t.call("quests/claim", { user = ada, body = { id = s.daily[1].id } }).status, 409)
        t.eq(t.call("quests/claim", { user = ada, body = { id = "not_a_quest" } }).status, 404)
    end,

    client_progress_is_capped_per_call = function()
        local ada = who()
        local s = t.call("quests/status", { user = ada, method = "GET" }).body.data
        local quest = s.daily[1]
        local r = t.call("quests/progress", { user = ada, body = { event = quest.event, amount = 10000 } })
        t.eq(r.status, 200)
        t.eq(r.body.data.capped, true)
        t.ok(r.body.data.daily[1].progress < quest.target + 1)
    end,

    daily_progress_resets_at_midnight_utc = function()
        local ada = who()
        local s = t.call("quests/status", { user = ada, method = "GET" }).body.data
        local quest = s.daily[1]
        t.call("quests/event", { user = ada, method = "CALL", body = { event = quest.event, amount = 1 } })
        t.clock.advance(86400)
        local next_day = t.call("quests/status", { user = ada, method = "GET" }).body.data
        for _, q in ipairs(next_day.daily) do
            t.eq(q.progress, 0)
        end
    end,
}
