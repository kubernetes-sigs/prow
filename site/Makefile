
# Serve the site locally.
serve:
	hugo server

# Build the site. Among other things, this populates the public/ folder.
build:
	hugo

# Update the "docsy" Hugo theme.
update-theme:
	git submodule update --init --recursive
	npm update

.PHONY: \
	update-theme
