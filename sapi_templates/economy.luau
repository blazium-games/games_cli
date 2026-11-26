-- Economy (spec 04): wallets, boosters, cosmetics, upgrades, gacha and run settlement.
--
--   GET  /v1/{game}/economy/state            wallet, owned items, boosters, equipped slots, upgrades
--   POST /v1/{game}/economy/purchase         { item_id, quantity? }
--   POST /v1/{game}/economy/consume_booster  { id }
--   POST /v1/{game}/economy/equip            { slot, item_id }
--   POST /v1/{game}/economy/upgrade          { id }
--   POST /v1/{game}/economy/gacha            { pool? }
--   POST /v1/{game}/economy/settle           { coins?, gems? }   run rewards, capped per call and per day
--   CALL economy/grant { currency | item_id, amount | quantity }   (api.call only)
--   CALL economy/debit { currency, amount }                        (api.call only)
--
-- Balance lives in config "economy_config" (chauffeur sapi config set economy_config '{...}').
local router = require("lib.router")
local guard = require("lib.guard")
local state = require("lib.state")
local plan = require("lib.plan")

local KEY = "econ:state"

local DEFAULTS = {
    currencies = { "coins", "gems" },
    catalog = {
        skin_red = { price = { coins = 100 }, kind = "cosmetic", slot = "skin" },
        skin_blue = { price = { coins = 100 }, kind = "cosmetic", slot = "skin" },
        bomb_pack = { price = { coins = 30 }, kind = "booster", grant = 3 },
    },
    upgrades = { speed = { max = 5, currency = "coins", base = 50, growth = 1.5 } },
    gacha = {
        cost = { gems = 10 },
        pool = {
            { weight = 70, reward = { coins = 50 } },
            { weight = 25, reward = { coins = 200 } },
            { weight = 5, reward = { gems = 10 } },
        },
    },
    settle_max = { coins = 500, gems = 0 },
    settle_daily = { coins = 5000, gems = 0 },
}

local function cfg()
    return state.config("economy_config", DEFAULTS)
end

local function load()
    return state.load(KEY, { v = 0, w = {}, own = {}, boost = {}, eq = {}, up = {}, day = 0, settled = {} })
end

local function currency_ok(conf, name, req)
    local allowed = plan.take(req, "economy", "currencies", conf.currencies)
    for _, c in ipairs(allowed) do
        if c == name then
            return true
        end
    end
    return false
end

local function can_afford(s, price)
    for cur, amount in pairs(price) do
        if (s.w[cur] or 0) < amount then
            return false, cur
        end
    end
    return true, nil
end

local function charge(s, price)
    for cur, amount in pairs(price) do
        s.w[cur] = (s.w[cur] or 0) - amount
        if amount > 0 then
            progress.stat(cur .. "_spent", amount, { mode = "add" })
        end
    end
end

local function credit(s, reward)
    for cur, amount in pairs(reward) do
        s.w[cur] = (s.w[cur] or 0) + amount
    end
end

local function snapshot(s)
    return { wallet = s.w, owned = s.own, boosters = s.boost, equipped = s.eq, upgrades = s.up }
end

local actions = {}

