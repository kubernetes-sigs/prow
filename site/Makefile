
# Serve the site locally.
serve:
	hugo server

# Build the site. Among other things, this populates the public/ folder.
build:
	hugo

# Used by Netlify to build the site.
netlify-build: build

netlify-deploy-preview:
	hugo --enableGitInfo --buildFuture -b $(DEPLOY_PRIME_URL)

check-broken-links:
	find ./public -name "*.html" -print0 | sort -z | xargs -0 ./check-broken-links.sh
