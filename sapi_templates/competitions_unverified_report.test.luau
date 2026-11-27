-- A signed-in player must not invent season points. These tests fail until match
-- points and claim rewards ignore client-supplied win, draw and loss values.
-- Player ids are fixed prefixes so a shared test database cannot collide with
-- the random ids in competitions.test.luau.

local function report(user, id, result)
    return t.call("competitions/report_match", { user = user, body = { match_id = id, result = result } })
end

local function points_of(user)
    local status = t.call("competitions/status", { user = user, method = "GET" })
    t.eq(status.status, 200)
    return status.body.data.points or 0, status.body.data
end

-- Join enough fresh players to own one full bracket. Earlier tests in the same
-- run share the open-bracket counter, so the first joins may only fill a bracket
-- that already has other players.
local function own_full_bracket()
    local mine = {}
    for i = 1, 16 do
        local id = string.format("forge%02d", i)
        mine[id] = true
        local joined = t.call("competitions/join", { user = id })
        t.eq(joined.status, 200, "join " .. id)
        local _, data = points_of(id)
        local standings = data.standings or {}
        if #standings == 8 then
            local pure = true
            local users = {}
            for _, row in ipairs(standings) do
                if not mine[row.user] then
                    pure = false
                end
                table.insert(users, row.user)
            end
            if pure then
                table.sort(users)
                return data.season, users
            end
        end
    end
    error("no full bracket contained only this test's players")
end

return {
    -- One draw and one win are enough to show the caller chooses the points.
    client_supplied_results_do_not_add_points = function()
        local ada = "forgesolo"
        t.eq(t.call("competitions/join", { user = ada }).status, 200)
        local draw = report(ada, "invented-draw", "draw")
        local win = report(ada, "invented-win", "win")
        local points = points_of(ada)
        if points ~= 0 then
            error(string.format(
                "client results changed season points: points=%s draw_status=%s draw_gained=%s win_status=%s win_gained=%s",
                tostring(points),
                tostring(draw.status),
                tostring(draw.body and draw.body.data and draw.body.data.gained),
                tostring(win.status),
                tostring(win.body and win.body.data and win.body.data.gained)
            ))
        end
        -- A refusal that still stored the points is the same bug.
        t.eq(points, 0)
    end,

    -- Twenty invented wins put the caller alone at the top of an eight-player
    -- bracket. After the week, claim_season pays that rank, including gems.
    forged_wins_do_not_take_first_place_or_pay_gems = function()
        local season, users = own_full_bracket()
        local forger = users[#users]
        local draw = report(forger, "invented-draw", "draw")
        local gained = 0
        local last = draw
        for n = 1, 19 do
            last = report(forger, "invented-win-" .. n, "win")
            if last.body and last.body.data and type(last.body.data.gained) == "number" then
                gained = gained + last.body.data.gained
            end
        end
        if draw.body and draw.body.data and type(draw.body.data.gained) == "number" then
            gained = gained + draw.body.data.gained
        end
        local points = points_of(forger)
        t.clock.advance(8 * 86400)
        local claimed = t.call("competitions/claim_season", { user = forger, body = { season = season } })
        local wallet = t.call("economy/state", { user = forger, method = "GET" })
        local reward = claimed.body and claimed.body.data and claimed.body.data.reward
        local rank = claimed.body and claimed.body.data and claimed.body.data.rank
        local wallet_gems = wallet.body and wallet.body.data and wallet.body.data.wallet and wallet.body.data.wallet.gems
        if points ~= 0 or rank ~= 8 or (reward and reward.gems ~= nil) or wallet_gems ~= nil then
            error(string.format(
                "forged reports scored the bracket: points=%s gained=%s rank=%s reward_gems=%s wallet_gems=%s claim_status=%s last_report=%s",
                tostring(points),
                tostring(gained),
                tostring(rank),
                tostring(reward and reward.gems),
                tostring(wallet_gems),
                tostring(claimed.status),
                tostring(last.status)
            ))
        end
        t.eq(points, 0)
        t.eq(rank, 8)
        t.eq(reward.coins, 500, "last place in a full bracket is the coins tier, not gems")
        t.eq(wallet_gems, nil)
    end,
}
