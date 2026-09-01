-- Ride-hailing authorized-access variant.
--
-- This profile is intentionally not a public-by-default driving profile. It
-- allows selected destination-style restrictions when the calling application
-- has already established that the vehicle is authorized to enter. Explicit
-- prohibitions and physical vehicle barriers remain enforced by the upstream
-- car profile.

local base = dofile("/opt/car.lua")
local base_setup = base.setup
local base_process_node = base.process_node
local base_process_way = base.process_way

local find_access_tag = require("lib/access").find_access_tag
local resolve_access = require("lib/access").resolve_access

local authorized_access_values = {
  private = true,
  residents = true,
  customers = true,
  destination = true,
  delivery = true,
  permit = true,
}

local access_hierarchy = {
  "motorcar",
  "motor_vehicle",
  "vehicle",
  "access",
}

local function directional_access(way, direction)
  for _, key in ipairs(access_hierarchy) do
    local value = way:get_value_by_key(key .. ":" .. direction)
    if value then
      return value
    end

    value = way:get_value_by_key(key)
    if value then
      return value
    end
  end

  return nil
end

local function mark_authorized_access(profile, way, result, direction)
  local access = resolve_access(directional_access(way, direction), profile)
  if access and authorized_access_values[access] then
    result[direction .. "_classes"]["restricted"] = true
  end
end

function setup()
  local profile = base_setup()

  -- Keep access classes visible on local roads, where authorized shortcuts
  -- are most likely to occur.
  profile.restricted_highway_whitelist["residential"] = true

  -- Convert only the selected ride-hailing access values from restricted
  -- roads into routable roads. Values such as no, military, and emergency
  -- remain blocked by the upstream car profile.
  for value in pairs(authorized_access_values) do
    profile.access_tag_whitelist[value] = true
    profile.access_tag_blacklist[value] = nil
    profile.restricted_access_tag_list[value] = nil
  end

  -- A private service road is also eligible in the authorized graph.
  profile.service_access_tag_blacklist["private"] = nil

  -- Keep restricted as an inspectable route class and allow Xicar to probe
  -- whether a public-only alternative exists.
  table.insert(profile.excludable, Set { "restricted" })

  return profile
end

function process_node(profile, node, result, relations)
  base_process_node(profile, node, result, relations)

  -- The upstream profile skips barrier processing when an access tag is
  -- present. Re-add the normal gate delay for an authorized gate so entering
  -- with permission still models the time spent at the access control point.
  local access = resolve_access(find_access_tag(node, profile.access_tags_hierarchy), profile)
  local barrier = node:get_value_by_key("barrier")
  if access and authorized_access_values[access]
      and (barrier == "gate" or barrier == "lift_gate") then
    obstacle_map:add(node, Obstacle.new(
      obstacle_type.gate,
      obstacle_direction.both,
      60,
      0
    ))
  end
end

function process_way(profile, way, result, relations)
  local processed = base_process_way(profile, way, result, relations)

  -- Preserve an inspectable class after removing the upstream restricted-road
  -- penalty. Xicar uses route steps to distinguish authorized access from a
  -- normal public route.
  mark_authorized_access(profile, way, result, "forward")
  mark_authorized_access(profile, way, result, "backward")

  return processed
end

return {
  setup = setup,
  process_node = process_node,
  process_way = process_way,
  process_turn = base.process_turn,
}
