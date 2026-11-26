-- Caller checks for feature actions. Each returns nil when the caller may continue, or a failure envelope.
local router = require("lib.router")

local guard = {}

-- A signed-in player (or a server key acting for one).
function guard.user(req)
    if req.user == nil then
        return router.fail(401, "unauthorized", "this action needs a signed-in player")
    end
    return nil
end

-- Only another script in this pack (api.call) or a platform/service caller.
function guard.internal(req)
    if req.method ~= "CALL" and req.caller ~= "service" and req.caller ~= "platform" then
        return router.fail(403, "forbidden", "this action is internal")
    end
    return nil
end

-- Only a server key, the schedule or an admin. Never a player.
function guard.server(req)
    if req.caller ~= "service" and req.caller ~= "schedule" and req.caller ~= "admin" and req.caller ~= "platform" then
        return router.fail(403, "forbidden", "this action is for game servers")
    end
    return nil
end

-- A positive whole number inside [1, max].
function guard.count(value, max)
    if type(value) ~= "number" or value ~= math.floor(value) or value < 1 or value > max then
        return false
    end
    return true
end

return guard
