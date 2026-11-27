-- Pay a reward table through the economy (and lives) scripts, in the caller's write batch.
--   reward.give({ coins = 50, gems = 1, lives = 1, items = { bomb_pack = 1 } })
-- Returns the failure envelope if a payout was refused, otherwise nil. A feature script keeps working in a pack
-- that has no economy or lives script: the missing peer is reported in `missing` and nothing is paid for it.
local router = require("lib.router")

local reward = {}

function reward.give(r)
    if r == nil then
        return nil, {}
    end
    local missing = {}
    local rest = {}
    local lives = r.lives
    for k, v in pairs(r) do
        if k ~= "lives" then
            rest[k] = v
        end
    end
    if next(rest) ~= nil then
        local ok, result = pcall(api.call, "economy/grant", { reward = rest })
        if not ok then
            table.insert(missing, "economy")
        elseif router.failed(result) then
            return result, missing
        end
    end
    if lives ~= nil then
        local ok, result = pcall(api.call, "lives/grant", { n = lives })
        if not ok then
            table.insert(missing, "lives")
        elseif router.failed(result) then
            return result, missing
        end
    end
    return nil, missing
end

-- Charge a cost table ({ coins = 20 }) through economy/debit. Returns the failure envelope on refusal.
function reward.charge(cost)
    for currency, amount in pairs(cost or {}) do
        local ok, result = pcall(api.call, "economy/debit", { currency = currency, amount = amount })
        if not ok then
            return router.fail(501, "feature_missing", "this action needs the economy feature")
        end
        if router.failed(result) then
            return result
        end
    end
    return nil
end

return reward
