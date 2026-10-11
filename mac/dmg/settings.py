# dmgbuild's settings for EmberStorm.dmg (mac/make-dmg.sh passes app=).
import os

application = defines["app"]  # noqa: F821 - given by dmgbuild

format = "UDZO"
filesystem = "HFS+"
files = [application]
symlinks = {"Applications": "/Applications"}
icon_locations = {os.path.basename(application): (160, 190), "Applications": (440, 190)}

background = defines["background"]  # noqa: F821 - the 1x and 2x pictures as one TIFF
window_rect = ((200, 120), (600, 350))
default_view = "icon-view"
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
icon_size = 112
text_size = 13
