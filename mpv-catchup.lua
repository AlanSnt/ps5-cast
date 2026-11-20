-- Loaded by the mpv window that ps5cast opens.
--
-- mpv plays a live stream at normal speed, so the delay left by a stall (a busy
-- machine, a window being moved) would stay until the window is restarted.
-- While mpv holds more of the stream than it should, this plays slightly
-- faster, until the picture is live again.

local chase_above = 0.3 -- seconds waiting in mpv that start the catch-up
local live_below = 0.05 -- seconds waiting in mpv that end it
local chase_speed = 1.1 -- the rate the web page uses too

mp.observe_property("demuxer-cache-duration", "number", function(_, waiting)
    if waiting == nil then
        return
    end
    if waiting > chase_above then
        mp.set_property_number("speed", chase_speed)
    elseif waiting < live_below then
        mp.set_property_number("speed", 1)
    end
end)
