-- Hub: one call that boots a game session (spec 01, offline fallback and first-paint).
--
--   GET /v1/{game}/hub/bootstrap   the status of every feature script in this pack, plus the server time and plan
--
-- The hub calls each installed feature's own status action with api.call, so it needs no data or utilities of its
-- own and always agrees with the feature scripts. A feature that is not in the pack (or is turned off for the
-- game) is simply absent from the reply; `enabled` lists the ones that answered, so a client can hide the rest. Set hub_config.features to change the list or its order.
local router = require("lib.router")
local guard = require("lib.guard")

local DEFAULT_FEATURES = {
    { id = "economy", action = "state" },
    { id = "daily", action = "status" },
    { id = "lives", action = "status" },
    { id = "wheel", action = "status" },
    { id = "chests", action = "status" },
    { id = "quests", action = "status" },
    { id = "meta", action = "status" },
    { id = "competitions", action = "status" },
    { id = "leaderboards", action = "daily_seed" },
}

local actions = {}

actions["GET:bootstrap"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local list = config.get("hub_config", {}).features or DEFAULT_FEATURES
    local features = {}
    local enabled = {}
    local absent = {}
    for _, f in ipairs(list) do
        local ok, result = pcall(api.call, f.id .. "/" .. f.action, nil, { method = "GET", query = req.query })
        if ok and not router.failed(result) then
            features[f.id] = result.data
            table.insert(enabled, f.id)
        else
            table.insert(absent, f.id)
        end
    end
    return router.ok({ features = features, enabled = enabled, absent = absent, plan = req.plan, channel = req.channel })
end

-- Moves one player into another, feature by feature, in one request so the writes commit together. Platform and service
-- callers only: POST /internal/games/{game}/merge-user runs it after a guest signs in. A feature that is not in the pack or
-- is turned off is skipped, and so is one with nothing stored for the player being merged.
local MERGE_ORDER = { "economy", "daily", "lives", "wheel", "chests", "quests", "meta", "competitions", "leaderboards" }

local function merge_users(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local from, to, bad = guard.merge(req)
    if bad then
        return bad
    end
    local merged, skipped = {}, {}
    for _, id in ipairs(MERGE_ORDER) do
        local ok, result = pcall(api.call, id .. "/merge", { from = from, to = to })
        if ok and not router.failed(result) and result.data and result.data.merged then
            table.insert(merged, id)
        else
            table.insert(skipped, id)
        end
    end
    return router.ok({ merged = merged, skipped = skipped })
end

actions["POST:merge"] = merge_users
actions["CALL:merge"] = merge_users

return {
    auth = "user",
    methods = { "GET", "POST" },
    rate = { per_user = 30, window = 60 },
    description = "Bootstrap: every installed feature's status in one request",
    handler = router.dispatch(actions),
}
