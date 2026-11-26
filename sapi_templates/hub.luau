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

return {
    auth = "user",
    methods = { "GET" },
    rate = { per_user = 30, window = 60 },
    description = "Bootstrap: every installed feature's status in one request",
    handler = router.dispatch(actions),
}
