-- A signed-in player must not choose the number stored for them on a board.
-- POST submit currently keeps body.score after a run ticket and a wait. These tests
-- fail while that is true and pass once the stored score is not the caller's number.
--
-- Run with the Scripted API emulator:
--   chauffeur sapi test --dir sapi_templates --plan free
-- or, with the emulator binary directly:
--   games_sapi --test sapi_templates --plan free

local FORGED = 1000000000

local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function profile_of(user, board_name)
    return t.call("leaderboards/profile", {
        method = "GET",
        user = user,
        query = { board = board_name, user = user },
    })
end

local function score_on(user, board_name)
    local profile = profile_of(user, board_name)
    t.eq(profile.status, 200)
    local rank = profile.body.data.rank
    if rank == nil then
        return nil
    end
    return rank.score
end

local function top_score(user, board_name)
    local top = t.call("leaderboards/top", { method = "GET", query = { board = board_name, n = "100" } })
    t.eq(top.status, 200)
    for _, entry in ipairs(top.body.data.entries) do
        if entry.user == user then
            return entry.score, entry.rank
        end
    end
    return nil, nil
end

-- Ticket plus the minimum elapsed time is the attack's whole gate. A refusal for a
-- bad ticket, a fast run, or an expired ticket is a different check and does not count.
local function forge(user, board_name, with_details)
    local started = t.call("leaderboards/start_run", { user = user, body = { board = board_name } })
    t.eq(started.status, 200)
    t.clock.advance(3)
    local body = { board = board_name, score = FORGED, run_ticket = started.body.data.run_ticket }
    if with_details then
        body.details = { moves = 1 }
    end
    local submitted = t.call("leaderboards/submit", { user = user, body = body })
    if submitted.status ~= 200 then
        local key = submitted.body and submitted.body.error and submitted.body.error.key
        t.ok(key ~= "bad_ticket" and key ~= "too_fast" and key ~= "stale", "a ticket and a three-second wait still reached the score")
    end
    return submitted
end

local function not_the_forged_score(got, where)
    t.ok(got ~= FORGED, where .. " kept the caller-chosen score " .. tostring(got))
end

return {
    -- Attack: start_run, wait three seconds, submit 1000000000, GET top.
    caller_cannot_place_the_maximum_on_the_public_main_board = function()
        local ada = who()
        forge(ada, "main", false)
        local listed, place = top_score(ada, "main")
        t.ok(listed ~= FORGED, "top kept the caller-chosen score " .. tostring(listed) .. " at rank " .. tostring(place))
        not_the_forged_score(score_on(ada, "main"), "profile")
        local around = t.call("leaderboards/around", { method = "GET", user = ada, query = { board = "main", n = "5" } })
        t.eq(around.status, 200)
        local me = around.body.data.me
        not_the_forged_score(me and me.score, "around")
        if me == nil then
            for _, entry in ipairs(around.body.data.entries) do
                if entry.user == ada then
                    not_the_forged_score(entry.score, "around entries")
                end
            end
        end
    end,

    caller_cannot_place_the_maximum_on_the_weekly_board = function()
        local ada = who()
        forge(ada, "weekly", false)
        not_the_forged_score(score_on(ada, "weekly"), "weekly profile")
        not_the_forged_score(top_score(ada, "weekly"), "weekly top")
    end,

    caller_cannot_store_the_chosen_score_in_run_details = function()
        local ada = who()
        forge(ada, "main", true)
        local profile = profile_of(ada, "main")
        t.eq(profile.status, 200)
        local details = profile.body.data.details
        not_the_forged_score(details and details.score, "run details")
        not_the_forged_score(profile.body.data.rank and profile.body.data.rank.score, "profile beside details")
    end,
}