actions["GET:state"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local s = load()
    local out = snapshot(s)
    -- Union with Blazium platform inventory (Layer 2 items). A bridge outage must not break the wallet.
    local ok, platform = pcall(items.list)
    if ok and type(platform) == "table" then
        out.platform_items = platform
    end
    return router.ok(out, s.v)
end

actions["POST:purchase"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, {
        type = "object",
        fields = { item_id = { type = "string", max_len = 48 }, quantity = { type = "integer", min = 1, max = 99, optional = true } },
    })
    if bad then
        return bad
    end
    local conf = cfg()
    local item = conf.catalog[body.item_id]
    if item == nil then
        return router.fail(404, "unknown_item", "no such item")
    end
    local qty = body.quantity or 1
    local s = load()
    if item.kind == "cosmetic" and s.own[body.item_id] then
        return router.fail(409, "already_owned", "you already own this item")
    end
    if item.kind == "cosmetic" then
        qty = 1
    end
    local price = {}
    for cur, amount in pairs(item.price or {}) do
        price[cur] = amount * qty
    end
    local ok, short = can_afford(s, price)
    if not ok then
        return router.fail(402, "insufficient_funds", "not enough " .. short, { currency = short })
    end
    charge(s, price)
    if item.kind == "booster" then
        s.boost[body.item_id] = (s.boost[body.item_id] or 0) + (item.grant or 1) * qty
    else
        s.own[body.item_id] = 1
    end
    local version = state.save(KEY, s)
    return router.ok(snapshot(s), version)
end

actions["POST:consume_booster"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", fields = { id = { type = "string", max_len = 48 } } })
    if bad then
        return bad
    end
    local s = load()
    if (s.boost[body.id] or 0) < 1 then
        return router.fail(409, "none_left", "you have none of this booster")
    end
    s.boost[body.id] = s.boost[body.id] - 1
    if s.boost[body.id] == 0 then
        s.boost[body.id] = nil
    end
    local version = state.save(KEY, s)
    return router.ok(snapshot(s), version)
end

actions["POST:equip"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, {
        type = "object",
        fields = { slot = { type = "string", max_len = 24 }, item_id = { type = "string", max_len = 48 } },
    })
    if bad then
        return bad
    end
    local conf = cfg()
    local item = conf.catalog[body.item_id]
    local s = load()
    if item == nil or item.kind ~= "cosmetic" or item.slot ~= body.slot or not s.own[body.item_id] then
        return router.fail(409, "not_owned", "you do not own an item for that slot")
    end
    s.eq[body.slot] = body.item_id
    local version = state.save(KEY, s)
    return router.ok(snapshot(s), version)
end

actions["POST:upgrade"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, { type = "object", fields = { id = { type = "string", max_len = 48 } } })
    if bad then
        return bad
    end
    local conf = cfg()
    local up = conf.upgrades[body.id]
    if up == nil then
        return router.fail(404, "unknown_upgrade", "no such upgrade")
    end
    local s = load()
    local level = s.up[body.id] or 0
    if level >= up.max then
        return router.fail(409, "max_level", "already at the highest level")
    end
    local price = { [up.currency] = math.floor(up.base * (up.growth ^ level)) }
    local ok, short = can_afford(s, price)
    if not ok then
        return router.fail(402, "insufficient_funds", "not enough " .. short, { currency = short })
    end
    charge(s, price)
    s.up[body.id] = level + 1
    local version = state.save(KEY, s)
    return router.ok(snapshot(s), version)
end

actions["POST:gacha"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local conf = cfg()
    local s = load()
    local ok, short = can_afford(s, conf.gacha.cost)
    if not ok then
        return router.fail(402, "insufficient_funds", "not enough " .. short, { currency = short })
    end
    local total = 0
    for _, row in ipairs(conf.gacha.pool) do
        total = total + row.weight
    end
    local roll = std.random.int(1, total)
    local reward = conf.gacha.pool[#conf.gacha.pool].reward
    for _, row in ipairs(conf.gacha.pool) do
        roll = roll - row.weight
        if roll <= 0 then
            reward = row.reward
            break
        end
    end
    charge(s, conf.gacha.cost)
    credit(s, reward)
    local version = state.save(KEY, s)
    return router.ok({ reward = reward, wallet = s.w }, version)
end

-- Run rewards are reported by the client, so they are capped per call and per UTC day.
actions["POST:settle"] = function(req)
    local denied = guard.user(req)
    if denied then
        return denied
    end
    local body, bad = router.body(req, {
        type = "object",
        fields = { coins = { type = "integer", min = 0, optional = true }, gems = { type = "integer", min = 0, optional = true } },
    })
    if bad then
        return bad
    end
    local conf = cfg()
    local s = load()
    local today = state.day()
    if s.day ~= today then
        s.day = today
        s.settled = {}
    end
    local granted = {}
    for _, cur in ipairs({ "coins", "gems" }) do
        local asked = body[cur] or 0
        local per_call = (conf.settle_max or {})[cur] or 0
        local per_day = (conf.settle_daily or {})[cur] or 0
        local room = math.max(0, per_day - (s.settled[cur] or 0))
        local amount = math.min(asked, per_call, room)
        if amount > 0 then
            granted[cur] = amount
            s.settled[cur] = (s.settled[cur] or 0) + amount
        end
    end
    credit(s, granted)
    local version = state.save(KEY, s)
    return router.ok({ granted = granted, wallet = s.w }, version)
end

-- Internal: other scripts pay out or charge through these so everything commits in one batch.
actions["CALL:grant"] = function(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local body = req.body or {}
    local conf = cfg()
    local s = load()
    if body.reward then
        -- One call can pay several currencies and items: { coins = 50, gems = 1, items = { bomb_pack = 1 } }.
        for key, amount in pairs(body.reward) do
            if key == "items" then
                for item_id, quantity in pairs(amount) do
                    local item = conf.catalog[item_id]
                    if item == nil then
                        return router.fail(404, "unknown_item", "no such item")
                    end
                    if item.kind == "booster" then
                        s.boost[item_id] = (s.boost[item_id] or 0) + (item.grant or 1) * quantity
                    else
                        s.own[item_id] = 1
                    end
                end
            elseif currency_ok(conf, key, req) and type(amount) == "number" and amount >= 1 then
                s.w[key] = (s.w[key] or 0) + math.floor(amount)
            else
                return router.fail(400, "unknown_currency", "no such currency: " .. tostring(key))
            end
        end
    elseif body.currency then
        if not currency_ok(conf, body.currency, req) then
            return router.fail(400, "unknown_currency", "no such currency")
        end
        local amount = math.floor(body.amount or 0)
        if amount < 1 then
            return router.bad_request("amount must be at least 1")
        end
        s.w[body.currency] = (s.w[body.currency] or 0) + amount
    elseif body.item_id then
        local item = conf.catalog[body.item_id]
        if item == nil then
            return router.fail(404, "unknown_item", "no such item")
        end
        if item.kind == "booster" then
            s.boost[body.item_id] = (s.boost[body.item_id] or 0) + (item.grant or 1) * (body.quantity or 1)
        else
            s.own[body.item_id] = 1
        end
    else
        return router.bad_request("grant needs currency or item_id")
    end
    local version = state.save(KEY, s)
    return router.ok({ wallet = s.w }, version)
end

actions["CALL:debit"] = function(req)
    local denied = guard.internal(req)
    if denied then
        return denied
    end
    local body = req.body or {}
    local amount = math.floor(body.amount or 0)
    if type(body.currency) ~= "string" or amount < 1 then
        return router.bad_request("debit needs a currency and a positive amount")
    end
    local s = load()
    local price = { [body.currency] = amount }
    local ok, short = can_afford(s, price)
    if not ok then
        return router.fail(402, "insufficient_funds", "not enough " .. short, { currency = short })
    end
    charge(s, price)
    local version = state.save(KEY, s)
    return router.ok({ wallet = s.w }, version)
end

return {
    auth = "user",
    methods = { "GET", "POST" },
    uses = { "items", "progress" },
    data = { read = { "user" }, write = { "user" } },
    rate = { per_user = 60, window = 60 },
    description = "Wallets, boosters, cosmetics, upgrades, gacha and run settlement",
    handler = router.dispatch(actions),
}
