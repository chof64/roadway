-- The default routing policy delegates to the version-pinned OSRM car profile.
-- Keep this wrapper separate so the Docker build has an explicit, selectable
-- restrictive variant without duplicating upstream profile code.
return dofile("/opt/car.lua")
