-- Shared helpers for the canonical feature scripts. Not a public route (it lives in lib/).
--
--   local router = require("lib.router")
--   local actions = {}
--   actions["GET:status"] = function(req) return router.ok({ hello = "world" }) end
--   actions["POST:custom/craft"] = function(req) ... end   -- a developer-added action
--   actions["CALL:grant"] = function(req) ... end           -- internal, only via api.call("feature/grant")
--   return { auth = "user", methods = { "GET", "POST" }, handler = router.dispatch(actions) }
--
-- Public HTTP can only send real verbs, so "CALL:*" actions are unreachable from outside the pack.
local router = {}

-- Success envelope. `version` is the player's state_version so a client can skip a re-render.
function router.ok(data, version)
    return { ok = true, data = data, server_time = std.time.now(), state_version = version }
end

-- Failure envelope with an HTTP status. `extra` may carry limit / upgrade hints.
function router.fail(status, key, message, extra)
    local err = { key = key, message = message }
    if extra then
        for k, v in pairs(extra) do
            err[k] = v
        end
    end
    return { status = status, body = { ok = false, error = err } }
end

function router.bad_request(message)
    return router.fail(400, "bad_request", message or "the request is not valid")
end

-- True when a handler (or an api.call result) is a failure envelope.
function router.failed(result)
    return result == nil or (type(result) == "table" and result.status ~= nil and result.status >= 400)
end

-- A plan-limit failure that tells the client how to unlock more.
function router.plan_limit(limit_key, limit, message)
    return router.fail(403, "plan_limit", message or "this plan's limit was reached", {
        limit = limit,
        limit_key = limit_key,
        upgrade = "https://blazium.games/pricing",
    })
end

-- Validate a request body against a std.validate schema, returning (body, failure).
function router.body(req, schema)
    local ok, err = std.validate(req.body or {}, schema)
    if not ok then
        return nil, router.bad_request(err)
    end
    return req.body or {}, nil
end

-- Turn a table of "<METHOD>:<action>" handlers into a logic handler. Falls back to "*:<action>".
function router.dispatch(actions)
    return function(req)
        local action = req.action or ""
        local handler = actions[req.method .. ":" .. action] or actions["*:" .. action]
        if handler == nil then
            return router.fail(404, "unknown_action", req.logic .. " has no " .. req.method .. " action named '" .. action .. "'")
        end
        return handler(req)
    end
end

return router
