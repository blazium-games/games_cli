-- A signed-in player must not finish a quest, or collect its coins or gems, by repeating
-- POST quests/progress. These tests fail while that client report is added to progress.
--
-- Each player stays inside the free-plan limit of 30 calls a minute and this script's limit
-- of 60: one status read, at most 24 reports, one reread, one claim, and one wallet read.
-- The report count is the published target divided by the same per-call cap the script uses
-- (a tenth of the target, or a tighter cap of another active quest with the same event).
-- A quest that needs more reports than that window still fails the test if any of the
-- reports that did fit moved its progress.

local function who()
    return "u" .. std.crypto.random_hex(6)
end

local function step_cap(quest)
    if type(quest.max_step) == "number" and quest.max_step >= 1 then
        return quest.max_step
    end
    return math.max(1, math.floor(quest.target / 10))
end

-- POST progress keeps only the smallest cap among active quests that share the event.
local function tightest_cap(view, event)
    local cap = nil
    for _, name in ipairs({ "daily", "weekly" }) do
        for _, quest in ipairs(view[name]) do
            if quest.event == event then
                local this_cap = step_cap(quest)
                if cap == nil or this_cap < cap then
                    cap = this_cap
                end
            end
        end
    end
    if cap == nil or cap < 1 then
        return 1
    end
    return cap
end

local function find_row(view, id)
    for _, name in ipairs({ "daily", "weekly" }) do
        for _, row in ipairs(view[name]) do
            if row.id == id then
                return row
            end
        end
    end
    return nil
end

local function report_until_target(user, quest, view, body_amount)
    local cap = tightest_cap(view, quest.event)
    local calls = math.floor((quest.target + cap - 1) / cap)
    local amount_label = tostring(body_amount)
    if body_amount == nil then
        -- Amount is optional and defaults to 1 inside POST progress.
        calls = quest.target
        amount_label = "omitted"
    end
    -- Leave room for the status read, the reread, the claim, and the wallet read.
    if calls > 24 then
        calls = 24
    end
    local reports = 0
    for _ = 1, calls do
        local body = { event = quest.event }
        if body_amount ~= nil then
            body.amount = body_amount
        end
        local posted = t.call("quests/progress", { user = user, body = body })
        reports = reports + 1
        if posted.status ~= 200 then
            break
        end
    end
    local after = t.call("quests/status", { user = user, method = "GET" })
    local row = find_row(after.body.data, quest.id)
    local claimed = t.call("quests/claim", { user = user, body = { id = quest.id } })
    local wallet = t.call("economy/state", { user = user, method = "GET" }).body.data.wallet or {}
    local coins = wallet.coins or 0
    local gems = wallet.gems or 0
    local progress = row and row.progress or -1
    local done = row and row.done or false
    if progress ~= 0 or done or claimed.status == 200 or coins ~= 0 or gems ~= 0 then
        return string.format(
            "%s event=%s target=%s cap=%s amount=%s reports=%s progress=%s done=%s claim=%s coins=%s gems=%s reward_coins=%s reward_gems=%s",
            quest.id,
            tostring(quest.event),
            tostring(quest.target),
            tostring(cap),
            amount_label,
            tostring(reports),
            tostring(progress),
            tostring(done),
            tostring(claimed.status),
            tostring(coins),
            tostring(gems),
            tostring(quest.reward and quest.reward.coins),
            tostring(quest.reward and quest.reward.gems)
        )
    end
    return nil
end

local function client_cannot_finish(kind)
    local sample = t.call("quests/status", { user = who(), method = "GET" })
    t.eq(sample.status, 200)
    local list = sample.body.data[kind]
    t.ok(#list > 0, "the free plan has " .. kind .. " quests")
    local problems = {}
    local smallest = list[1]
    for _, quest in ipairs(list) do
        local user = who()
        local opened = t.call("quests/status", { user = user, method = "GET" })
        t.eq(opened.status, 200)
        local problem = report_until_target(user, quest, opened.body.data, 10000)
        if problem ~= nil then
            table.insert(problems, problem)
        end
        if quest.target < smallest.target then
            smallest = quest
        end
    end
    local plain = who()
    local opened = t.call("quests/status", { user = plain, method = "GET" })
    t.eq(opened.status, 200)
    local plain_problem = report_until_target(plain, smallest, opened.body.data, nil)
    if plain_problem ~= nil then
        table.insert(problems, "default-amount " .. plain_problem)
    end
    t.ok(#problems == 0, table.concat(problems, " | "))
end

return {
    signed_in_player_cannot_finish_daily_quests_by_repeating_progress = function()
        client_cannot_finish("daily")
    end,

    signed_in_player_cannot_finish_weekly_quests_by_repeating_progress = function()
        client_cannot_finish("weekly")
    end,
}
