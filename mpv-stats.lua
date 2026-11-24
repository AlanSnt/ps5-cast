-- Loaded by the mpv window that ps5cast opens.
--
-- Prints once a second how the playback goes. ps5cast keeps these lines for
-- its debug log, where they tell whether the window itself is what lags:
-- pictures dropped because the PC decodes or draws too slowly, sound and
-- picture apart, or stream waiting in mpv.

local function number(property)
    return mp.get_property_number(property, -1) -- -1: mpv does not know yet
end

mp.msg.info(mp.get_property("mpv-version", "mpv of an unknown version"))

mp.add_periodic_timer(1, function()
    mp.msg.info(string.format(
        "pos=%.3f speed=%.2f avsync=%.3f waiting=%.3f fps=%.1f display_fps=%.1f"
            .. " dropped=%.0f decoder_dropped=%.0f delayed=%.0f mistimed=%.0f"
            .. " hwdec=%s vo=%s size=%.0fx%.0f",
        number("time-pos"), number("speed"), number("avsync"), number("demuxer-cache-duration"),
        number("estimated-vf-fps"), number("estimated-display-fps"),
        number("frame-drop-count"), number("decoder-frame-drop-count"),
        number("vo-delayed-frame-count"), number("mistimed-frame-count"),
        mp.get_property("hwdec-current", "?"), mp.get_property("current-vo", "?"),
        number("width"), number("height")))
end)
